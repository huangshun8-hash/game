package httpx

import (
	"kemiluo/pkg/packetx"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"google.golang.org/protobuf/proto"
)

// requestIDSetter 嵌入 BaseReq 的类型自动实现此接口，用于内部填充 requestID
type requestIDSetter interface {
	SetRequestID(string)
}

// playerIDSetter 嵌入 BaseReq 的类型自动实现此接口，用于内部填充 playerID
type playerIDSetter interface {
	SetPlayerID(int64)
}

// BaseReq 请求基础字段，嵌入所有非 proto 请求 DTO 中。
// 由 httpx.Bind 系列函数自动填充，业务代码不应手动设置。
// proto 请求消息不嵌入此类型，requestID 和 playerID 由 handler 从 Context 显式读取后作为参数传递。
type BaseReq struct {
	requestID string
	playerID  int64
}

// RequestID 返回当前请求的追踪ID
func (b BaseReq) RequestID() string {
	return b.requestID
}

// PlayerID 返回当前请求的玩家ID（由 JWT 中间件解析后自动填充，未登录时为 0）
func (b BaseReq) PlayerID() int64 {
	return b.playerID
}

// SetRequestID 设置请求追踪ID（仅供 httpx.Bind 内部调用）
func (b *BaseReq) SetRequestID(id string) {
	b.requestID = id
}

// SetPlayerID 设置玩家ID（仅供 httpx.Bind 内部调用）
func (b *BaseReq) SetPlayerID(id int64) {
	b.playerID = id
}

// Bind 解析请求体到目标结构体。
//
// 序列化格式按 Content-Type 自动选择：
//   - application/x-protobuf 或 application/protobuf → proto.Unmarshal（T 须实现 proto.Message）
//   - 其他（含 application/json 及空） → sonic JSON
//
// 对于嵌入了 BaseReq 的普通 DTO，自动填充 requestID 和 playerID。
// 对于 proto 消息，不注入追踪信息，handler 应从 Context 显式读取后作为参数传递给 logic 层。
// 当请求体为空时跳过 body 解析，仅对普通 DTO 填充 BaseReq。
//
// proto 请求解析结果会写入 Fiber Locals 供日志中间件懒序列化打印：
//   - 解析成功：Locals("reqBodyProto") = proto.Message（打印时才序列化为 JSON）
//   - 解析失败：Locals("reqBodyErr") = error
func Bind[T any](c Context) (*T, error) {
	req := new(T)
	if body := c.Body(); len(body) > 0 {
		if pm, ok := any(req).(proto.Message); ok && contentTypeIsProto(c) {
			// 收发包加解密：请求体为 protobuf 且携带玩家收发包秘钥时，先解密为明文 protobuf 字节。
			// 秘钥为空（登录、管理端等未认证或无秘钥场景）时按明文处理，保持登录等接口现状不变。
			plain := body
			if key := packetKeyFromCtx(c); key != nil {
				opened, err := packetx.OpenWire(body, key)
				if err != nil {
					c.Locals("reqBodyErr", err)
					return nil, err
				}
				plain = opened
			}
			if err := proto.Unmarshal(plain, pm); err != nil {
				// 写入解析错误，供日志中间件打印
				c.Locals("reqBodyErr", err)
				return nil, err
			}
			// 解析成功，将 proto message 存入 Locals 供日志中间件懒序列化打印
			c.Locals("reqBodyProto", pm)
		} else {
			if err := c.Bind().Body(req); err != nil {
				return nil, err
			}
		}
	}
	// 仅对嵌入了 BaseReq 的普通 DTO 填充追踪信息
	if setter, ok := any(req).(requestIDSetter); ok {
		setter.SetRequestID(c.RequestID())
	}
	if setter, ok := any(req).(playerIDSetter); ok {
		setter.SetPlayerID(parsePlayerID(c.UserID()))
	}
	return req, nil
}

// BindQuery 解析查询参数到目标结构体。
//
// 直接依赖 Fiber v3 的 Bind().Query()：解析器会读取 `query` tag，无 tag 时回退到结构体字段名，
// 并将 tag/字段名统一转小写后与 query 键匹配，因此 proto 生成的 PascalCase 字段（如 `OrderId`）
// 能自动匹配 camelCase 查询参数（如 `orderId`），无需额外中间层。
// 对于嵌入了 BaseReq 的普通 DTO，自动填充 requestID 和 playerID；
// proto 消息不注入追踪信息，handler 应从 Context 显式读取后作为参数传递。
func BindQuery[T any](c Context) (*T, error) {
	req := new(T)
	if err := c.Bind().Query(req); err != nil {
		return nil, err
	}
	if setter, ok := any(req).(requestIDSetter); ok {
		setter.SetRequestID(c.RequestID())
	}
	if setter, ok := any(req).(playerIDSetter); ok {
		setter.SetPlayerID(parsePlayerID(c.UserID()))
	}
	return req, nil
}

// BindParams 解析路由参数到目标结构体，并自动填充 BaseReq 中的 requestID 和 playerID。
func BindParams[T any](c Context) (*T, error) {
	req := new(T)
	if err := c.Bind().URI(req); err != nil {
		return nil, err
	}
	if setter, ok := any(req).(requestIDSetter); ok {
		setter.SetRequestID(c.RequestID())
	}
	if setter, ok := any(req).(playerIDSetter); ok {
		setter.SetPlayerID(parsePlayerID(c.UserID()))
	}
	return req, nil
}

const (
	// MIMEApplicationProtobuf protobuf 二进制的 MIME 类型
	MIMEApplicationProtobuf = "application/x-protobuf"
	// MIMEApplicationProtobufAlt protobuf 二进制的备用 MIME 类型
	MIMEApplicationProtobufAlt = "application/protobuf"
)

// OK 发送成功响应，根据请求的 Content-Type 及 data 类型自动选择序列化格式：
//   - data 是 proto.Message：
//   - Content-Type 为 protobuf → 直接 proto.Marshal 输出二进制；
//     同时将 proto.Message 写入 Locals("respBodyProto") 供日志中间件懒序列化打印。
//   - 否则 → 直接序列化 proto 消息本身作为响应体（proto 消息已含 code/message/data 字段，
//     无需再套 httpx.Response 信封，避免出现嵌套 {code:0, data:{code:X, ...}} 的双层结构）。
//   - data 是普通类型：统一套 httpx.Response{code:0, message:"ok", data:...} 后以 JSON 返回。
func OK(c fiber.Ctx, data any) error {
	if pm, ok := data.(proto.Message); ok {
		if contentTypeIsProto(c) {
			// 先将明文 proto 存入 Locals 供日志中间件懒序列化打印（无论是否加密，日志始终打印明文）
			c.Locals("respBodyProto", pm)
			b, err := marshalProtoWire(c, pm)
			if err != nil {
				return err
			}
			c.Set(fiber.HeaderContentType, c.Get(fiber.HeaderContentType))
			return c.Status(fiber.StatusOK).Send(b)
		}
		// proto 消息本身已含 code/message/data，直接序列化，无需套信封
		return c.JSON(data)
	}
	// 普通类型统一套标准 JSON 信封
	return c.JSON(Response{
		Code:    0,
		Message: "ok",
		Data:    data,
	})
}

// packetKeyFromCtx 从请求上下文中解析当前玩家的收发包加密秘钥（原始字节）。
// 秘钥源为 JWT Claims 中的 base64 秘钥（登录时下发），经 packetx.DecodeKey 解码为原始字节。
// 未认证、无秘钥（如登录、管理端 Token）或秘钥格式非法时返回 nil，调用方据此走明文收发，保持这些接口现状不变。
func packetKeyFromCtx(c fiber.Ctx) []byte {
	ctx, ok := c.(Context)
	if !ok {
		return nil
	}
	packetKey := ctx.PacketKey()
	if packetKey == "" {
		return nil
	}
	key, err := packetx.DecodeKey(packetKey)
	if err != nil {
		return nil
	}
	return key
}

// marshalProtoWire 将 proto 响应消息序列化为线格式字节。
// 携带玩家收发包秘钥时经 AES-CBC 加密（与客户端算法一致）；
// 秘钥为空（登录、管理端等）时输出裸 protobuf 明文，保持这些接口现状不变。
func marshalProtoWire(c fiber.Ctx, pm proto.Message) ([]byte, error) {
	if key := packetKeyFromCtx(c); key != nil {
		return packetx.Encode(pm, key)
	}
	return proto.Marshal(pm)
}

// contentTypeIsProto 判断 Content-Type 是否为 protobuf 格式。
// 兼容 application/x-protobuf 和 application/protobuf，忽略 "; param=value" 附加参数。
func contentTypeIsProto(c fiber.Ctx) bool {
	ct := c.Get(fiber.HeaderContentType)
	if idx := strings.IndexByte(ct, ';'); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}
	return strings.EqualFold(ct, MIMEApplicationProtobuf) ||
		strings.EqualFold(ct, MIMEApplicationProtobufAlt)
}

// parsePlayerID 将 userID 字符串解析为 int64，解析失败时返回 0
func parsePlayerID(userID string) int64 {
	if userID == "" {
		return 0
	}
	pid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return 0
	}
	return pid
}
