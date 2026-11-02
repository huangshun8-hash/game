package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"kemiluo/pkg/authx"
	"kemiluo/pkg/logx"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// TimingMiddleware adds a Server-Timing header with request processing duration.
func TimingMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		duration := time.Since(start)
		c.Set("Server-Timing", "total;dur="+duration.String())
		return err
	}
}

// ServerTimeMiddleware 在每个响应的 Header 中注入当前服务器时间（Unix 毫秒）。
// Header 名称为 X-Server-Time，值为 Unix 毫秒时间戳字符串，方便客户端做时间校准。
func ServerTimeMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		err := c.Next()
		c.Set("X-Server-Time", strconv.FormatInt(time.Now().UnixMilli(), 10))
		return err
	}
}

// isJSONContentType 判断 Content-Type 是否为 JSON
func isJSONContentType(ct string) bool {
	return strings.Contains(ct, "application/json")
}

// isBinaryContentType 判断 Content-Type 是否为二进制协议（protobuf、octet-stream、multipart 等），
// 此类内容不适合以文本形式打印到日志，避免乱码与无意义的大字段。
func isBinaryContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "protobuf") ||
		strings.Contains(ct, "octet-stream") ||
		strings.Contains(ct, "multipart/")
}

// bodyLogField 根据 Content-Type 返回合适的 zap.Field：
//   - JSON 内容：以 json.RawMessage 内嵌，zap 懒编码时直接输出 JSON 对象而非转义字符串，无效 JSON 时降级为字符串
//   - 二进制内容（protobuf 等）：返回零值 Field（不打印）
//   - 其他文本内容：作为字符串打印
//
// body 仅在日志写入前的同步流程内使用，直接引用底层字节即可，无需额外拷贝。
func bodyLogField(key, contentType string, body []byte) (zap.Field, bool) {
	if len(body) == 0 {
		return zap.Field{}, false
	}
	// 二进制协议不打印，避免乱码
	if isBinaryContentType(contentType) {
		return zap.Field{}, false
	}
	// JSON 且合法时以 RawMessage 内嵌（json.RawMessage 实现 json.Marshaler，zap 懒编码时直接输出原始 JSON）
	if isJSONContentType(strings.ToLower(contentType)) && sonic.Valid(body) {
		return zap.Reflect(key, json.RawMessage(body)), true
	}
	// 其他文本（form、plain text 等）作为字符串打印
	return zap.String(key, string(body)), true
}

// lazyProtoJSON 包装 proto.Message 实现 json.Marshaler，配合 zap.Reflect 实现懒序列化：
// 仅当日志级别通过、zap 真正写日志时才调用 sonic.Marshal，级别被过滤时零开销。
// 相比"先 Marshal 再 Unmarshal 成 map 后逐字段写入"的方式，此处仅序列化一次，
// 且输出为平铺的 JSON 对象（数值、布尔、嵌套结构均保持原始 JSON 形态）。
type lazyProtoJSON struct {
	msg proto.Message
}

// MarshalJSON 实现 json.Marshaler 接口，使用 sonic 序列化 proto 消息
func (l lazyProtoJSON) MarshalJSON() ([]byte, error) {
	return sonic.Marshal(l.msg)
}

// sensitiveQueryKeys 访问日志中必须脱敏的 query 参数名。
// 浏览器 WebSocket API 无法设置请求头，登录令牌只能经 query 传递（如 /ws?token=xxx），
// 且 LogBodySkipPaths 只作用于请求/响应体，因此必须在日志层按键名脱敏，否则凭证会原样落盘。
//
// 约定：**键必须全部为小写**。比对时会把请求中的键名折叠为小写后查表，
// 表中一旦出现大写字母，该键将永远无法命中，敏感值会静默明文落盘。
// maskKeyLookupLimit 之外的超长键名不查表（直接视为非敏感），避免异常请求放大开销。
var sensitiveQueryKeys = map[string]struct{}{
	// 登录/会话凭证
	"token":         {},
	"accesstoken":   {},
	"refreshtoken":  {},
	"idtoken":       {},
	"authorization": {},
	"sessionid":     {},
	"ticket":        {},
	// 下划线形式仅用于识别第三方回调传入的参数名（本项目自身对外参数一律 camelCase）
	"access_token":  {},
	"refresh_token": {},
	// 口令与密钥
	"password":  {},
	"passwd":    {},
	"pwd":       {},
	"secret":    {},
	"apikey":    {},
	"appsecret": {},
	// 验签参数：ck 为 LunPlay 系列接口的 md5(...+key) 签名，与 sign/signature 同级。
	// 这些参数以 query 形式出现在 GET 接口上（见 services/platform 的 lunplay 路由），
	// 若明文落盘即泄露可重放的签名/账号标识。
	"sign":      {},
	"signature": {},
	"ck":        {},
	// 账号标识：LunPlay 的 passport（即 openId）可直接定位到具体账号
	"passport": {},
	// OAuth 授权码，可换取访问令牌
	"code": {},
}

// maskedQueryValue 敏感 query 参数在日志中的替代值
const maskedQueryValue = "***"

// maskKeyLookupLimit 参与敏感键比对的最大键名长度（字节）。
// 敏感键清单中最长的键远小于该值，超长键名一律视为非敏感，
// 避免构造超长参数名的请求把脱敏逻辑变成放大器。
const maskKeyLookupLimit = 64

// maskSensitiveQuery 对 query string 做脱敏：保留键名便于排查，敏感键的值替换为掩码。
//
// 实现为单遍扫描：逐个切分 key=value，把键名折叠成小写后查表，命中即掩码。
// 未命中任何敏感键时直接返回原始串，不做重组与额外分配
// （`map[string(byteSlice)]` 由编译器优化为免分配查找，键名折叠使用栈上缓冲）。
// 不采用「先整串搜索敏感子串再决定是否重组」的两段式写法：那样既要为每个敏感键各扫一遍全串，
// 又要求清单里的键名恒为小写，一旦有人加入大写键就会静默漏脱敏。
//
// 参数：raw 原始 query string 字节（不修改其内容）
// 返回：可安全写入日志的 query string
func maskSensitiveQuery(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var sb strings.Builder
	// masked 标记是否出现过掩码；未出现时直接返回原始串，避免无谓的重组开销。
	// copied 为「已搬入缓冲的原始内容」边界：非敏感片段与分隔符不逐个处理，
	// 而是在下一次命中（或收尾）时整段拷贝，从而完整保留原始分隔结构。
	masked := false
	copied := 0
	for pos := 0; pos < len(raw); {
		end := len(raw)
		if i := bytes.IndexByte(raw[pos:], '&'); i >= 0 {
			end = pos + i
		}
		pair := raw[pos:end]
		key := pair
		if i := bytes.IndexByte(pair, '='); i >= 0 {
			key = pair[:i]
		}
		if isSensitiveQueryKey(key) {
			if !masked {
				masked = true
				sb.Grow(len(raw) + len(maskedQueryValue))
			}
			// 命中前的原始内容（含分隔符与非敏感参数）整段保留
			sb.Write(raw[copied:pos])
			sb.Write(key)
			sb.WriteByte('=')
			sb.WriteString(maskedQueryValue)
			copied = end
		}
		pos = end + 1 // 跳过 '&'
	}
	if !masked {
		return string(raw)
	}
	sb.Write(raw[copied:])
	return sb.String()
}

// isSensitiveQueryKey 判断 query 参数名是否属于敏感键（ASCII 大小写不敏感）。
// 折叠小写时使用栈上缓冲，不产生堆分配。
//
// 参数：key 原始键名字节
// 返回：true 表示该参数的值必须脱敏
func isSensitiveQueryKey(key []byte) bool {
	if len(key) == 0 || len(key) > maskKeyLookupLimit {
		return false
	}
	var buf [maskKeyLookupLimit]byte
	for i, b := range key {
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		buf[i] = b
	}
	_, ok := sensitiveQueryKeys[string(buf[:len(key)])]
	return ok
}

// LogMiddleware logs each HTTP request with method, path, status, latency, IP,
// request body (params), and response body.
// skipBodyPaths 中的路径不记录请求/响应体，支持以 * 结尾的前缀通配（如 "/api/config/releases/*/upload"）。
func LogMiddleware(skipBodyPaths ...string) fiber.Handler {
	type matcher struct {
		prefix string // 非空时表示前缀匹配
		suffix string
		exact  string // 精确路径
	}
	matchers := make([]matcher, 0, len(skipBodyPaths))
	for _, p := range skipBodyPaths {
		if prefix, suffix, found := strings.Cut(p, "*"); found {
			matchers = append(matchers, matcher{prefix: prefix, suffix: suffix})
		} else {
			matchers = append(matchers, matcher{exact: p})
		}
	}

	skipBody := func(path string) bool {
		for _, m := range matchers {
			if m.exact != "" {
				if path == m.exact {
					return true
				}
			} else {
				if strings.HasPrefix(path, m.prefix) && strings.HasSuffix(path, m.suffix) {
					return true
				}
			}
		}
		return false
	}

	return func(c fiber.Ctx) error {
		start := time.Now()

		path := c.Path()
		skip := skipBody(path)

		// 记录请求体与 Content-Type（处理前）。skip 的敏感接口不做任何相关计算。
		// proto 请求由 Bind 写入 Locals("reqBodyProto") 后懒序列化，二进制请求不拷贝原始字节，
		// 避免无用的堆分配。c.Body() 只是 getter 不消费 body，handler 后续仍可正常读取。
		var reqBodyRaw []byte
		var reqContentType string
		if !skip {
			reqContentType = string(c.Request().Header.ContentType())
			if !isBinaryContentType(reqContentType) {
				if raw := c.Body(); len(raw) > 0 {
					reqBodyRaw = make([]byte, len(raw))
					copy(reqBodyRaw, raw)
				}
			}
		}

		// query string：单遍扫描做敏感参数脱敏（token / password / ck 等只保留键名），
		// 未命中敏感键时原样返回原始字节，不走 QueryArgs 解析再重组的开销
		queryStr := maskSensitiveQuery(c.Request().URI().QueryString())

		err := c.Next()

		// Fiber 的全局错误处理器要等整条中间件链返回之后才执行：业务 handler 直接 return 错误
		// （如 httpx.NewAppError）时，此处响应体尚未写出，respBody 与业务错误码会整体缺失，
		// 日志无法与客户端实际收到的结果对齐。因此在采集日志前先执行一次全局错误处理，
		// 把最终响应落到 Response 上；处理成功即代表响应已写出，不再向上返回错误。
		nextErr := err
		if err != nil {
			// 记录 handler 返回的原始错误用于日志，nextErr 仅决定是否需要交回 Fiber 兜底
			if handlerErr := c.App().ErrorHandler(c, err); handlerErr != nil {
				nextErr = handlerErr
			} else {
				nextErr = nil
			}
		}

		latency := time.Since(start)
		status := c.Response().StatusCode()

		requestID, _ := c.Locals("requestId").(string)

		// JWT 中间件执行后已写入 userId，若存在则追加到日志
		userID, _ := c.Locals("userId").(string)

		// 静态托管中间件直接响应时会写入该标记，日志按两点降噪：
		// 1) JS/CSS bundle 常达数 MB、图片与字体为二进制，全量写日志会造成日志膨胀与乱码，故跳过请求/响应体；
		// 2) 一次后台页面加载会产生数十个资源请求，成功命中的访问日志降级为 Debug，避免淹没业务日志；
		//    4xx/5xx 仍按 Warn/Error 记录，保证「产物缺失」这类故障可查。
		isStaticAsset := isStaticAssetRequest(c)
		if isStaticAsset {
			skip = true
		}

		var respBodyRaw []byte
		var respContentType string
		if !skip {
			respBodyRaw = c.Response().Body()
			respContentType = string(c.Response().Header.ContentType())
		}

		fields := []zap.Field{
			zap.String("method", c.Method()),
			zap.String("path", path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.String("ip", c.IP()),
			zap.String("requestId", requestID),
		}

		if queryStr != "" {
			fields = append(fields, zap.String("query", queryStr))
		}
		if userID != "" {
			fields = append(fields, zap.String("userId", userID))
		}
		// 仅记录 Content-Type（reqContentType 仅在非 skip 时赋值），
		// 不再全量打印请求头，避免放大日志体积与写盘开销
		if reqContentType != "" {
			fields = append(fields, zap.String("contentType", reqContentType))
		}
		// 敏感接口（skip=true）不打印任何请求/响应体。
		// proto 请求/响应经 Bind、OK、Fail 写入 Locals，此处必须同样受 skip 约束，
		// 否则 proto 传输的登录凭证、支付收据等敏感报文会绕过 LogBodySkipPaths 泄露到日志。
		hasRespBody := false
		if !skip {
			// proto 请求：优先使用 Bind 解析后写入 Locals 的 proto.Message，懒序列化为 JSON
			if protoMsg, ok := c.Locals("reqBodyProto").(proto.Message); ok && protoMsg != nil {
				fields = append(fields, zap.Reflect("reqBody", lazyProtoJSON{msg: protoMsg}))
			} else if protoErr, ok := c.Locals("reqBodyErr").(error); ok && protoErr != nil {
				// 解析失败，打印错误信息
				fields = append(fields, zap.String("reqBodyErr", protoErr.Error()))
			} else if f, ok := bodyLogField("reqBody", reqContentType, reqBodyRaw); ok {
				// 非 proto 请求走常规逻辑
				fields = append(fields, f)
			}
			if protoMsg, ok := c.Locals("respBodyProto").(proto.Message); ok && protoMsg != nil {
				// proto 响应同样懒序列化
				fields = append(fields, zap.Reflect("respBody", lazyProtoJSON{msg: protoMsg}))
				hasRespBody = true
			} else if f, ok := bodyLogField("respBody", respContentType, respBodyRaw); ok {
				fields = append(fields, f)
				hasRespBody = true
			}
		}
		// 响应体已记录业务结果（code/message）时不再重复打印错误文案；
		// 仅敏感路径（skip，不记录响应体）或响应体缺失时保留错误信息，避免丢失排查线索。
		if err != nil && !hasRespBody {
			fields = append(fields, zap.Error(err))
		}

		switch {
		case status >= 500:
			logx.Error("request", fields...)
		case status >= 400:
			logx.Warn("request", fields...)
		case isStaticAsset:
			// 静态资源成功命中：降级为 Debug，避免单次页面加载的数十条资源日志淹没业务日志
			logx.Debug("request", fields...)
		default:
			logx.Info("request", fields...)
		}

		return nextErr
	}
}

// JWTMiddleware validates the Bearer token from the Authorization header
// using authx.JWT, and stores the parsed claims + user_id in Locals.
// skipPaths 中的路径将跳过 JWT 校验（如登录接口）。
//
// Locals keys set:
//   - "userId" : string — 已认证用户的唯一标识（与 Context.UserID() 读取 key 一致）
//   - "claims"  : *authx.Claims
func JWTMiddleware(j *authx.JWT, skipPaths ...string) fiber.Handler {
	return JWTMiddlewareWithTokenStore(j, nil, skipPaths...)
}

// JWTMiddlewareWithTokenStore 在 JWT 签名校验基础上，额外对比 Redis 中存储的 Token 值，
// 实现踢人/封禁后令牌即时失效：管理员执行踢人或封禁时调用 TokenStore.Revoke 删除 Redis key，
// 下次请求因 key 不存在或值不匹配而被拒绝（401）。
//
// 容错策略：当 Redis 查询异常时采取"失败放行"（仅告警），避免缓存抖动导致全量用户无法访问。
// ts 为 nil 时退化为纯 JWT 签名校验（向后兼容）。
func JWTMiddlewareWithTokenStore(j *authx.JWT, ts *authx.TokenStore, skipPaths ...string) fiber.Handler {
	skipSet := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
		skipSet[p] = struct{}{}
	}

	return func(c fiber.Ctx) error {
		// 检查是否在跳过列表中
		if _, skip := skipSet[c.Path()]; skip {
			return c.Next()
		}
		token := c.Get("Authorization")
		if token == "" {
			return Fail(c, ErrCodeUnauthorized, "missing authorization token")
		}

		// 剥离 Bearer 前缀
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		claims, err := j.ParseToken(token)
		if err != nil {
			return Fail(c, ErrCodeUnauthorized, "invalid or expired token")
		}

		// Token 存储校验：对比 Redis 中存储的值，实现踢人/封禁即时生效
		if ts != nil {
			if err := ts.Validate(c.Context(), claims.UserID, token); err != nil {
				if code, message, ok := tokenStoreFailure(err); ok {
					return Fail(c, code, message)
				}
				// Redis 异常时失败放行，仅记录告警，避免缓存抖动导致全量用户被拒
				logx.Warn("Token存储校验异常，本次放行",
					zap.String("requestId", localRequestID(c)),
					zap.String("userId", claims.UserID),
					zap.Error(err),
				)
			}
		}

		c.Locals("userId", claims.UserID)
		c.Locals("claims", claims)
		return c.Next()
	}
}

// tokenStoreFailure 把 TokenStore.Validate 的业务性失败分类为客户端响应。
// 返回 ok=false 表示不是令牌本身的问题（如 Redis 查询异常），调用方应按容错策略失败放行。
//
// 参数：err TokenStore.Validate 的返回值
// 返回：业务错误码、英文提示、是否属于令牌失败
func tokenStoreFailure(err error) (code int, message string, ok bool) {
	switch {
	case errors.Is(err, authx.ErrSessionReplaced):
		return ErrCodeSessionReplaced, "session replaced by another login", true
	case errors.Is(err, authx.ErrTokenNotFound):
		return ErrCodeUnauthorized, "token has been revoked", true
	default:
		return 0, "", false
	}
}

// localRequestID 从 Locals 读取请求追踪ID，便于日志关联
func localRequestID(c fiber.Ctx) string {
	rid, _ := c.Locals("requestId").(string)
	return rid
}

// AuthMiddleware validates a token extracted from the Authorization header
// and stores the user ID in the context. The validate function should return
// the user ID on success or an error on failure.
func AuthMiddleware(validate func(token string) (userID string, err error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		token := c.Get("Authorization")
		if token == "" {
			return Fail(c, ErrCodeUnauthorized, "missing authorization token")
		}

		// Strip "Bearer " prefix if present
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		uid, err := validate(token)
		if err != nil {
			return Fail(c, ErrCodeUnauthorized, "invalid token")
		}

		c.Locals("userId", uid)
		return c.Next()
	}
}
