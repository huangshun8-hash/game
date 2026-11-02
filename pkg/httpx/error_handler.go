package httpx

import (
	"errors"

	"github.com/gofiber/fiber/v3"
)

// DefaultErrorHandler 是面向客户端接口的全局错误处理器。
// 所有客户端响应均统一返回 HTTP 200，通过响应体中的 code 字段区分业务错误。
// 序列化格式由 Fail 内部按 Accept 头自动选择（JSON 或 protobuf），保持客户端体验一致。
func DefaultErrorHandler(c fiber.Ctx, err error) error {
	// 优先匹配携带业务码的 AppError
	var appErr *AppError
	if errors.As(err, &appErr) {
		return Fail(c, appErr.BizCode, appErr.Message)
	}

	// 兼容 fiber 内置错误（如路由未匹配、请求体过大等框架级错误）
	// 框架错误同样以 HTTP 200 返回，code 使用 HTTP 状态码语义值
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return Fail(c, fiberErr.Code, fiberErr.Message)
	}

	// 兜底：内部服务错误
	return Fail(c, ErrCodeInternal, "internal server error")
}
