package main

import (
	"context"
	"time"

	"kemiluo/generated/gdconfig"
	"kemiluo/pkg/async"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/schedx"
	"kemiluo/services/platform/internal/config"
	"kemiluo/services/platform/internal/handler"
	"kemiluo/services/platform/internal/logic"
	"kemiluo/services/platform/internal/repo"
	"kemiluo/services/shared/configx"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"go.uber.org/zap"
)

// platformService 封装平台服务的所有依赖与组装逻辑
type platformService struct {
	cfg            *config.Config
	server         *httpx.Server
	mongoSvc       *mongox.Service
	configMongoSvc *mongox.Service
	redisSvc       *redisx.Service
	asyncSvc       *async.Service
	gamelogicSvc   *httpcli.Service
	schedSvc       *schedx.Scheduler
	gdConfig       *gdconfig.ConfigManager
}

// retryPendingNotifyInterval 支付成功但通知未达订单的补通知扫描周期
const retryPendingNotifyInterval = time.Minute

// mockMarkPaidInterval 模拟补单定时任务触发周期（仅 local/dev 环境）
const mockMarkPaidInterval = 5 * time.Second

// newPlatformService 创建并组装平台服务
func newPlatformService(cfg *config.Config) *platformService {
	svc := &platformService{
		cfg:      cfg,
		gdConfig: gdconfig.NewConfigManager(),
	}
	svc.initInfra()
	svc.initQueues()
	svc.initScheduler()
	svc.initServer()
	svc.ensureIndexes()
	svc.registerRoutes()
	return svc
}

// initInfra 初始化基础设施组件（MongoDB、Redis、Async、HTTP Client）
func (s *platformService) initInfra() {
	s.mongoSvc = mongox.NewService(s.cfg.Mongo)
	// 游戏配置专用库（gconfig_db），与主库共享连接参数，仅库名不同
	s.configMongoSvc = mongox.NewService(s.cfg.ConfigMongo, "mongodb-config")
	s.redisSvc = redisx.NewService(s.cfg.Redis)
	s.asyncSvc = async.NewService()
	s.gamelogicSvc = httpcli.NewService(&httpcli.Config{
		BaseURL: s.cfg.GamelogicURL,
		Timeout: 30 * time.Second,
		Secret:  s.cfg.InternalSecret,
	}, "gamelogic")
}

// initQueues 注册异步任务队列
func (s *platformService) initQueues() {
	s.asyncSvc.Register(async.QueueConfig{Name: "log", Workers: 4, BufferSize: 2048, Policy: async.PolicyDrop, MaxRetries: 0})
	s.asyncSvc.Register(async.QueueConfig{Name: "audit", Workers: 1, BufferSize: 512, Policy: async.PolicyBlock, MaxRetries: 3})
}

// initScheduler 注册周期性定时任务
// 任务在 Fiber 启动所有 Service 后才开始触发（首次执行在一个 interval 之后），
// 此时 mongoSvc.Client() 已就绪。调度器随应用生命周期优雅启停。
func (s *platformService) initScheduler() {
	s.schedSvc = schedx.NewScheduler()

	iapLogic := logic.NewIAPLogic(s.mongoSvc, s.gamelogicSvc, s.gdConfig, s.cfg.InternalSecret)

	// 支付成功但通知未达 GameLogic 的订单，周期性补通知
	s.schedSvc.AddJob("retryPendingNotifications", retryPendingNotifyInterval, iapLogic.RetryPendingNotifications)

	// 模拟补单：仅 local/dev 环境注册，每5秒将待支付订单模拟标记为已支付并回调 GameLogic
	if s.cfg.IsLocalOrDev() {
		s.schedSvc.AddJob("simulateMarkPaid", mockMarkPaidInterval, iapLogic.SimulateMarkPaid)
	}
}

// initServer 初始化HTTP服务器
func (s *platformService) initServer() {
	serverCfg := httpx.DefaultServerConfig()
	serverCfg.Name = s.cfg.Server.Name
	serverCfg.Port = s.cfg.Server.Port
	// 显式声明随应用生命周期启停的服务，启动顺序即此切片顺序，关闭时逆序终止
	serverCfg.Services = []fiber.Service{s.mongoSvc, s.configMongoSvc, s.redisSvc, s.asyncSvc, s.gamelogicSvc, s.schedSvc}
	// 内部认证接口含三方令牌，跳过 body 日志，避免泄漏敏感令牌。
	// 本地开发环境为便于调试保留完整日志，仅在非本地环境启用脱敏跳过。
	if !s.cfg.IsLocal() {
		serverCfg.LogBodySkipPaths = []string{
			"/internal/account/auth",
		}
	}
	serverCfg.CORSConfig = &cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{fiber.MethodGet, fiber.MethodPost},
		AllowHeaders:  []string{"Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
	}

	s.server = httpx.NewServer(serverCfg)
}

// ensureIndexes 创建数据库索引
// 索引创建失败仅警告，不阻止服务启动。
// 说明：Fiber v3 在 NewServer（fiber.New）阶段即已启动所有 Service，
// 因此此处可直接复用 mongoSvc.Client()，无需另建临时连接。
func (s *platformService) ensureIndexes() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := s.mongoSvc.Client()
	if client == nil {
		logx.Warn("MongoDB 服务未就绪，跳过索引创建")
		return
	}

	if err := repo.EnsureIndexes(ctx, client); err != nil {
		logx.Warn("创建数据库索引失败", zap.Error(err))
		return
	}
	logx.Info("数据库索引创建完成")
}

// loadConfigs 从 gconfig_db 加载游戏数值配置到内存。
// 加载失败仅警告，不阻止服务启动（业务层需自行处理配置为空的情况）。
// 说明：initServer 之后调用，此时 configMongoSvc.Client() 已就绪。
func (s *platformService) loadConfigs() {
	client := s.configMongoSvc.Client()
	if client == nil {
		logx.Warn("配置 MongoDB 服务未就绪，跳过游戏配置加载")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := gdconfig.Load(ctx, client.Database(), s.gdConfig); err != nil {
		logx.Warn("加载游戏数值配置失败", zap.Error(err))
		return
	}
	logx.Info("游戏数值配置加载完成", zap.Strings("tables", gdconfig.RegisteredTables()))
}

// registerRoutes 注册所有路由及中间件
func (s *platformService) registerRoutes() {
	deps := &handler.Deps{
		MongoSvc:     s.mongoSvc,
		RedisSvc:     s.redisSvc,
		AsyncSvc:     s.asyncSvc,
		Config:       s.cfg,
		GamelogicSvc: s.gamelogicSvc,
		GdConfig:     s.gdConfig,
	}
	s.server.RegisterRouters("", handler.NewRouters(deps)...)
}

// start 启动HTTP服务器（阻塞，直到信号退出）
func (s *platformService) start() error {
	// loadConfigs 在 HTTP 服务器阻塞前执行，此时 configMongoSvc.Client() 已就绪。
	// 加载失败仅警告，不阻止服务启动（业务层需自行处理配置为空的情况），与 gamelogic 语义不同：
	// platform 为辅助消费方，配置缺失仅降级，gamelogic 为主服务方，配置缺失不可启动。
	s.loadConfigs()
	// 在 HTTP 服务器阻塞前，后台启动 Redis 配置热更订阅
	go s.runConfigSubscriber()
	return s.server.Start()
}

// configReloadTimeout 单次配置热更的超时时间
const configReloadTimeout = 30 * time.Second

// runConfigSubscriber 后台订阅 Redis 配置发布频道（config:publish），处理正式配置热更。
// 收到消息后从生产库全量热更配置内存，与 gamelogic 保持一致，避免发布后需重启才能感知最新配置。
//
// 生命周期：随进程运行；Redis 断线后自动重试，每次重试间隔 5 秒，
// 避免 Redis 抖动期间无限空转消耗 CPU。
func (s *platformService) runConfigSubscriber() {
	if s.redisSvc.Client() == nil {
		logx.Warn("Redis 服务未就绪，配置热更订阅未启动")
		return
	}
	configClient := s.configMongoSvc.Client()
	if configClient == nil {
		logx.Warn("配置 MongoDB 服务未就绪，配置热更订阅未启动")
		return
	}

	const retryInterval = 5 * time.Second

	for {
		s.subscribeOnce(configClient)
		// subscribeOnce 正常退出意味着连接断开或频道关闭，等待后重试
		logx.Warn("配置热更订阅断开，等待重连", zap.Duration("retryInterval", retryInterval))
		time.Sleep(retryInterval)
	}
}

// subscribeOnce 建立一次 Pub/Sub 订阅（监听 config:publish 频道）并持续消费消息，
// 直到连接断开。返回时调用方应决定是否重连。
func (s *platformService) subscribeOnce(configClient *mongox.Client) {
	ctx := context.Background()
	pubsub := s.redisSvc.Client().Raw().Subscribe(ctx, configx.PublishChannel)
	defer pubsub.Close()

	logx.Info("配置热更订阅已建立", zap.String("channel", configx.PublishChannel))

	for msg := range pubsub.Channel() {
		if msg.Channel == configx.PublishChannel {
			s.handleConfigPublishMessage(configClient, msg.Payload)
		}
	}
}

// handleConfigPublishMessage 处理 config:publish 频道消息，原子热更配置内存。
// 收到消息后从生产库（gconfig_db）全量加载所有配置表，加载完成后逐表原子替换旧实例。
// 加载期间旧实例仍正常服务，不存在中间态。任何错误均记录日志后继续，不中断订阅循环。
func (s *platformService) handleConfigPublishMessage(configClient *mongox.Client, payload string) {
	var msg configx.PublishMessage
	if err := sonic.UnmarshalString(payload, &msg); err != nil {
		logx.Warn("解析配置发布消息失败",
			zap.String("payload", payload),
			zap.Error(err),
		)
		return
	}

	if msg.ReleaseID == "" {
		logx.Warn("配置发布消息字段不完整，跳过热更",
			zap.String("payload", payload),
		)
		return
	}

	logx.Info("收到配置发布消息，开始热更配置",
		zap.String("releaseId", msg.ReleaseID),
		zap.Strings("tables", msg.Tables),
		zap.String("operator", msg.Operator),
	)

	ctx, cancel := context.WithTimeout(context.Background(), configReloadTimeout)
	defer cancel()

	// 全量加载到新实例，再逐表原子替换到当前实例
	newManager := gdconfig.NewConfigManager()
	if err := gdconfig.Load(ctx, configClient.Database(), newManager); err != nil {
		logx.Warn("热更配置失败",
			zap.String("releaseId", msg.ReleaseID),
			zap.Strings("tables", msg.Tables),
			zap.String("operator", msg.Operator),
			zap.Error(err),
		)
		return
	}

	// 逐表原子替换，并发读取全程安全（handler 层持有的 *ConfigManager 指针不变）
	gdconfig.SwapTables(s.gdConfig, newManager)

	logx.Info("配置热更完成",
		zap.String("releaseId", msg.ReleaseID),
		zap.Strings("tables", msg.Tables),
		zap.String("operator", msg.Operator),
	)
}
