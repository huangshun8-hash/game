package logic

import (
	"context"
	"errors"
	"fmt"

	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"

	"github.com/bytedance/sonic"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// customConfigRedisKey Redis 中自定义配置的固定 key（全局仅一条）
const customConfigRedisKey = "customConfig"

// customConfigCache 自定义配置 Redis 缓存结构（与 GMT 侧写入格式一致）
type customConfigCache struct {
	// Data 原始JSON数据
	Data any `json:"data"`
}

// CustomConfigLogic 自定义配置读取业务逻辑（客户端侧，仅从 Redis 读取）
type CustomConfigLogic struct {
	redisSvc *redisx.Service
}

// NewCustomConfigLogic 创建自定义配置读取逻辑实例
func NewCustomConfigLogic(redisSvc *redisx.Service) *CustomConfigLogic {
	return &CustomConfigLogic{redisSvc: redisSvc}
}

// Get 获取自定义配置，从 Redis 读取固定 key 的缓存
// 返回配置 data 字段（原样JSON），若配置不存在返回 nil, nil
func (l *CustomConfigLogic) Get(ctx context.Context, requestID string) (any, error) {
	client := l.redisSvc.Client()
	if client == nil {
		return nil, fmt.Errorf("redis client not ready")
	}

	val, err := client.Get(ctx, customConfigRedisKey)
	if err != nil {
		if isRedisNotFound(err) {
			return nil, nil
		}
		logx.Warn("自定义配置Redis读取失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("读取自定义配置缓存失败: %w", err)
	}

	var cache customConfigCache
	if err := sonic.UnmarshalString(val, &cache); err != nil {
		return nil, fmt.Errorf("反序列化自定义配置缓存失败: %w", err)
	}

	return cache.Data, nil
}

// isRedisNotFound 判断 redisx 错误是否为 key 不存在
func isRedisNotFound(err error) bool {
	return errors.Is(err, redis.Nil)
}
