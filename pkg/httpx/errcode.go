package httpx

// 通用基础错误码，供中间件层使用。
// 各业务服务应在自身 model 包中定义细化的业务错误码，
// 建议按服务分段（如 platform: 1000-1999，gamelogic: 2000-2999）。
const (
	// ErrCodeUnauthorized 通用未认证错误码（中间件层 Token 缺失或无效）
	ErrCodeUnauthorized = 10401
	// ErrCodeSessionReplaced 会话已被新登录顶替（同一账号在其它设备登录）。
	// 与 ErrCodeUnauthorized（过期/无效/被封禁）区分开，客户端据此提示「账号已在其它设备登录」，
	// 而不是按过期处理去静默重登——静默重登会与其它设备互相顶号。
	ErrCodeSessionReplaced = 10402
	// ErrCodeForbidden 通用无权限错误码
	ErrCodeForbidden = 10403
	// ErrCodeBadRequest 通用请求参数错误码
	ErrCodeBadRequest = 10400
	// ErrCodeNotFound 通用资源未找到错误码
	ErrCodeNotFound = 10404
	// ErrCodeTooManyRequests 通用请求过于频繁错误码（限流触发）
	ErrCodeTooManyRequests = 10429
	// ErrCodeInternal 通用内部服务错误码
	ErrCodeInternal = 10500
)
