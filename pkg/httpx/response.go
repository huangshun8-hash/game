package httpx

import (
	"kemiluo/pb/cspb"

	"github.com/gofiber/fiber/v3"
)

// Response 统一 JSON 接口响应结构
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// AppError 携带业务错误码的应用层错误。
// 面向客户端的接口统一返回 HTTP 200，业务结果通过 BizCode 字段传递。
type AppError struct {
	// BizCode 业务错误码，写入响应体的 code 字段
	BizCode int
	// Message 返回给客户端的错误描述
	Message string
}

// Error 实现 error 接口
func (e *AppError) Error() string {
	return e.Message
}

// NewAppError 创建一个携带业务错误码的应用层错误。
// bizCode: 业务错误码；message: 错误描述（面向客户端，需为英文）
func NewAppError(bizCode int, message string) *AppError {
	return &AppError{
		BizCode: bizCode,
		Message: message,
	}
}

// Fail 发送业务失败响应（HTTP 状态统一 200，通过 code 字段区分业务结果）。
// 根据请求 Content-Type 自动选择序列化格式：
//   - Content-Type 为 application/x-protobuf 或 application/protobuf → 用 proto.Marshal 序列化 ErrorResp 输出二进制
//     （ErrorResp 的 tag 编号与所有 *Resp 前两个字段一致，客户端可用具体 Resp 类型解析）
//   - 其他 → 输出 JSON 信封 {code, message}
func Fail(c fiber.Ctx, bizCode int, message string) error {
	if contentTypeIsProto(c) {
		resp := &cspb.ErrorResp{
			Code:    int32(bizCode),
			Message: message,
		}
		// 存入 Locals 供日志中间件懒序列化打印，避免 proto 错误响应体在日志中缺失（始终打印明文）
		c.Locals("respBodyProto", resp)
		// 携带玩家收发包秘钥时对错误响应同样加密，与正常响应算法一致；秘钥为空时输出明文
		b, err := marshalProtoWire(c, resp)
		if err != nil {
			return err
		}
		c.Set(fiber.HeaderContentType, c.Get(fiber.HeaderContentType))
		return c.Status(fiber.StatusOK).Send(b)
	}
	return c.Status(fiber.StatusOK).JSON(Response{
		Code:    bizCode,
		Message: message,
	})
}
