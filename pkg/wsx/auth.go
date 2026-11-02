package wsx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// TokenDigest 返回握手令牌的 SHA-256 摘要（十六进制），用于在不保存明文 Token 的前提下
// 区分不同登录会话。空令牌返回空串。
func TokenDigest(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TokenValidator 校验 WebSocket 握手令牌的签名与有效期（在协议升级**之前**执行）。
// 这一阶段必须廉价：它是对公网的第一道闸门，任何伪造/篡改令牌都应在这里被挡掉，
// 不进入 WebSocket 升级流程（见 httpx.Fail 返回的 JSON 错误信封）。
//
// 参数：
//   - c:     Fiber 请求上下文（可读取 requestId 等本地字段用于日志关联）
//   - token: 登录令牌（来自升级请求的 query 参数 token）
//
// 返回：userId 玩家ID字符串；err 校验失败原因（调用方应将其映射为未认证响应）
type TokenValidator func(c fiber.Ctx, token string) (userId string, err error)

// SessionValidator 校验握手令牌对应的登录会话是否仍然有效（在协议升级**之后**、创建会话之前执行）。
// 与 TokenValidator 拆成两段的原因：浏览器 WebSocket API 无法读取升级失败（非 101）的响应体，
// 「被顶号」「被撤销」这类必须展示给用户的结论只能通过升级后的错误帧 + Close 帧送达。
//
// 参数：
//   - ctx:       下游调用上下文（Redis 查询等）
//   - requestID: 握手请求的追踪ID，用于日志关联
//   - userId:    升级前签名校验得到的玩家ID
//   - token:     握手令牌原文
//
// 返回：ErrSessionReplaced（被新登录顶替）/ ErrTokenRevoked（已撤销或过期）表示会话失效；
// nil 表示通过或存储异常放行
type SessionValidator func(ctx context.Context, requestID string, userId string, token string) error

// HandshakeAuth WebSocket 握手鉴权的两段式配置。
//   - Validate：升级前签名校验，负责挡住伪造令牌，避免无效请求拿到 101 消耗连接资源；
//   - CheckSession：升级后会话校验，负责把「被顶号/被撤销」以客户端可读的方式告知。
//
// 两者都可以为 nil（表示跳过该阶段），但 CheckSession 依赖 Validate 产出的 userId，
// 单独配置 CheckSession 而不配置 Validate 时所有连接都会以「身份非法」被拒。
type HandshakeAuth struct {
	// Validate 升级前的签名校验器；nil 表示跳过（此时任何请求都能升级，调用方须自行限流）
	Validate TokenValidator
	// CheckSession 升级后的会话校验器；nil 表示只验签名
	CheckSession SessionValidator
}

// ErrTokenRevoked 握手令牌已失效：已被撤销（封禁/注销后 Token 从存储中删除）或已过期。
// 与"签名无效"区分开，便于调用方按需给出不同的客户端提示与日志级别。
var ErrTokenRevoked = errors.New("wsx: handshake token revoked")

// ErrSessionReplaced 握手令牌已被新登录顶替（同一账号在其它设备登录）。
// 客户端收到后应停止重连并提示「账号已在其它设备登录」，而不是按令牌过期静默重连。
var ErrSessionReplaced = errors.New("wsx: handshake session replaced")

// NewJWTAuth 基于 JWT 签名构建握手鉴权配置（不做 Token 存储二次校验）。
// 仅适用于未启用 Redis Token 存储的场景：此时撤销 Token 无法拦截新的握手，
// 被封禁玩家断线重连仍可建连，因此**生产环境应使用 NewJWTAuthWithStore**。
//
// 参数：
//   - j: JWT 签名校验器
//
// 返回：握手鉴权配置（仅 Validate 阶段）
func NewJWTAuth(j *authx.JWT) *HandshakeAuth {
	return NewJWTAuthWithStore(j, nil)
}

// NewJWTAuthWithStore 构建两段式握手鉴权：升级前只验 JWT 签名，升级后再比对 Redis 中存储的 Token，
// 使踢人/封禁对 WebSocket 握手同样即时生效，同时保证浏览器端能读到 10402/10401。
//
// 为什么把 Token 存储校验放到升级之后：浏览器 WebSocket API 读不到握手失败（非 101）的响应体，
// 只能拿到 1006 异常关闭码，「账号已在其它设备登录」这类结论无法送达。放在升级之后即可通过
// WsErrorInfo 错误帧 + Close 码（4002 顶号 / 4006 已撤销）告知客户端。
//
// 为什么保留升级前的签名校验：伪造令牌只需一次 JSON 响应即可拒绝，而升级后拒绝要多付
// 一次协议升级 + 连接缓冲 + 排空等待。签名校验是最廉价的闸门，必须留在升级前。
//
// 容错策略与 httpx.JWTMiddlewareWithTokenStore 保持一致：Redis 查询异常时"失败放行"（仅告警），
// 避免缓存抖动导致全量玩家无法建连；仅在明确判定 Token 不存在/已撤销时拒绝。
//
// 参数：
//   - j:  JWT 签名校验器
//   - ts: 会话 Token 存储；为 nil 时退化为纯签名校验（向后兼容）
//
// 返回：握手鉴权配置（Validate 必非 nil，ts 为 nil 时 CheckSession 为 nil）
func NewJWTAuthWithStore(j *authx.JWT, ts *authx.TokenStore) *HandshakeAuth {
	auth := &HandshakeAuth{
		Validate: func(c fiber.Ctx, token string) (string, error) {
			claims, err := j.ParseToken(token)
			if err != nil {
				return "", err
			}
			return claims.UserID, nil
		},
	}
	if ts == nil {
		return auth
	}
	auth.CheckSession = func(ctx context.Context, requestID string, userId string, token string) error {
		if err := ts.Validate(ctx, userId, token); err != nil {
			if mapped := mapTokenStoreError(err); mapped != nil {
				return mapped
			}
			// Redis 异常：失败放行，避免 Token 存储抖动导致全量玩家无法建立长连接
			logx.Warn("WebSocket 握手 Token 存储校验异常，本次放行",
				zap.String("requestId", requestID),
				zap.String("userId", userId),
				zap.Error(err),
			)
		}
		return nil
	}
	return auth
}

// mapTokenStoreError 把 TokenStore.Validate 的业务性失败映射为握手错误。
// 返回 nil 表示不是令牌本身的问题（如 Redis 查询异常），调用方应按容错策略失败放行。
//
// 参数：err TokenStore.Validate 的返回值
// 返回：ErrSessionReplaced（被新登录顶替）/ ErrTokenRevoked（已撤销或过期）/ nil（存储异常）
func mapTokenStoreError(err error) error {
	if errors.Is(err, authx.ErrSessionReplaced) {
		return ErrSessionReplaced
	}
	if errors.Is(err, authx.ErrTokenNotFound) {
		return ErrTokenRevoked
	}
	return nil
}

// AuthMiddleware 为 WebSocket 升级路径提供升级前的签名鉴权中间件（fiber.Handler）。
// 浏览器 WebSocket API 无法设置 Authorization 请求头，故登录令牌取自 query 参数 token；
// 校验通过后在上下文中写入 userId（键名与 httpx JWTMiddleware 一致），
// 升级处理器通过 conn.Locals("userId") 读取；失败时返回统一 JSON 错误信封。
//
// 注意：这里应只挂载廉价的签名校验（HandshakeAuth.Validate），不要挂会话级校验——
// 升级前的失败响应体浏览器读不到，只能看到 1006；「被顶号/被撤销」必须留给
// UpgradeHandler 在升级后下发（见 HandshakeAuth.CheckSession）。
// validate 为 nil 时跳过鉴权，直接放行。
func AuthMiddleware(validate TokenValidator) fiber.Handler {
	return func(c fiber.Ctx) error {
		if validate == nil {
			return c.Next()
		}
		requestID := requestIDFromFiber(c)
		token := c.Query("token")
		if token == "" {
			logx.Warn("WebSocket 握手缺少登录令牌",
				zap.String("requestId", requestID),
				zap.String("ip", c.IP()),
			)
			return httpx.Fail(c, httpx.ErrCodeUnauthorized, "missing authorization token")
		}
		userId, err := validate(c, token)
		if err != nil {
			logx.Warn("WebSocket 握手鉴权失败",
				zap.String("requestId", requestID),
				zap.String("ip", c.IP()),
				zap.Error(err),
			)
			// 标准两段式配置下 Validate 只验签名，不会返回会话级错误；
			// 保留以下映射是为了兼容自定义校验器，并给出可区分的提示语
			if errors.Is(err, ErrSessionReplaced) {
				return httpx.Fail(c, httpx.ErrCodeSessionReplaced, "session replaced by another login")
			}
			if errors.Is(err, ErrTokenRevoked) {
				return httpx.Fail(c, httpx.ErrCodeUnauthorized, "token has been revoked")
			}
			return httpx.Fail(c, httpx.ErrCodeUnauthorized, "invalid or expired token")
		}
		c.Locals("userId", userId)
		return c.Next()
	}
}

// requestIDFromFiber 从 Fiber 上下文读取请求追踪ID，用于握手鉴权日志关联。
// c 为 nil 时返回空串。
func requestIDFromFiber(c fiber.Ctx) string {
	if c == nil {
		return ""
	}
	rid, _ := c.Locals("requestId").(string)
	return rid
}
