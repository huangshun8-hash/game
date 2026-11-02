package cache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pkg/redisx"

	"github.com/bytedance/sonic"
)

// payStatsKeyPrefix 玩家付费统计缓存 key 前缀，完整 key 格式 payStats:<playerID>
const payStatsKeyPrefix = "payStats:"

// payStatsTTL 玩家付费统计缓存过期时间。
// 付费统计仅在订单支付成功时变化（更新频率极低），TTL 主要作为内存水位控制
// 与并发读-写竞态下的陈旧兜底；支付成功路径会主动失效缓存，保证常规场景即时一致。
const payStatsTTL = 24 * time.Hour

// PayStatsItem 玩家付费统计缓存条目（单位分）
type PayStatsItem struct {
	LastPay  int64 `json:"lastPay"`
	Last4Pay int64 `json:"last4Pay"`
	MonthPay int64 `json:"monthPay"`
	TotalPay int64 `json:"totalPay"`
}

// PayStatsCache 玩家付费统计缓存，避免每次用户标签匹配请求都触发 MongoDB 聚合查询。
// 付费统计更新频率极低（仅订单支付成功时变化），缓存命中率极高。
// client 为 nil 时缓存不可用：读取返回未命中、写入返回错误，调用方按降级处理。
type PayStatsCache struct {
	client *redisx.Client
}

// NewPayStatsCache 创建玩家付费统计缓存
// client: Redis 客户端；为 nil 时缓存不可用（读写降级）
func NewPayStatsCache(client *redisx.Client) *PayStatsCache {
	return &PayStatsCache{client: client}
}

// Get 读取玩家付费统计缓存。
// 未命中或缓存不可用（Redis 未就绪、读取失败、解码失败）时返回 ok=false，由调用方回源 DB；
// 读取失败不返回错误，符合缓存尽力而为的语义。
func (c *PayStatsCache) Get(ctx context.Context, playerID int64) (item PayStatsItem, ok bool) {
	if c.client == nil {
		return PayStatsItem{}, false
	}
	val, err := c.client.Get(ctx, c.buildKey(playerID))
	if err != nil {
		return PayStatsItem{}, false
	}
	if err := sonic.UnmarshalString(val, &item); err != nil {
		return PayStatsItem{}, false
	}
	return item, true
}

// Set 写入玩家付费统计缓存。
// 仅用于回填 DB 查询成功的结果；写入失败返回错误，调用方按尽力而为处理。
func (c *PayStatsCache) Set(ctx context.Context, playerID int64, item PayStatsItem) error {
	if c.client == nil {
		return errClientNil
	}
	data, err := sonic.Marshal(item)
	if err != nil {
		return fmt.Errorf("cache: marshal pay stats failed: %w", err)
	}
	return c.client.Set(ctx, c.buildKey(playerID), data, payStatsTTL)
}

// Del 删除玩家付费统计缓存。
// 支付成功路径调用，保证玩家最新付费立即反映到下一次标签匹配；key 不存在不视为错误。
// 删除失败返回错误，调用方记录日志即可（TTL 兜底保证数据最终一致）。
func (c *PayStatsCache) Del(ctx context.Context, playerID int64) error {
	if c.client == nil {
		return errClientNil
	}
	return c.client.Del(ctx, c.buildKey(playerID))
}

// buildKey 构建玩家付费统计缓存的 Redis key
func (c *PayStatsCache) buildKey(playerID int64) string {
	return payStatsKeyPrefix + strconv.FormatInt(playerID, 10)
}
