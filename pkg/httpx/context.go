package httpx

import (
	"kemiluo/pkg/authx"

	"github.com/gofiber/fiber/v3"
)

// Context 是扩展的请求上下文接口，仅承载请求级数据。
// 服务级依赖（Mongo、Redis、JWT等）应通过 handler 层的 Deps 容器注入，不放在此处。
type Context interface {
	fiber.Ctx

	// UserID 获取已认证用户的唯一标识字符串（由中间件写入）
	UserID() string

	// SetUserID 存储已认证用户的唯一标识
	SetUserID(uid string)

	// PlayerID 获取已认证玩家的唯一标识（UserID 解析为 int64），未认证时返回 0。
	// 适用于 gamelogic 等以 int64 存储玩家ID的服务；GMT 等使用字符串 ID 的服务请用 UserID()。
	PlayerID() int64

	// RequestID 返回当前请求的追踪ID
	RequestID() string

	// Claims 返回当前请求解析后的 JWT Claims，未经过认证的请求返回 nil
	Claims() *authx.Claims

	// PacketKey 返回当前请求 JWT Claims 中携带的玩家收发包加密秘钥（base64），
	// 用于对 protobuf 收发包做 AES 加解密。
	// 未携带（如管理端 Token）或未经过认证的请求返回空字符串
	PacketKey() string
}

// Handler 是类型化的处理函数，接收扩展的 Context 接口
type Handler func(c Context) error

// ToFiberHandler 将类型化的 Handler 转为 fiber.Handler
// 由于 Fiber 使用 NewWithCustomCtx 注册了自定义上下文工厂，底层 Ctx 始终为 *Ctx
func ToFiberHandler(h Handler) fiber.Handler {
	return func(c fiber.Ctx) error {
		return h(c.(Context))
	}
}

// Ctx 是 Context 接口的具体实现，扩展 Fiber 的 DefaultCtx。
// 业务代码不应跨协程持有此上下文的引用（Fiber 会复用池中实例）。
type Ctx struct {
	fiber.DefaultCtx
}

// NewCtx 创建新的自定义上下文实例
func NewCtx(app *fiber.App) *Ctx {
	return &Ctx{
		DefaultCtx: *fiber.NewDefaultCtx(app),
	}
}

// UserID 获取已认证用户的唯一标识字符串
func (c *Ctx) UserID() string {
	return fiber.Locals[string](c, "userId")
}

// SetUserID 存储已认证用户的唯一标识
func (c *Ctx) SetUserID(uid string) {
	fiber.Locals(c, "userId", uid)
}

// PlayerID 获取已认证玩家的唯一标识（UserID 解析为 int64），未认证时返回 0
func (c *Ctx) PlayerID() int64 {
	return parsePlayerID(c.UserID())
}

// RequestID 返回当前请求的追踪ID（由 requestid 中间件写入）
func (c *Ctx) RequestID() string {
	rid := fiber.Locals[string](c, "requestId")
	if rid != "" {
		return rid
	}
	return c.Get("X-Request-Id")
}

// Claims 返回当前请求解析后的 JWT Claims（由 JWTMiddleware 写入 Locals）
// 未经过认证的请求（如登录接口）返回 nil
func (c *Ctx) Claims() *authx.Claims {
	return fiber.Locals[*authx.Claims](c, "claims")
}

// PacketKey 返回当前请求 JWT Claims 中携带的玩家收发包加密秘钥（base64）
// 未携带或未经过认证的请求返回空字符串
func (c *Ctx) PacketKey() string {
	claims := c.Claims()
	if claims == nil {
		return ""
	}
	return claims.PacketKey
}
