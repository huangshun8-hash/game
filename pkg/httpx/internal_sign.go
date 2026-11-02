package httpx

import (
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/signx"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// noncePrefix 内部接口防重放 nonce 在 Redis 中的 key 前缀
const noncePrefix = "nonce:internal:"

// VerifyInternalSignature 校验内部服务间调用的签名，并基于 nonce 做重放防护。
// 校验项：
//  1. 请求头 X-Signature / X-Timestamp / X-Nonce 必须存在；
//  2. HMAC-SHA256 签名有效且 timestamp 在容忍窗口内（见 signx.Verify）；
//  3. nonce 在容忍窗口内未被使用过（基于 Redis SETNX 去重）。
//
// 容错策略：当 redisCli 为 nil 或 Redis 异常时，nonce 去重"失败放行"（仅告警），
// 与限流、JWT 版本校验保持一致；签名本身仍强制校验，业务接口自身具备幂等性兜底。
//
// 参数：
//   - c:        当前请求上下文
//   - secret:   内部共享密钥
//   - redisCli: 用于 nonce 去重的 Redis 客户端，可为 nil（降级为不去重）
//
// 返回：校验失败时返回 *AppError（调用方直接 return 即可），成功返回 nil
func VerifyInternalSignature(c fiber.Ctx, secret []byte, redisCli *redisx.Client) error {
	signature := c.Get("X-Signature")
	timestamp := c.Get("X-Timestamp")
	nonce := c.Get("X-Nonce")
	if signature == "" || timestamp == "" || nonce == "" {
		return NewAppError(ErrCodeUnauthorized, "missing signature information")
	}

	body := c.Body()
	if !signx.Verify(secret, timestamp, nonce, signature, body, signx.DefaultTolerance) {
		return NewAppError(ErrCodeUnauthorized, "signature verification failed")
	}

	// nonce 重放防护：容忍窗口内同一 nonce 只允许使用一次
	if !checkNonceFresh(c, redisCli, nonce) {
		return NewAppError(ErrCodeUnauthorized, "duplicate request detected")
	}
	return nil
}

// checkNonceFresh 借助 Redis SETNX 判断 nonce 是否首次出现。
// 返回 true 表示 nonce 新鲜（首次，放行）；false 表示重放（拒绝）。
// redisCli 为 nil 或 Redis 异常时返回 true（失败放行），避免缓存故障导致内部调用全部失败。
func checkNonceFresh(c fiber.Ctx, redisCli *redisx.Client, nonce string) bool {
	if redisCli == nil {
		return true
	}
	// 过期时间取签名容忍窗口：窗口外的 nonce 即便重复，签名时效校验也已拦截
	isFresh, err := redisCli.SetNX(c.Context(), noncePrefix+nonce, 1, signx.DefaultTolerance)
	if err != nil {
		logx.Warn("nonce 去重写入失败，本次放行",
			zap.String("requestId", localRequestID(c)),
			zap.Error(err),
		)
		return true
	}
	return isFresh
}
