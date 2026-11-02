package handler

import (
	"kemiluo/generated/gdconfig"
	"kemiluo/pkg/async"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/services/platform/internal/config"
)

// Deps 是 handler 层的依赖容器
// 所有 handler 通过此结构体获取基础设施和配置，避免逐个传参
type Deps struct {
	// MongoSvc MongoDB服务实例
	MongoSvc *mongox.Service
	// RedisSvc Redis服务实例
	RedisSvc *redisx.Service
	// AsyncSvc 异步任务服务
	AsyncSvc *async.Service
	// Config 完整服务配置
	Config *config.Config
	// GamelogicSvc 用于调用 GameLogic 内部接口的 HTTP 客户端服务
	GamelogicSvc *httpcli.Service
	// GdConfig 游戏数值配置管理器（原子热更，启动时从 MongoDB 加载）
	GdConfig *gdconfig.ConfigManager
}
