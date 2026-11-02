// 此文件由 genconfig 自动生成，请勿手动修改。
// 提供统一的配置加载接口，供各服务在启动时调用。
package gdconfig

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// NewConfigManager 创建一个空的 ConfigManager 实例
func NewConfigManager() *ConfigManager {
	return &ConfigManager{}
}

// Load 从指定 MongoDB Database 加载所有已注册配置表到内存，完成后执行所有已注册的 postLoadHooks。
//
// 示例：
//
//	cfg := gdconfig.NewConfigManager()
//	cfg.AddPostLoadHook(func(m *gdconfig.ConfigManager) error {
//	    // 加载完成后的初始化逻辑
//	    return nil
//	})
//	if err := gdconfig.Load(ctx, db, cfg); err != nil {
//	    log.Fatal(err)
//	}
//	product := cfg.IapProductGlobal.Load().Get(1)
func Load(ctx context.Context, db *mongo.Database, manager *ConfigManager) error {
	return manager.LoadAll(ctx, db)
}
