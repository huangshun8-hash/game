package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pkg/redisx"

	"github.com/bytedance/sonic"
	"github.com/redis/go-redis/v9"
)

// BaseKey 玩家基础信息的 base 存档 key，对应 cspb.Base 消息类型。
const BaseKey = "base"

// cachedSaveKeys 需要缓存的存档 key 集合，增删 key 直接修改此处。
var cachedSaveKeys = map[string]struct{}{
	BaseKey: {},
}

// saveCacheKeyPrefix 玩家存档缓存 key 前缀，完整 key 格式为 saveCache:<playerId>:<key>。
const saveCacheKeyPrefix = "saveCache:"

// saveCacheTTL 玩家存档缓存过期时间，用于控制 Redis 内存占用上限。
const saveCacheTTL = 24 * time.Hour

// negativeCacheTTL 负缓存过期时间。
const negativeCacheTTL = 5 * time.Minute

var errClientNil = errors.New("cache: redis client is nil")

// setIfNewerScript 版本守卫写入脚本，仅当传入版本严格更新时覆盖缓存。
const setIfNewerScript = `
local cur = redis.call('GET', KEYS[1])
if cur then
    local ok, decoded = pcall(cjson.decode, cur)
    if ok and decoded and decoded.version and tonumber(decoded.version) >= tonumber(ARGV[2]) then
        return 0
    end
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', tonumber(ARGV[3]))
return 1
`

// Item 玩家存档缓存条目。
type Item struct {
	Key      string `json:"key"`
	Version  int64  `json:"version"`
	Sign     string `json:"sign"`
	Content  string `json:"content"`
	NotFound bool   `json:"notFound,omitempty"`
}

// Manager 玩家存档缓存管理器，封装 Redis JSON 缓存读写。
type Manager struct {
	client *redisx.Client
}

// New 创建玩家存档缓存管理器。client 为 nil 时缓存不可用。
func New(client *redisx.Client) *Manager {
	return &Manager{client: client}
}

// IsCachedKey 判断存档 key 是否需要缓存。
func (m *Manager) IsCachedKey(key string) bool {
	_, ok := cachedSaveKeys[key]
	return ok
}

// Set 写入玩家指定 key 的存档缓存，并使用版本守卫防止旧版本覆盖新版本。
func (m *Manager) Set(ctx context.Context, playerID int64, key string, version int64, sign string, content string) error {
	if !m.IsCachedKey(key) {
		return nil
	}
	if m.client == nil {
		return errClientNil
	}
	item := Item{Key: key, Version: version, Sign: sign, Content: content}
	data, err := sonic.Marshal(item)
	if err != nil {
		return fmt.Errorf("cache: marshal key %q failed: %w", key, err)
	}
	_, err = m.client.Eval(ctx, setIfNewerScript, []string{m.buildKey(playerID, key)}, string(data), version, int64(saveCacheTTL.Seconds()))
	if err != nil {
		return fmt.Errorf("cache: setIfNewer key %q failed: %w", key, err)
	}
	return nil
}

// SetNotFound 写入玩家指定 key 的负缓存标记。
func (m *Manager) SetNotFound(ctx context.Context, playerID int64, key string) error {
	if !m.IsCachedKey(key) {
		return nil
	}
	item := Item{Key: key, NotFound: true}
	return m.setJSON(ctx, m.buildKey(playerID, key), item, negativeCacheTTL)
}

// Del 删除玩家指定 key 的存档缓存。
func (m *Manager) Del(ctx context.Context, playerID int64, key string) error {
	if !m.IsCachedKey(key) {
		return nil
	}
	if m.client == nil {
		return errClientNil
	}
	return m.client.Del(ctx, m.buildKey(playerID, key))
}

// Get 读取玩家指定 key 的存档缓存。
func (m *Manager) Get(ctx context.Context, playerID int64, key string) (*Item, bool, error) {
	if !m.IsCachedKey(key) {
		return nil, false, nil
	}
	var item Item
	if err := m.getJSON(ctx, m.buildKey(playerID, key), &item); err != nil {
		if isNotFound(err) || errors.Is(err, errClientNil) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if item.NotFound {
		return nil, false, nil
	}
	return &item, true, nil
}

// GetMany 批量读取多个玩家同一存档 key 的缓存。
func (m *Manager) GetMany(ctx context.Context, playerIDs []int64, key string) (map[int64]*Item, error) {
	if !m.IsCachedKey(key) || len(playerIDs) == 0 || m.client == nil {
		return nil, nil
	}

	keys := make([]string, 0, len(playerIDs))
	keyToPlayer := make(map[string]int64, len(playerIDs))
	seen := make(map[int64]struct{}, len(playerIDs))
	for _, id := range playerIDs {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		redisKey := m.buildKey(id, key)
		keys = append(keys, redisKey)
		keyToPlayer[redisKey] = id
	}

	values, err := m.client.MGet(ctx, keys...)
	if err != nil {
		return nil, err
	}

	items := make(map[int64]*Item, len(keys))
	for index, value := range values {
		encoded, ok := value.(string)
		if !ok {
			continue
		}
		var item Item
		if err := sonic.UnmarshalString(encoded, &item); err != nil {
			return nil, fmt.Errorf("cache: unmarshal key %q failed: %w", keys[index], err)
		}
		items[keyToPlayer[keys[index]]] = &item
	}
	return items, nil
}

// buildKey 构建玩家存档缓存的 Redis key。
func (m *Manager) buildKey(playerID int64, key string) string {
	return saveCacheKeyPrefix + strconv.FormatInt(playerID, 10) + ":" + key
}

// setJSON 将 value 序列化为 JSON 后写入 Redis。
func (m *Manager) setJSON(ctx context.Context, key string, value any, expiration time.Duration) error {
	if m.client == nil {
		return errClientNil
	}
	data, err := sonic.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache: marshal key %q failed: %w", key, err)
	}
	return m.client.Set(ctx, key, data, expiration)
}

// getJSON 读取 Redis 中的 JSON 并反序列化到 out。
func (m *Manager) getJSON(ctx context.Context, key string, out any) error {
	if m.client == nil {
		return errClientNil
	}
	value, err := m.client.Get(ctx, key)
	if err != nil {
		return err
	}
	if err := sonic.UnmarshalString(value, out); err != nil {
		return fmt.Errorf("cache: unmarshal key %q failed: %w", key, err)
	}
	return nil
}

// isNotFound 判断缓存读取错误是否表示键不存在。
func isNotFound(err error) bool {
	return errors.Is(err, redis.Nil)
}
