package authx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// fakeTokenCmdable 内存版 Redis 命令实现，仅覆盖 TokenStore 用到的方法。
// 用于在无真实 Redis 依赖的情况下覆盖 Validate 的分支判定。
type fakeTokenCmdable struct {
	// value 当前存储的 Token 值
	value string
	// missing 是否模拟 key 不存在（redis.Nil）
	missing bool
	// err 非空时模拟 Redis 查询异常
	err error
}

// Get 模拟 GET 命令
func (f *fakeTokenCmdable) Get(ctx context.Context, key string) *redis.StringCmd {
	if f.err != nil {
		return redis.NewStringResult("", f.err)
	}
	if f.missing {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(f.value, nil)
}

// Set 模拟 SET 命令
func (f *fakeTokenCmdable) Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	f.value, _ = value.(string)
	f.missing = false
	return redis.NewStatusResult("OK", nil)
}

// Del 模拟 DEL 命令
func (f *fakeTokenCmdable) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	f.value = ""
	f.missing = true
	return redis.NewIntResult(1, nil)
}

// TestTokenStoreValidate 校验顶号识别依赖的核心分支：
// 值一致通过、值不一致判定为被顶替、key 不存在判定为过期/撤销。
func TestTokenStoreValidate(t *testing.T) {
	ctx := context.Background()
	store := &TokenStore{rdb: &fakeTokenCmdable{}}

	if err := store.Save(ctx, "1001", "token-a", time.Minute); err != nil {
		t.Fatalf("保存 Token 失败: %v", err)
	}
	if err := store.Validate(ctx, "1001", "token-a"); err != nil {
		t.Fatalf("同一 Token 应通过校验，实际错误为 %v", err)
	}

	// 新登录覆盖后，旧 Token 必须判定为「被顶替」而不是「过期/撤销」
	if err := store.Save(ctx, "1001", "token-b", time.Minute); err != nil {
		t.Fatalf("覆盖 Token 失败: %v", err)
	}
	if err := store.Validate(ctx, "1001", "token-a"); !errors.Is(err, ErrSessionReplaced) {
		t.Fatalf("旧 Token 应返回 ErrSessionReplaced，实际为 %v", err)
	}
	if err := store.Validate(ctx, "1001", "token-b"); err != nil {
		t.Fatalf("新 Token 应通过校验，实际错误为 %v", err)
	}

	// 撤销（封禁/注销）后 key 不存在，应判定为过期/撤销而不是顶号
	if err := store.Revoke(ctx, "1001"); err != nil {
		t.Fatalf("撤销 Token 失败: %v", err)
	}
	if err := store.Validate(ctx, "1001", "token-b"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("撤销后应返回 ErrTokenNotFound，实际为 %v", err)
	}
}

// TestTokenStoreValidateRedisError 校验 Redis 异常不被误判为业务性令牌失败：
// 中间件依赖该区分执行「失败放行」，若误判为 ErrSessionReplaced/ErrTokenNotFound 会拒绝全量用户。
func TestTokenStoreValidateRedisError(t *testing.T) {
	redisErr := errors.New("redis unavailable")
	store := &TokenStore{rdb: &fakeTokenCmdable{err: redisErr}}

	err := store.Validate(context.Background(), "1001", "token-a")
	if err == nil {
		t.Fatal("Redis 异常时 Validate 应返回错误")
	}
	if errors.Is(err, ErrSessionReplaced) || errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("Redis 异常不应归类为业务性令牌失败，实际为 %v", err)
	}
	if !errors.Is(err, redisErr) {
		t.Fatalf("应保留原始 Redis 错误以便上层识别，实际为 %v", err)
	}
}
