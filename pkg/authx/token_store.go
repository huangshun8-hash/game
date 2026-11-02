package authx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// tokenKeyPrefix Redis 中会话 Token 的 key 前缀
	tokenKeyPrefix = "token:"
)

var (
	// ErrTokenNotFound Token 不存在或已过期（被主动撤销或 TTL 到期）
	ErrTokenNotFound = errors.New("authx: token not found")
	// ErrSessionReplaced Token 与 Redis 中存储的当前 Token 不一致（已被新登录顶替）。
	// 与 ErrTokenNotFound 区分开：调用方应返回独立的业务错误码，客户端据此提示
	// 「账号在其它设备登录」，而不是按令牌过期处理（重新静默登录会和其它端互相顶号）。
	ErrSessionReplaced = errors.New("authx: session replaced by another login")
)

// tokenCmdable TokenStore 依赖的 Redis 命令集合。
// 只声明实际用到的方法，便于单测注入假实现（无需真实 Redis）。
type tokenCmdable interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// TokenStore 基于 Redis 的会话 Token 存储，用于支持主动撤销（踢人/封禁）与顶号识别。
// 每个玩家同一时刻仅持有一个有效 Token（新登录 SET 覆写旧 Token）。
// key: token:<playerID>  value: <token 字符串>  TTL: TokenTTL
// 顶号判定无需额外保存旧 Token：key 存在且值与请求 Token 不一致，即说明该 Token 已被新登录覆盖。
type TokenStore struct {
	rdb tokenCmdable
}

// NewTokenStore 创建会话 Token Redis 存储
func NewTokenStore(rdb *redis.Client) *TokenStore {
	return &TokenStore{rdb: rdb}
}

// Save 保存（或覆盖）玩家的 Token，TTL 与令牌有效期一致
// 新登录调用此方法后，旧 Token 在下次请求时将因 Validate 失败而被拒绝
func (s *TokenStore) Save(ctx context.Context, playerID string, token string, ttl time.Duration) error {
	key := tokenKeyPrefix + playerID
	if err := s.rdb.Set(ctx, key, token, ttl).Err(); err != nil {
		return fmt.Errorf("authx: save token failed: %w", err)
	}
	return nil
}

// Validate 校验 token 是否与 Redis 中存储的当前 Token 一致。
// 返回 ErrTokenNotFound 表示 key 不存在（已被撤销或 TTL 到期）
// 返回 ErrSessionReplaced 表示 key 存在但值不一致（已被新登录顶替）
func (s *TokenStore) Validate(ctx context.Context, playerID string, token string) error {
	key := tokenKeyPrefix + playerID
	stored, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrTokenNotFound
		}
		return fmt.Errorf("authx: get token failed: %w", err)
	}
	if stored != token {
		return ErrSessionReplaced
	}
	return nil
}

// Revoke 立即撤销玩家的 Token（删除 key），用于踢人下线或封禁后即时失效
func (s *TokenStore) Revoke(ctx context.Context, playerID string) error {
	key := tokenKeyPrefix + playerID
	if err := s.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("authx: revoke token failed: %w", err)
	}
	return nil
}
