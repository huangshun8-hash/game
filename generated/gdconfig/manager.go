// 此文件由 genconfig 自动生成，请勿手动修改。
package gdconfig

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// PostLoadHook 配置加载完成后的钩子函数类型
type PostLoadHook func(m *ConfigManager) error

// ConfigManager 配置管理器，聚合所有配置表的内存访问层。
// 每张配置表以 atomic.Pointer 存储，支持热更时无锁原子替换，读操作始终拿到完整快照。
// 仅维护正式配置（prod），热更时通过 SwapTables 将新实例的各表原子写入当前实例。
type ConfigManager struct {
	// IAPProductGlobal 配置表（原子指针，支持并发热更）
	IAPProductGlobal atomic.Pointer[IAPProductGlobalTable]
	// Rate 配置表（原子指针，支持并发热更）
	Rate atomic.Pointer[RateTable]
	// Usertag 配置表（原子指针，支持并发热更）
	Usertag atomic.Pointer[UsertagTable]

	// postLoadHooks 加载完成后依次执行的钩子列表
	postLoadHooks []PostLoadHook
}

// AddPostLoadHook 注册加载后钩子，LoadAll 完成后按注册顺序依次调用
func (m *ConfigManager) AddPostLoadHook(hook PostLoadHook) {
	m.postLoadHooks = append(m.postLoadHooks, hook)
}

// LoadAll 从 MongoDB 加载所有已注册配置表数据到内存，完成后依次执行 postLoadHooks。
// db: 配置数据所在的 MongoDB Database（通常为 gconfig_db，取决于部署架构）
func (m *ConfigManager) LoadAll(ctx context.Context, db *mongo.Database) error {
	// 遍历注册表，统一执行 Find + apply
	for _, entry := range registry {
		if err := loadEntry(ctx, db, entry, m); err != nil {
			return fmt.Errorf("加载 %s 配置失败: %w", entry.tableName, err)
		}
	}

	// 加载完成，依次执行钩子
	for _, hook := range m.postLoadHooks {
		if err := hook(m); err != nil {
			return fmt.Errorf("执行加载后钩子失败: %w", err)
		}
	}
	return nil
}

// SwapTables 将 src 中所有配置表的快照原子写入 dst。
// 用于热更场景：先将新配置加载到临时 src 实例，再逐表原子替换到正在服务的 dst 实例，
// 整个过程并发安全，持有 dst 指针的调用方无需任何感知。
func SwapTables(dst, src *ConfigManager) {
	dst.IAPProductGlobal.Store(src.IAPProductGlobal.Load())
	dst.Rate.Store(src.Rate.Load())
	dst.Usertag.Store(src.Usertag.Load())
}
