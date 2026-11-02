package handler

import (
	"kemiluo/pkg/async"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gmt/internal/config"
)

// Deps 是 handler 层的依赖容器
// 所有 handler 通过此结构体获取基础设施依赖，避免逐个传参
type Deps struct {
	// MongoSvc MongoDB服务实例（默认库：gmt_db，存放管理员账号、操作日志等 GMT 自身数据）
	MongoSvc *mongox.Service
	// ConfigMongoSvc 游戏配置专用 MongoDB 服务实例（默认库：gconfig_db）
	// 存放配置表结构、发布任务、配置清单、暂存区及生产配置数据
	ConfigMongoSvc *mongox.Service
	// RedisSvc Redis服务实例（用于配置验收消息发布）
	RedisSvc *redisx.Service
	// JWT JWT签发/验证实例
	JWT *authx.JWT
	// TokenStore 管理员登录态存储，用于改密、禁用后的即时失效
	TokenStore *authx.TokenStore
	// AsyncSvc 异步任务服务（用于操作日志异步写入）
	AsyncSvc *async.Service
	// PlatformSvc 用于调用 Platform 内部接口的 HTTP 客户端服务
	PlatformSvc *httpcli.Service
	// GameLogicSvc 用于调用 GameLogic 内部 GMT 接口的 HTTP 客户端服务
	GameLogicSvc *httpcli.Service
	// Config 完整服务配置（用于获取 InternalSecret 等内部配置）
	Config *config.Config
}
