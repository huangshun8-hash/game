package httpcli

import (
	"io"

	"github.com/gofiber/fiber/v3/client"
)

// Response 封装了 Fiber 客户端的响应对象，提供便捷的响应数据访问方法。
// 使用完毕后必须调用 Close 释放资源，避免内存泄漏。
type Response struct {
	raw *client.Response
}

// StatusCode 返回 HTTP 响应状态码。
func (r *Response) StatusCode() int {
	return r.raw.StatusCode()
}

// Status 返回 HTTP 响应状态消息。
func (r *Response) Status() string {
	return r.raw.Status()
}

// Header 返回指定响应头的值。若头不存在则返回空字符串。
func (r *Response) Header(key string) string {
	return r.raw.Header(key)
}

// Body 返回响应体原始字节切片。
// 返回的字节切片在调用 Close 后失效，如需保留请提前拷贝。
func (r *Response) Body() []byte {
	return r.raw.Body()
}

// String 返回去除首尾空白后的响应体字符串。
func (r *Response) String() string {
	return r.raw.String()
}

// BodyStream 返回响应体的流式读取器，适用于大文件下载或 SSE 场景。
// 使用流式读取后，调用 Body 可能返回空切片。
func (r *Response) BodyStream() io.Reader {
	return r.raw.BodyStream()
}

// IsStreaming 返回响应体是否以流式传输。
func (r *Response) IsStreaming() bool {
	return r.raw.IsStreaming()
}

// JSON 将响应体反序列化为指定的结构体。
// v 必须为可寻址的指针，否则反序列化将失败。
func (r *Response) JSON(v any) error {
	return r.raw.JSON(v)
}

// Raw 返回底层的 Fiber 客户端响应对象。
// 获取原始对象后仍需通过 Response.Close 释放资源。
func (r *Response) Raw() *client.Response {
	return r.raw
}

// Close 释放响应对象及相关请求资源。
// 调用后不可再访问该 Response 的任何方法。
func (r *Response) Close() {
	r.raw.Close()
}
