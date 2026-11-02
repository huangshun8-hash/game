package cache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pkg/redisx"

	"github.com/bytedance/sonic"
)

// friendCacheKeyPrefix 好友ID列表缓存 key 前缀，完整 key 格式 friendIds:<playerId>
const friendCacheKeyPrefix = "friendIds:"

// accountPlayersKeyPrefix 账号解析缓存 key 前缀，完整 key 格式 accountPlayers:<account>
const accountPlayersKeyPrefix = "accountPlayers:"

// friendIDsTTL 好友ID列表缓存过期时间。
// 好友关系变更（同意申请/删除好友）时会主动删除双方缓存，TTL 仅作为失效兜底与内存水位控制
// （写路径删除失败或跨实例竞态时，最长 TTL 后自动收敛）。
const friendIDsTTL = 10 * time.Minute

// accountPlayersTTL 账号解析缓存过期时间（解析到玩家时）。
// 账号→玩家映射随注册/注销变化，且变更无显式失效点，故 TTL 取较短值以限制陈旧窗口。
const accountPlayersTTL = 10 * time.Minute

// accountPlayersNegativeTTL 账号解析负缓存过期时间（未解析到任何玩家时）。
// 未命中的账号多为拼写错误或尚未注册，用更短 TTL 避免"新注册账号短期内查不到"。
const accountPlayersNegativeTTL = time.Minute

// FriendCache 好友关系与账号解析缓存。
// 好友排行榜为私榜但读多写少，且 friends 参数允许一次提交多个 account，
// 若不缓存则每次请求都要查询好友关系并批量回源玩家，缓存后热路径不再打 DB。
// client 为 nil 时缓存不可用：读取视为未命中、写入返回 errClientNil，调用方按降级处理。
type FriendCache struct {
	client *redisx.Client
}

// NewFriendCache 创建好友缓存。client 为 nil 时缓存不可用（读写降级）。
func NewFriendCache(client *redisx.Client) *FriendCache {
	return &FriendCache{client: client}
}

// IsAvailable 返回缓存是否可用（Redis 客户端已就绪）。
// 调用方可据此跳过回填写入，避免 Redis 未就绪时反复产生写失败告警。
func (c *FriendCache) IsAvailable() bool {
	return c != nil && c.client != nil
}

// GetFriendIDs 读取玩家好友ID列表缓存。
// 命中时 ok=true（ids 可能为空列表，表示"确认无好友"，同样视为命中，避免空好友玩家反复回源）。
// 未命中或缓存不可用（Redis 未就绪、读取/解码失败）时返回 ok=false，由调用方回源 DB。
func (c *FriendCache) GetFriendIDs(ctx context.Context, playerID int64) (ids []int64, ok bool) {
	if !c.IsAvailable() {
		return nil, false
	}
	val, err := c.client.Get(ctx, c.buildFriendIDsKey(playerID))
	if err != nil {
		return nil, false
	}
	if err := sonic.UnmarshalString(val, &ids); err != nil {
		return nil, false
	}
	if ids == nil {
		ids = []int64{}
	}
	return ids, true
}

// SetFriendIDs 写入玩家好友ID列表缓存。nil 列表按空列表写入（负缓存）。
func (c *FriendCache) SetFriendIDs(ctx context.Context, playerID int64, ids []int64) error {
	if !c.IsAvailable() {
		return errClientNil
	}
	if ids == nil {
		ids = []int64{}
	}
	data, err := sonic.Marshal(ids)
	if err != nil {
		return fmt.Errorf("cache: marshal friend ids failed: %w", err)
	}
	return c.client.Set(ctx, c.buildFriendIDsKey(playerID), data, friendIDsTTL)
}

// DelFriendIDs 删除多个玩家的好友ID列表缓存（好友关系变更时对双方调用）。
// key 不存在不视为错误；任一次删除失败立即返回，由 TTL 兜底收敛。
func (c *FriendCache) DelFriendIDs(ctx context.Context, playerIDs ...int64) error {
	if !c.IsAvailable() {
		return errClientNil
	}
	for _, playerID := range playerIDs {
		if err := c.client.Del(ctx, c.buildFriendIDsKey(playerID)); err != nil {
			return err
		}
	}
	return nil
}

// GetAccountPlayerIDs 批量读取账号解析缓存。
// 返回命中的 account → 玩家ID列表（空列表表示已确认无对应玩家，即负缓存命中），
// 以及未命中的 account 列表；账号先做去重与空串过滤，
// 因此 missing 始终为可直接回源 DB 的去重非空账号（缓存不可用时 missing 即为去重后的全部账号）。
// 调用方对 missing 账号批量回源 DB 后再调用 SetAccountPlayerIDs 回填。
func (c *FriendCache) GetAccountPlayerIDs(ctx context.Context, accounts []string) (map[string][]int64, []string) {
	hits := make(map[string][]int64)

	// 先统一去重并过滤空账号，保证缓存可用与否都向调用方交出去重后的待回源账号
	uniq := make([]string, 0, len(accounts))
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		if account == "" {
			continue
		}
		if _, ok := seen[account]; ok {
			continue
		}
		seen[account] = struct{}{}
		uniq = append(uniq, account)
	}
	if len(uniq) == 0 {
		return hits, nil
	}
	if !c.IsAvailable() {
		return hits, uniq
	}

	keys := make([]string, len(uniq))
	for i, account := range uniq {
		keys[i] = c.buildAccountKey(account)
	}
	values, err := c.client.MGet(ctx, keys...)
	if err != nil {
		return hits, uniq
	}

	missing := make([]string, 0)
	for i, value := range values {
		encoded, ok := value.(string)
		if !ok {
			missing = append(missing, uniq[i])
			continue
		}
		var ids []int64
		if err := sonic.UnmarshalString(encoded, &ids); err != nil {
			missing = append(missing, uniq[i])
			continue
		}
		if ids == nil {
			ids = []int64{}
		}
		hits[uniq[i]] = ids
	}
	return hits, missing
}

// SetAccountPlayerIDs 写入单个账号的解析结果缓存。
// playerIDs 为空时按负缓存写入（更短 TTL）；账号为空时跳过。
func (c *FriendCache) SetAccountPlayerIDs(ctx context.Context, account string, playerIDs []int64) error {
	if account == "" {
		return nil
	}
	if !c.IsAvailable() {
		return errClientNil
	}
	if playerIDs == nil {
		playerIDs = []int64{}
	}
	ttl := accountPlayersTTL
	if len(playerIDs) == 0 {
		ttl = accountPlayersNegativeTTL
	}
	data, err := sonic.Marshal(playerIDs)
	if err != nil {
		return fmt.Errorf("cache: marshal account players failed: %w", err)
	}
	return c.client.Set(ctx, c.buildAccountKey(account), data, ttl)
}

// buildFriendIDsKey 构建玩家好友ID列表缓存 key
func (c *FriendCache) buildFriendIDsKey(playerID int64) string {
	return friendCacheKeyPrefix + strconv.FormatInt(playerID, 10)
}

// buildAccountKey 构建账号解析缓存 key
func (c *FriendCache) buildAccountKey(account string) string {
	return accountPlayersKeyPrefix + account
}
