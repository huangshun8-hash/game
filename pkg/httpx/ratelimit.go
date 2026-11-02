package httpx

import (
	"time"

	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// rateLimitScript 原子完成计数与首次设置过期：INCR 后若为窗口内首个请求则设置过期时间，
// 避免先 INCR 再单独 EXPIRE 的非原子操作在 EXPIRE 失败或进程崩溃时导致 key 永不过期、客户端被永久限流。
const rateLimitScript = `
local c = redis.call('INCR', KEYS[1])
if c == 1 then
	redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return c
`

// RateLimitConfig 基于 Redis 的固定窗口限流配置。
type RateLimitConfig struct {
	// Client Redis 客户端。为 nil 时中间件退化为直接放行（不限流）。
	Client *redisx.Client
	// Max 时间窗口内允许的最大请求数。
	Max int
	// Window 限流时间窗口。
	Window time.Duration
	// KeyPrefix Redis key 前缀，用于区分不同限流场景（如 "rl:login:"）。
	KeyPrefix string
	// KeyFunc 自定义限流维度的 key（默认使用客户端 IP）。
	// 配置了 TrustedProxies 后 Fiber 的 c.IP() 会自动返回 X-Forwarded-For 中的真实 IP，
	// 因此默认 KeyFunc 无需手动解析 Header，直接取 c.IP() 即可。
	KeyFunc func(c fiber.Ctx) string
}

// RateLimitMiddleware 返回一个基于 Redis 固定窗口算法的限流中间件。
// 使用 Lua 脚本原子实现 INCR + 首次设置过期：窗口内首个请求设置过期时间，计数超过 Max 时返回 429。
//
// 容错策略：Redis 异常时采取“失败放行”（仅告警），避免缓存故障导致接口完全不可用。
// 若 cfg.Client 为 nil，则中间件直接放行（便于在未配置 Redis 的环境下平滑降级）。
func RateLimitMiddleware(cfg RateLimitConfig) fiber.Handler {
	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = func(c fiber.Ctx) string { return c.IP() }
	}
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "rl:"
	}
	// 参数校验：Max 与 Window 非法时回退到默认值，避免窗口为 0 导致 key 永不过期
	max := cfg.Max
	if max <= 0 {
		max = 100
	}
	window := cfg.Window
	if window <= 0 {
		window = time.Minute
	}
	expireSec := int64(window / time.Second)
	if expireSec < 1 {
		expireSec = 1
	}

	return func(c fiber.Ctx) error {
		// 未配置 Redis 时不限流，直接放行
		if cfg.Client == nil {
			return c.Next()
		}

		key := prefix + keyFunc(c)
		rdb := cfg.Client.Raw()
		ctx := c.Context()

		cnt, err := rdb.Eval(ctx, rateLimitScript, []string{key}, expireSec).Int64()
		if err != nil {
			// Redis 异常：失败放行，避免限流组件成为单点故障
			logx.Warn("限流计数失败，本次放行",
				zap.String("requestId", localRequestID(c)),
				zap.String("key", key),
				zap.Error(err),
			)
			return c.Next()
		}

		if cnt > int64(max) {
			return Fail(c, ErrCodeTooManyRequests, "too many requests, please try again later")
		}

		return c.Next()
	}
}
