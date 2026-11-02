package handler

import (
	"kemiluo/generated/gdconfig"
	"kemiluo/pkg/async"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/wsx"
	"kemiluo/services/gamelogic/internal/config"
)

// Deps 是 handler 层的依赖容器
// 所有 handler 通过此结构体获取基础设施和配置，避免逐个传参
type Deps struct {
	// MongoSvc MongoDB服务实例
	MongoSvc *mongox.Service
	// RedisSvc Redis服务实例
	RedisSvc *redisx.Service
	// JWT JWT签发/验证实例
	JWT *authx.JWT
	// TokenStore 会话 Token Redis 存储（踢人/封禁即时生效），可能为 nil（Redis 未就绪时）
	TokenStore *authx.TokenStore
	// AsyncSvc 异步任务服务
	AsyncSvc *async.Service
	// PlatformSvc 用于调用平台服务的 HTTP 客户端服务
	PlatformSvc *httpcli.Service
	// Config 完整服务配置（用于获取 InternalSecret 等内部配置）
	Config *config.Config
	// GdConfig 游戏数值配置管理器（原子热更，启动时及收到发布消息时从 MongoDB 全量加载）
	// 业务层通过 GdConfig.IapProductGlobal.Load() 等方式直接读取配置表快照。
	GdConfig *gdconfig.ConfigManager
	// Hub WebSocket 长连接会话管理器，用于向玩家推送消息及封禁后强制断开长连接
	Hub *wsx.Hub
	// InstanceID 当前进程实例标识，用于顶号广播时跳过自身消息
	InstanceID string
}
