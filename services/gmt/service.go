package main

import (
	"context"
	"strings"
	"time"

	"kemiluo/pkg/async"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gmt/internal/config"
	"kemiluo/services/gmt/internal/handler"
	"kemiluo/services/gmt/internal/logic"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// gmtService 封装GMT管理后台服务的所有依赖与组装逻辑
type gmtService struct {
	cfg            *config.Config
	server         *httpx.Server
	mongoSvc       *mongox.Service
	configMongoSvc *mongox.Service
	redisSvc       *redisx.Service
	asyncSvc       *async.Service
	jwt            *authx.JWT
	tokenStore     *authx.TokenStore
	platformSvc    *httpcli.Service
	gamelogicSvc   *httpcli.Service
}

// newGmtService 创建并组装GMT服务
func newGmtService(cfg *config.Config) *gmtService {
	svc := &gmtService{cfg: cfg}
	svc.initInfra()
	svc.initQueues()
	svc.initServer()
	svc.ensureIndexes()
	svc.registerRoutes()
	return svc
}

// initInfra 初始化基础设施组件（MongoDB、Redis、Async、JWT、HTTP Client）。
// TokenStore 在此处独立创建 Redis 连接，因为 Fiber Service 的 Start() 要等到 server.Start() 才被调用，
// 而 initServer / registerRoutes 阶段就需要注入 tokenStore。
func (s *gmtService) initInfra() {
	s.mongoSvc = mongox.NewService(s.cfg.Mongo)
	// 游戏配置专用库（gconfig_db），与主库共享连接参数，仅库名不同
	s.configMongoSvc = mongox.NewService(s.cfg.ConfigMongo, "mongodb-config")
	s.redisSvc = redisx.NewService(s.cfg.Redis)
	s.asyncSvc = async.NewService()
	s.jwt = authx.NewJWT(s.cfg.JWT)
	s.platformSvc = httpcli.NewService(&httpcli.Config{
		BaseURL: s.cfg.PlatformURL,
		Timeout: 30 * time.Second,
		Secret:  s.cfg.InternalSecret,
	}, "platform")
	s.gamelogicSvc = httpcli.NewService(&httpcli.Config{
		BaseURL: s.cfg.GameLogicURL,
		Timeout: 30 * time.Second,
		Secret:  s.cfg.InternalSecret,
	}, "gamelogic")

	// TokenStore 独立创建 Redis 客户端，不依赖 Fiber Service 生命周期。
	tokenStoreCli, err := redisx.NewClient(context.Background(), s.cfg.Redis)
	if err != nil {
		logx.Warn("TokenStore Redis 连接失败，管理员令牌即时失效功能将不可用", zap.Error(err))
	} else {
		s.tokenStore = authx.NewTokenStore(tokenStoreCli.Raw())
	}
}

// initQueues 注册异步任务队列
// log队列：用于操作日志异步写入，使用阻塞策略保证审计完整性
func (s *gmtService) initQueues() {
	s.asyncSvc.Register(async.QueueConfig{
		Name:       "log",
		Workers:    2,
		BufferSize: 1024,
		Policy:     async.PolicyBlock,
		MaxRetries: 3,
	})
}

// initServer 初始化HTTP服务器
// 全局挂载：recover、requestid、日志、CORS、JWT鉴权中间件
func (s *gmtService) initServer() {
	serverCfg := httpx.DefaultServerConfig()
	serverCfg.Name = s.cfg.Server.Name
	serverCfg.Port = s.cfg.Server.Port
	// Vue 前端打包目录与可选后台 API 反向代理目标，由环境变量 GMT_WEB_DIST / GMT_WEB_PROXY_TARGET 配置；
	// 未配置 GMT_WEB_DIST 时保持纯 API 服务（不做静态托管）。
	serverCfg.StaticDir = s.cfg.WebDist
	serverCfg.StaticProxyTarget = s.cfg.WebProxyTarget
	// 显式声明随应用生命周期启停的服务，启动顺序即此切片顺序，关闭时逆序终止
	serverCfg.Services = []fiber.Service{s.mongoSvc, s.configMongoSvc, s.redisSvc, s.asyncSvc, s.platformSvc, s.gamelogicSvc}
	// 配置上传和管理员密码相关接口含敏感内容，统一跳过 body 日志，避免明文凭据写入日志。
	// 注意：/api/admins 同时承载 GET（列表，无敏感内容）和 POST（创建，含密码），
	// 当前中间件仅支持按路径匹配无法区分 Method，为安全起见一并跳过。
	// 列表类请求通常无 request body，响应不含密码字段，日志损失可接受。
	serverCfg.LogBodySkipPaths = []string{
		"/api/config/releases/*/upload",
		"/api/proto/upload",
		"/api/admin/login",
		"/api/admin/changePassword",
		"/api/admins",
		"/api/admins/*/password",
	}
	// CORS 来源白名单：仅允许显式配置的可信前端域名，默认不含 "*"
	serverCfg.CORSConfig = &cors.Config{
		AllowOrigins:  parseCORSOrigins(s.cfg.CORSAllowedOrigins),
		AllowMethods:  []string{fiber.MethodGet, fiber.MethodPost, fiber.MethodPut, fiber.MethodDelete, fiber.MethodOptions},
		AllowHeaders:  []string{"Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
	}

	s.server = httpx.NewServer(serverCfg)

	// 全局JWT鉴权中间件，仅跳过健康检查与登录接口；其余接口（含 GMT IAP）均需 JWT 鉴权。
	// tokenStore 在 initInfra 中已独立初始化，此处直接使用。
	s.server.App().Use(httpx.JWTMiddlewareWithTokenStore(s.jwt, s.tokenStore,
		"/health",
		"/version",
		"/api/admin/login",
	))
}

// ensureIndexes 使用临时连接创建数据库索引，并按需执行显式超管初始化。
// 索引创建或账号初始化失败仅警告，不阻止服务启动。
func (s *gmtService) ensureIndexes() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := mongox.NewClient(ctx, s.cfg.Mongo)
	if err != nil {
		logx.Warn("连接MongoDB创建索引失败", zap.Error(err))
		return
	}
	defer client.Close(ctx)

	if err := repo.EnsureIndexes(ctx, client); err != nil {
		logx.Warn("创建数据库索引失败", zap.Error(err))
		return
	}
	logx.Info("数据库索引创建完成")

	// 文章库（test）索引。与主库共用连接，仅库名不同。
	if err := repo.EnsureArticleIndexes(ctx, client); err != nil {
		logx.Warn("创建文章库索引失败", zap.Error(err))
	} else {
		logx.Info("文章库索引创建完成")
	}

	// 配置专用库（gconfig_db）索引
	configClient, err := mongox.NewClient(ctx, s.cfg.ConfigMongo)
	if err != nil {
		logx.Warn("连接配置MongoDB创建索引失败", zap.Error(err))
	} else {
		defer configClient.Close(ctx)
		if err := repo.EnsureConfigIndexes(ctx, configClient); err != nil {
			logx.Warn("创建配置数据库索引失败", zap.Error(err))
		} else {
			logx.Info("配置数据库索引创建完成")
		}
	}

	// 仅在显式提供 bootstrap 账号信息时执行初始化，避免弱默认超管口令。
	s.ensureBootstrapAdmin(ctx, client)
}

// ensureBootstrapAdmin 按环境变量显式提供的账号信息初始化超管。
// 未配置 bootstrap 信息时直接跳过，避免自动创建弱口令账号。
func (s *gmtService) ensureBootstrapAdmin(ctx context.Context, client *mongox.Client) {
	if s.cfg.BootstrapAdminUsername == "" || s.cfg.BootstrapAdminPassword == "" {
		logx.Info("未提供超管初始化参数，跳过 bootstrap admin 创建")
		return
	}
	if err := logic.ValidateAdminPassword(s.cfg.BootstrapAdminPassword); err != nil {
		logx.Warn("超管初始化密码不符合安全策略", zap.Error(err))
		return
	}

	adminRepo := repo.NewAdminRepo(client)

	if _, err := adminRepo.FindByUsername(ctx, s.cfg.BootstrapAdminUsername); err == nil {
		// 账号已存在，无需创建
		return
	} else if err != mongox.ErrNotFound {
		logx.Warn("检查 bootstrap admin 账号失败", zap.Error(err))
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(s.cfg.BootstrapAdminPassword), 12)
	if err != nil {
		logx.Warn("生成 bootstrap admin 密码失败", zap.Error(err))
		return
	}

	admin := &model.Admin{
		Username: s.cfg.BootstrapAdminUsername,
		Nickname: s.cfg.BootstrapAdminNickname,
		Password: string(hashed),
		Status:   model.AdminStatusNormal,
		IsSuper:  true,
	}
	if err := adminRepo.InsertAdmin(ctx, admin); err != nil {
		// 并发场景下唯一索引冲突视为已创建
		if mongox.IsDuplicateKeyError(err) {
			return
		}
		logx.Warn("自动创建 bootstrap admin 账号失败", zap.Error(err))
		return
	}
	logx.Info("bootstrap admin 账号已创建", zap.String("username", s.cfg.BootstrapAdminUsername))
}

// registerRoutes 注册所有路由
func (s *gmtService) registerRoutes() {
	deps := &handler.Deps{
		MongoSvc:       s.mongoSvc,
		ConfigMongoSvc: s.configMongoSvc,
		RedisSvc:       s.redisSvc,
		JWT:            s.jwt,
		TokenStore:     s.tokenStore,
		AsyncSvc:       s.asyncSvc,
		PlatformSvc:    s.platformSvc,
		GameLogicSvc:   s.gamelogicSvc,
		Config:         s.cfg,
	}
	s.server.RegisterRouters("", handler.NewRouters(deps)...)
}

// start 启动HTTP服务器（阻塞，直到信号退出）
func (s *gmtService) start() error {
	return s.server.Start()
}

// parseCORSOrigins 解析逗号分隔的 CORS 来源白名单，忽略空白项。
// 未配置时返回空切片，表示不开放任何跨域来源（不含 "*"）。
func parseCORSOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		if o := strings.TrimSpace(p); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}
