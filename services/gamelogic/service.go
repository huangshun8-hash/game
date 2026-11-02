package main

import (
	"context"
	"fmt"
	"time"

	"kemiluo/generated/gdconfig"
	"kemiluo/pb/cspb"
	"kemiluo/pkg/async"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/schedx"
	"kemiluo/pkg/wsx"
	"kemiluo/services/gamelogic/internal/config"
	"kemiluo/services/gamelogic/internal/handler"
	"kemiluo/services/gamelogic/internal/logic"
	"kemiluo/services/gamelogic/internal/repo"
	"kemiluo/services/shared/authdto"
	"kemiluo/services/shared/configx"
	"kemiluo/services/shared/iapdto"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// 登录接口限流参数（IP 维度，固定窗口）
const (
	// loginRateLimitMax 每个窗口内允许的最大登录请求数
	loginRateLimitMax = 10
	// loginRateLimitWindow 登录限流时间窗口
	loginRateLimitWindow = time.Minute
)

// 内购订单接口限流参数（玩家维度，固定窗口）
const (
	// iapRateLimitMax 内购通用接口每窗口最大请求数（领奖/查询/收据等）
	iapRateLimitMax = 100
	// iapRateLimitWindow 内购通用接口限流时间窗口
	iapRateLimitWindow = time.Minute
	// createOrderRateLimitMax 创建订单接口每窗口最大请求数（每位玩家五秒内仅允许一次）
	createOrderRateLimitMax = 1
	// createOrderRateLimitWindow 创建订单接口限流时间窗口
	createOrderRateLimitWindow = 5 * time.Second
)

// closeExpiredOrdersInterval 过期未支付订单的扫描关闭周期
const closeExpiredOrdersInterval = time.Minute

// cleanupExpiredMailsInterval 过期邮件的清理周期
const cleanupExpiredMailsInterval = time.Hour

// gamelogicService 封装逻辑服的所有依赖与组装逻辑
type gamelogicService struct {
	cfg            *config.Config
	server         *httpx.Server
	mongoSvc       *mongox.Service
	configMongoSvc *mongox.Service
	redisSvc       *redisx.Service
	asyncSvc       *async.Service
	jwt            *authx.JWT
	tokenStore     *authx.TokenStore
	platformSvc    *httpcli.Service
	schedSvc       *schedx.Scheduler
	gdConfig       *gdconfig.ConfigManager
	hub            *wsx.Hub
	// instanceID 当前进程实例标识（启动时随机生成），用于顶号广播时跳过自身消息
	instanceID string
}

// newGamelogicService 创建并组装逻辑服
func newGamelogicService(cfg *config.Config) *gamelogicService {
	svc := &gamelogicService{
		cfg:        cfg,
		gdConfig:   gdconfig.NewConfigManager(),
		instanceID: uuid.NewString(),
	}
	svc.initInfra()
	svc.initQueues()
	svc.initScheduler()
	svc.initServer()
	svc.ensureIndexes()
	svc.registerRoutes()
	return svc
}

// initInfra 初始化基础设施组件（MongoDB、Redis、Async、JWT、HTTP Client）
func (s *gamelogicService) initInfra() {
	s.mongoSvc = mongox.NewService(s.cfg.Mongo)
	// 游戏配置专用库（gconfig_db），与主库共享连接参数，仅库名不同
	s.configMongoSvc = mongox.NewService(s.cfg.ConfigMongo, "mongodb-config")
	s.redisSvc = redisx.NewService(s.cfg.Redis)
	s.asyncSvc = async.NewService()
	s.jwt = authx.NewJWT(s.cfg.JWT)
	s.hub = wsx.NewHub()
	s.platformSvc = httpcli.NewService(&httpcli.Config{
		BaseURL: s.cfg.PlatformURL,
		Timeout: 30 * time.Second,
		Secret:  s.cfg.InternalSecret,
	}, "platform")
}

// initQueues 注册异步任务队列
func (s *gamelogicService) initQueues() {
	s.asyncSvc.Register(async.QueueConfig{Name: "log", Workers: 4, BufferSize: 2048, Policy: async.PolicyDrop, MaxRetries: 0})
	s.asyncSvc.Register(async.QueueConfig{Name: "stat", Workers: 2, BufferSize: 1024, Policy: async.PolicyDropSilent, MaxRetries: 0})
}

// initScheduler 注册周期性定时任务
// 任务在 Fiber 启动所有 Service 后才开始触发（首次执行在一个 interval 之后），
// 此时 mongoSvc.Client() 已就绪。调度器随应用生命周期优雅启停。
func (s *gamelogicService) initScheduler() {
	s.schedSvc = schedx.NewScheduler()

	// 内购业务逻辑实例（仅持有 mongoSvc/platformSvc 引用，构造开销极小）。
	// 定时任务实例不注入付费统计缓存与 Redis 发布客户端（仅执行过期订单关闭，
	// 不涉及付费统计失效与 WS 推送；且 initScheduler 时 Redis 尚未启动，Client() 为 nil）
	iapLogic := logic.NewIAPLogic(s.mongoSvc, s.platformSvc, nil, nil)
	// 邮件业务逻辑实例（仅持有 mongoSvc 引用，构造开销极小）
	mailLogic := logic.NewMailLogic(s.mongoSvc)

	// 关闭超时未支付订单（WAIT_PAY → CLOSED）
	s.schedSvc.AddJob("closeExpiredOrders", closeExpiredOrdersInterval, iapLogic.CloseExpiredOrders)
	// 清理过期超过宽限期（7天）的邮件
	s.schedSvc.AddJob("cleanupExpiredMails", cleanupExpiredMailsInterval, mailLogic.CleanupExpiredMails)
}

// initServer 初始化HTTP服务器
func (s *gamelogicService) initServer() {
	serverCfg := httpx.DefaultServerConfig()
	serverCfg.Name = s.cfg.Server.Name
	serverCfg.Port = s.cfg.Server.Port
	serverCfg.TrustedProxies = s.cfg.TrustedProxies
	// 显式声明随应用生命周期启停的服务，启动顺序即此切片顺序，关闭时逆序终止
	serverCfg.Services = []fiber.Service{s.mongoSvc, s.configMongoSvc, s.redisSvc, s.asyncSvc, s.platformSvc, s.schedSvc, s.hub}
	// 登录/存档/收据接口含敏感报文，跳过 body 日志。
	// 本地开发环境为便于调试保留完整日志，仅在非本地环境启用脱敏跳过。
	if !s.cfg.IsLocal() {
		serverCfg.LogBodySkipPaths = []string{
			"/auth/login",
			"/api/save/upload", "/api/save/download",
			"/api/iap/receipt",
			"/internal/iap/notify",
		}
	}
	serverCfg.CORSConfig = &cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{fiber.MethodGet, fiber.MethodPost},
		AllowHeaders:  []string{"Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
	}

	s.server = httpx.NewServer(serverCfg)

	// 此时 Fiber 已在 NewServer 内部启动所有 Service，redisSvc.Client() 可安全访问。
	// 构建会话 Token 存储，供中间件与认证逻辑共享，实现踢人/封禁即时生效。
	if s.redisSvc.Client() != nil {
		s.tokenStore = authx.NewTokenStore(s.redisSvc.Client().Raw())
	}

	// 全局JWT鉴权中间件，跳过登录、健康检查、内部回调接口以及 WebSocket 升级路径。
	// 内部回调（支付通知、GMT封禁/邮件管理）仅靠签名+nonce 校验，不挂 JWT；
	// WebSocket 升级请求由 wsx 的两段式握手鉴权把关（升级前验签名、升级后验会话），
	// 浏览器无法携带 Authorization 头，令牌只能走 query 参数 token。
	// 升级路径取自配置（cfg.WSPath，默认 /ws），必须与 wsx.Register 使用的路径保持一致。
	skipJWT := []string{
		"/health", "/version", "/auth/login", "/api/serverTime", "/api/customConfig",
		"/gmt/iap/orders", "/gmt/iap/payStats",
		"/internal/iap/notify",
		"/internal/role/queryByPassport", "/internal/role/queryByRoleId", "/internal/role/serverList",
		"/gmt/ban/batchBan", "/gmt/ban/batchUnban", "/gmt/ban/records",
		"/gmt/save/download", "/gmt/save/upload",
		"/gmt/mail/send", "/gmt/mail/list", "/gmt/mail/recall",
	}
	skipJWT = append(skipJWT, s.cfg.WSPath)
	s.server.App().Use(httpx.JWTMiddlewareWithTokenStore(s.jwt, s.tokenStore, skipJWT...))

	// 登录接口按 IP 维度限流，防止游客登录被刷量（默认 10 次/分钟）
	s.server.App().Use("/auth/login", httpx.RateLimitMiddleware(httpx.RateLimitConfig{
		Client:    s.redisSvc.Client(),
		Max:       loginRateLimitMax,
		Window:    loginRateLimitWindow,
		KeyPrefix: "rl:login:",
	}))

	// 内购订单接口按玩家维度限流（JWT 中间件已写入 userId）：
	//   - 创建订单：更严格，抑制刷单与同秒订单号碰撞放大
	//   - 其余内购接口（收据/领奖/查询）：统一通用限流
	// 玩家维度优先取已认证 userId，缺失时回退到 IP，避免未鉴权请求绕过限流。
	playerRateKey := func(c fiber.Ctx) string {
		if uid, ok := c.Locals("userId").(string); ok && uid != "" {
			return uid
		}
		return c.IP()
	}
	// 创建订单严格限流：仅作用于精确路径 POST /api/iap/order，
	// 避免前缀匹配误伤 /api/iap/order/status、/api/iap/orders/paid 等查询接口（弱联网下需频繁轮询）
	createOrderLimiter := httpx.RateLimitMiddleware(httpx.RateLimitConfig{
		Client:    s.redisSvc.Client(),
		Max:       createOrderRateLimitMax,
		Window:    createOrderRateLimitWindow,
		KeyPrefix: "rl:iap:order:",
		KeyFunc:   playerRateKey,
	})
	s.server.App().Use("/api/iap/order", func(c fiber.Ctx) error {
		if c.Method() == fiber.MethodPost && c.Path() == "/api/iap/order" {
			return createOrderLimiter(c)
		}
		return c.Next()
	})
	// 其余内购接口（收据/领奖/查询）统一通用限流
	s.server.App().Use("/api/iap", httpx.RateLimitMiddleware(httpx.RateLimitConfig{
		Client:    s.redisSvc.Client(),
		Max:       iapRateLimitMax,
		Window:    iapRateLimitWindow,
		KeyPrefix: "rl:iap:",
		KeyFunc:   playerRateKey,
	}))
}

// ensureIndexes 创建数据库索引
// 索引创建失败仅警告，不阻止服务启动。
// 说明：Fiber v3 在 NewServer（fiber.New）阶段即已启动所有 Service，
// 因此此处可直接复用 mongoSvc.Client()，无需另建临时连接。
func (s *gamelogicService) ensureIndexes() {
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
// 配置加载失败时返回错误，阻止服务启动。
func (s *gamelogicService) loadConfigs() error {
	client := s.configMongoSvc.Client()
	if client == nil {
		return fmt.Errorf("配置 MongoDB 服务未就绪")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := gdconfig.Load(ctx, client.Database(), s.gdConfig); err != nil {
		return fmt.Errorf("加载游戏数值配置失败: %w", err)
	}
	logx.Info("游戏数值配置加载完成", zap.Strings("tables", gdconfig.RegisteredTables()))
	return nil
}

// registerRoutes 注册所有路由及中间件
func (s *gamelogicService) registerRoutes() {
	deps := &handler.Deps{
		MongoSvc:    s.mongoSvc,
		RedisSvc:    s.redisSvc,
		JWT:         s.jwt,
		TokenStore:  s.tokenStore,
		AsyncSvc:    s.asyncSvc,
		PlatformSvc: s.platformSvc,
		Config:      s.cfg,
		GdConfig:    s.gdConfig,
		Hub:         s.hub,
		InstanceID:  s.instanceID,
	}
	s.server.RegisterRouters("", handler.NewRouters(deps)...)

	// WebSocket 升级端点：query 参数 token 在握手时校验 JWT 签名，并比对 Redis Token 存储，
	// 使封禁/踢人对新握手同样即时生效（否则客户端自动重连可立刻恢复被封禁玩家的连接）；
	// 升级成功后由 Hub 统一管理会话。连接参数取自配置（websocket 段），可不改代码调整。
	// 复用 pkg/wsx（公共长连接网关封装）；业务消息回调由 handleWSMessage 记录，后续可按需分发。
	wsCfg := s.cfg.WS
	if wsCfg.HasOriginWildcard() && !s.cfg.IsLocal() {
		logx.Warn("WebSocket Origin 白名单为通配，跨站 WebSocket 劫持（CSWSH）防御未启用",
			zap.String("configKey", "websocket.origins"),
			zap.Strings("origins", wsCfg.Origins),
		)
	}
	s.hub.SetMessageHandler(s.handleWSMessage)
	wsx.Register(s.server.App(), s.cfg.WSPath, s.hub, wsCfg, wsx.NewJWTAuthWithStore(s.jwt, s.tokenStore))
}

// handleWSMessage 处理 WebSocket 客户端上行业务帧。
// 参数：
//   - playerID: 会话所属玩家ID
//   - msgType:  帧类型（cspb.WsMsgType，当前恒为 business）
//   - bizID:    业务消息ID（帧的 bizId 字段），业务侧的路由与幂等依据
//   - data:     帧负载（原始字节，按 bizID 对应协议解析）
//
// 当前版本尚未接入任何业务分发，仅以 Debug 级别记录消息用于联调
// （上行属高频事件，Info 级别会随客户端发送频率线性放大日志写盘量）。
// 注意：生产环境日志级别为 info 时本条日志不输出，即上行业务帧目前确实会被丢弃；
// 这是「协议通道已就绪、业务分发待接入」的过渡状态，接入具体业务后应按 bizID 路由。
func (s *gamelogicService) handleWSMessage(playerID int64, msgType cspb.WsMsgType, bizID string, data []byte) {
	logx.Debug("收到 WebSocket 业务消息",
		zap.Int64("userId", playerID),
		zap.String("msgType", msgType.String()),
		zap.String("bizId", bizID),
		zap.Int("msgSize", len(data)),
	)
}

// start 启动HTTP服务器（阻塞，直到信号退出）
func (s *gamelogicService) start() error {
	if err := s.loadConfigs(); err != nil {
		return fmt.Errorf("加载游戏配置失败: %w", err)
	}
	// 在 HTTP 服务器阻塞前，后台启动 Redis 配置热更订阅
	go s.runConfigSubscriber()
	// 后台启动支付成功跨服广播订阅，实现玩家 WS 连接所在实例与支付回调处理实例解耦
	go s.runPaySuccessSubscriber()
	// 后台启动顶号跨服广播订阅：旧连接可能位于其它实例，需由持有连接的实例下发通知并断开
	go s.runSessionKickedSubscriber()
	return s.server.Start()
}

// configReloadTimeout 单次配置热更的超时时间
const configReloadTimeout = 30 * time.Second

// runConfigSubscriber 后台订阅 Redis 配置发布频道，处理正式配置热更。
//
//   - config:publish：收到后从生产库全量热更配置内存（所有玩家即时生效）
//
// 生命周期：随进程运行；Redis 断线后自动重试，每次重试间隔 5 秒，
// 避免 Redis 抖动期间无限空转消耗 CPU。
func (s *gamelogicService) runConfigSubscriber() {
	redisClient := s.redisSvc.Client()
	if redisClient == nil {
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
func (s *gamelogicService) subscribeOnce(configClient *mongox.Client) {
	ctx := context.Background()
	pubsub := s.redisSvc.Client().Raw().Subscribe(ctx, configx.PublishChannel)
	defer pubsub.Close()

	logx.Info("配置热更订阅已建立",
		zap.String("channel", configx.PublishChannel),
	)

	for msg := range pubsub.Channel() {
		if msg.Channel == configx.PublishChannel {
			s.handleConfigPublishMessage(configClient, msg.Payload)
		}
	}
}

// handleConfigPublishMessage 处理 config:publish 频道消息，原子热更配置内存。
// 收到消息后从生产库（gconfig_db）全量加载所有配置表，加载完成后原子替换旧实例。
// 加载期间旧实例仍正常服务，不存在中间态。
// 任何错误均记录日志后继续，不中断订阅循环。
func (s *gamelogicService) handleConfigPublishMessage(configClient *mongox.Client, payload string) {
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
	// 直接赋值整个结构体不安全（含 atomic/sync 字段），改为逐字段原子 Store
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

// runPaySuccessSubscriber 后台订阅 Redis 支付成功跨服广播频道（iap:paySuccess）。
//
// 多服务器部署下，玩家 WS 连接所在的 GameLogic 实例与处理支付回调的实例可能不同
// （每个实例的 wsx.Hub 仅管理本进程内的连接），故支付成功实例把推送消息发布到共享频道，
// 本实例订阅后按 PlayerID 路由，目标玩家连接在本实例上才下发 WS 推送。
//
// 生命周期：随进程运行；Redis 断线后自动重试，每次重试间隔 5 秒，
// 避免 Redis 抖动期间无限空转消耗 CPU（与配置热更订阅保持一致）。
func (s *gamelogicService) runPaySuccessSubscriber() {
	redisClient := s.redisSvc.Client()
	if redisClient == nil {
		logx.Warn("Redis 服务未就绪，支付成功WS广播订阅未启动")
		return
	}

	const retryInterval = 5 * time.Second

	for {
		s.subscribePaySuccessOnce()
		// subscribePaySuccessOnce 正常退出意味着连接断开或频道关闭，等待后重试
		logx.Warn("支付成功广播订阅断开，等待重连", zap.Duration("retryInterval", retryInterval))
		time.Sleep(retryInterval)
	}
}

// subscribePaySuccessOnce 建立一次 Pub/Sub 订阅（监听 iap:paySuccess 频道）并持续消费消息，
// 直到连接断开。返回时调用方应决定是否重连。
func (s *gamelogicService) subscribePaySuccessOnce() {
	ctx := context.Background()
	pubsub := s.redisSvc.Client().Raw().Subscribe(ctx, iapdto.PaySuccessPushChannel)
	defer pubsub.Close()

	logx.Info("支付成功广播订阅已建立",
		zap.String("channel", iapdto.PaySuccessPushChannel),
	)

	for msg := range pubsub.Channel() {
		if msg.Channel == iapdto.PaySuccessPushChannel {
			s.handlePaySuccessPushMessage(msg.Payload)
		}
	}
}

// handlePaySuccessPushMessage 处理 iap:paySuccess 频道消息，向本实例 Hub 上的目标玩家下发 WS 推送。
// 订阅消息为 JSON 内部契约（PaySuccessPushMessage），下发给客户端的负载转换为
// 客户端契约 cspb.PaySuccessPush（proto）后推送，wsx 按 proto.Message 走 protobuf 编码。
// PushResult.Online 表示目标玩家在本实例上的在线连接数：绝大多数实例为 0（玩家不在该实例），
// 仅玩家连接所在实例真正完成投递，实现多服务器下的定向通知。
// 解析或推送失败仅记录日志后继续，不中断订阅循环。
func (s *gamelogicService) handlePaySuccessPushMessage(payload string) {
	var msg iapdto.PaySuccessPushMessage
	if err := sonic.UnmarshalString(payload, &msg); err != nil {
		logx.Warn("解析支付成功广播消息失败",
			zap.String("payload", payload),
			zap.Error(err),
		)
		return
	}
	if msg.PlayerID <= 0 || msg.Data.OrderID == "" {
		logx.Warn("支付成功广播消息字段不完整，跳过推送",
			zap.String("payload", payload),
		)
		return
	}

	// bizId 复用订单号，客户端据此幂等去重；Data 为 proto 消息（cspb.PaySuccessPush）
	result := s.hub.PushToPlayer(msg.PlayerID, &wsx.PushPayload{
		BizID: msg.Data.OrderID,
		Data: &cspb.PaySuccessPush{
			OrderId:  msg.Data.OrderID,
			PayId:    msg.Data.ProductID,
			Channel:  msg.Data.Channel,
			AwardTag: msg.Data.AwardTag,
			Award:    msg.Data.Award,
			PaidAt:   msg.Data.PaidAt,
		},
	})
	if result.Online > 0 {
		logx.Info("支付成功WS推送已下发给本地玩家",
			zap.Int64("userId", msg.PlayerID),
			zap.String("orderId", msg.Data.OrderID),
			zap.Int("delivered", result.Delivered),
			zap.Int("online", result.Online),
		)
	} else {
		// 玩家不在本实例上为预期常态（连接在其他实例），仅 Debug 级记录避免日志噪声
		logx.Debug("支付成功广播消息目标玩家不在本实例，跳过推送",
			zap.Int64("userId", msg.PlayerID),
			zap.String("orderId", msg.Data.OrderID),
		)
	}
}

// runSessionKickedSubscriber 后台订阅 Redis 顶号广播频道（auth:sessionKicked）。
//
// 严格单端策略下，同一账号在其它设备登录时旧端需要「先收到通知、再被断开」。
// 每个实例的 wsx.Hub 只管理本进程内的连接，登录请求所在实例可能并不持有旧连接，
// 故登录实例把事件发布到共享频道，各实例订阅后按 PlayerID 路由，
// 只有旧连接在本实例上时才真正下发通知并断开。
//
// 生命周期与其它订阅一致：随进程运行，Redis 断线后自动重试，间隔 5 秒。
func (s *gamelogicService) runSessionKickedSubscriber() {
	redisClient := s.redisSvc.Client()
	if redisClient == nil {
		logx.Warn("Redis 服务未就绪，顶号广播订阅未启动")
		return
	}

	const retryInterval = 5 * time.Second

	for {
		s.subscribeSessionKickedOnce()
		// subscribeSessionKickedOnce 正常退出意味着连接断开或频道关闭，等待后重试
		logx.Warn("顶号广播订阅断开，等待重连", zap.Duration("retryInterval", retryInterval))
		time.Sleep(retryInterval)
	}
}

// subscribeSessionKickedOnce 建立一次订阅（监听 auth:sessionKicked 频道）并持续消费消息，
// 直到连接断开。返回时调用方应决定是否重连。
func (s *gamelogicService) subscribeSessionKickedOnce() {
	ctx := context.Background()
	pubsub := s.redisSvc.Client().Raw().Subscribe(ctx, authdto.SessionKickedChannel)
	defer pubsub.Close()

	logx.Info("顶号广播订阅已建立",
		zap.String("channel", authdto.SessionKickedChannel),
	)

	for msg := range pubsub.Channel() {
		if msg.Channel == authdto.SessionKickedChannel {
			s.handleSessionKickedMessage(msg.Payload)
		}
	}
}

// handleSessionKickedMessage 处理 auth:sessionKicked 频道消息：
// 若被顶号玩家的旧连接在本实例上，先下发 cspb.KickedOfflinePush 通知帧，再以 4002 断开。
// 目标玩家不在本实例属预期常态（绝大多数实例都不持有该连接），仅 Debug 记录避免日志噪声。
// 解析或推送失败仅记录日志后继续，不中断订阅循环。
func (s *gamelogicService) handleSessionKickedMessage(payload string) {
	var msg authdto.SessionKickedMessage
	if err := sonic.UnmarshalString(payload, &msg); err != nil {
		logx.Warn("解析顶号广播消息失败",
			zap.String("payload", payload),
			zap.Error(err),
		)
		return
	}
	if msg.PlayerID <= 0 {
		logx.Warn("顶号广播消息字段不完整，跳过处理",
			zap.String("payload", payload),
		)
		return
	}
	// 跳过自身发出的消息：本地已在登录链路同步完成通知与断开，无需重复处理；
	// 同时避免把「发布后新建立的连接」当作旧端误踢。
	if msg.OriginInstance != "" && msg.OriginInstance == s.instanceID {
		logx.Debug("顶号广播来自本实例，跳过重复处理",
			zap.String("requestId", msg.RequestID),
			zap.Int64("userId", msg.PlayerID),
		)
		return
	}

	closed := s.hub.KickPlayerWithNoticeExcept(msg.PlayerID, &wsx.PushPayload{
		// bizId 复用登录请求ID，客户端可据此去重/追溯
		BizID: msg.RequestID,
		Data: &cspb.KickedOfflinePush{
			LoginAt:    msg.LoginAt,
			Platform:   msg.Platform,
			DeviceName: msg.DeviceName,
			Ip:         msg.IP,
			DeviceId:   msg.DeviceID,
		},
	}, msg.TokenDigest)
	if closed > 0 {
		logx.Info("顶号广播已在本实例通知并断开旧端连接",
			zap.String("requestId", msg.RequestID),
			zap.Int64("userId", msg.PlayerID),
			zap.Int("closedConnections", closed),
		)
	}
}
