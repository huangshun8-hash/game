package httpx

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/authx"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/protobuf/proto"
)

// TestMaskSensitiveQuery 校验访问日志 query 脱敏：敏感参数值被掩码，其余参数原样保留。
// WebSocket 握手只能通过 query 携带登录令牌，若原样落盘即构成凭证泄露。
func TestMaskSensitiveQuery(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "空 query",
			raw:  "",
			want: "",
		},
		{
			name: "无敏感参数原样返回",
			raw:  "platform=ios&page=2",
			want: "platform=ios&page=2",
		},
		{
			name: "WebSocket 握手令牌被脱敏",
			raw:  "token=eyJhbGciOiJIUzI1NiJ9.abc.def&platform=ios",
			want: "token=***&platform=ios",
		},
		{
			name: "键名大小写不敏感",
			raw:  "accessToken=abc&Password=123&userId=7",
			want: "accessToken=***&Password=***&userId=7",
		},
		{
			name: "签名类参数被脱敏",
			raw:  "sign=abcdef&nonce=1&signature=xyz",
			want: "sign=***&nonce=1&signature=***",
		},
		{
			name: "仅出现敏感子串的非敏感键不受影响",
			raw:  "tokenType=bearer&csrfToken=abc",
			want: "tokenType=bearer&csrfToken=abc",
		},
		{
			name: "无值参数保持结构",
			raw:  "token=&flag",
			want: "token=***&flag",
		},
		{
			name: "LunPlay 验签参数 ck 与账号标识 passport 被脱敏",
			raw:  "serverCode=1&passport=abc&t=1700000000&ck=deadbeef",
			want: "serverCode=1&passport=***&t=1700000000&ck=***",
		},
		{
			name: "尾随分隔符不破坏结构",
			raw:  "token=x&",
			want: "token=***&",
		},
		{
			name: "前导分隔符不破坏结构",
			raw:  "&token=x",
			want: "&token=***",
		},
		{
			name: "敏感参数位于末尾",
			raw:  "platform=ios&token=x",
			want: "platform=ios&token=***",
		},
		{
			name: "连续多个敏感参数",
			raw:  "token=a&secret=b",
			want: "token=***&secret=***",
		},
		{
			name: "无键名参数原样保留",
			raw:  "=orphan&token=x",
			want: "=orphan&token=***",
		},
		{
			name: "超长键名视为非敏感且原样保留",
			raw:  strings.Repeat("t", maskKeyLookupLimit+1) + "=v",
			want: strings.Repeat("t", maskKeyLookupLimit+1) + "=v",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := maskSensitiveQuery([]byte(c.raw)); got != c.want {
				t.Fatalf("maskSensitiveQuery(%q) = %q，期望 %q", c.raw, got, c.want)
			}
		})
	}
}

// TestMaskSensitiveQueryDoesNotMutateInput 校验脱敏过程不修改调用方传入的原始字节切片
func TestMaskSensitiveQueryDoesNotMutateInput(t *testing.T) {
	raw := []byte("token=secret&platform=ios")
	original := string(raw)
	_ = maskSensitiveQuery(raw)
	if string(raw) != original {
		t.Fatalf("原始 query 字节被修改：%q，期望保持 %q", string(raw), original)
	}
}

// TestSensitiveQueryKeysAreLowercase 把「敏感键清单必须全小写」这一约定固化为测试。
// 比对时会先把请求里的键名折叠为小写再查表，清单中一旦混入大写字母，
// 该键将永远无法命中，敏感值会静默明文写入日志——这类缺陷不会有任何报错，只能靠测试兜住。
func TestSensitiveQueryKeysAreLowercase(t *testing.T) {
	for key := range sensitiveQueryKeys {
		if key == "" {
			t.Fatal("敏感键清单不允许包含空键")
		}
		if lower := strings.ToLower(key); lower != key {
			t.Fatalf("敏感键 %q 含大写字母，将永远无法命中，请改为 %q", key, lower)
		}
		if len(key) > maskKeyLookupLimit {
			t.Fatalf("敏感键 %q 长度 %d 超过比对上限 %d，将永远无法命中", key, len(key), maskKeyLookupLimit)
		}
	}
}

// TestLogMiddlewareLogsJSONBusinessError 校验业务错误在日志中间件内被提前落成响应。
// handler 直接 return httpx.NewAppError 时，Fiber 的全局错误处理器要等中间件链返回后才执行；
// 若不在日志中间件内提前执行错误处理，日志只剩 status=200 + error 文案，
// respBody 与 errCode 整体缺失，无法确认实际返回给客户端的业务结果。
func TestLogMiddlewareLogsJSONBusinessError(t *testing.T) {
	const (
		probePath = "/api/bizErrorProbe"
		bizCode   = 2426
		bizMsg    = "order reward already claimed"
	)

	app := newBareTestApp(t)
	app.Use(LogMiddleware())
	app.Post(probePath, func(c fiber.Ctx) error {
		return NewAppError(bizCode, bizMsg)
	})

	before := readTestLogs(t)
	resp, body := doRequestWith(t, app, http.MethodPost, probePath, map[string]string{
		fiber.HeaderContentType: fiber.MIMEApplicationJSON,
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("业务错误响应 HTTP 状态码 = %d，期望统一 200", resp.StatusCode)
	}
	var got struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := sonic.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解析响应体失败: %v, body=%q", err, body)
	}
	if got.Code != bizCode || got.Message != bizMsg {
		t.Fatalf("响应体 = %+v，期望 code=%d message=%q", got, bizCode, bizMsg)
	}

	added := strings.TrimPrefix(readTestLogs(t), before)
	if !strings.Contains(added, `"respBody":{"code":2426`) {
		t.Errorf("访问日志缺少业务错误响应体：%s", added)
	}
	// 响应体已包含 code/message，错误文案不应重复打印
	if strings.Contains(added, `"error":`) {
		t.Errorf("响应体已记录业务结果，错误文案不应重复打印：%s", added)
	}
}

// TestLogMiddlewareLogsProtoBusinessError 校验 protobuf 请求下的业务错误日志。
// 响应体是二进制 ErrorResp，无法按字节打印，需依赖 Fail 写入 Locals 的 respBodyProto
// 懒序列化记录业务码，确保日志中的 errCode 与客户端解出的 code 一致。
func TestLogMiddlewareLogsProtoBusinessError(t *testing.T) {
	const (
		probePath = "/api/bizErrorProbeProto"
		bizCode   = 2426
		bizMsg    = "order reward already claimed"
	)

	app := newBareTestApp(t)
	app.Use(LogMiddleware())
	app.Post(probePath, func(c fiber.Ctx) error {
		return NewAppError(bizCode, bizMsg)
	})

	before := readTestLogs(t)
	req := httptest.NewRequest(http.MethodPost, probePath, nil)
	req.Header.Set(fiber.HeaderContentType, MIMEApplicationProtobuf)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", probePath, err)
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("读取 proto 响应体失败: %v", err)
	}
	var errResp cspb.ErrorResp
	if err := proto.Unmarshal(raw, &errResp); err != nil {
		t.Fatalf("解析 ErrorResp 失败: %v", err)
	}
	if errResp.GetCode() != bizCode {
		t.Fatalf("响应体 code = %d，期望 %d", errResp.GetCode(), bizCode)
	}

	added := strings.TrimPrefix(readTestLogs(t), before)
	if !strings.Contains(added, `"respBody":{"code":2426`) {
		t.Errorf("proto 错误响应体未通过 respBodyProto 记录：%s", added)
	}
	if strings.Contains(added, `"error":`) {
		t.Errorf("响应体已记录业务结果，错误文案不应重复打印：%s", added)
	}
}

// TestTokenStoreFailureMapping 校验 TokenStore 错误到客户端业务错误码的映射：
// 「被新登录顶替」必须使用独立错误码 10402，与令牌过期/撤销（10401）区分；
// Redis 异常不属于业务性失败（ok=false），由中间件按容错策略失败放行。
func TestTokenStoreFailureMapping(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantCode    int
		wantMessage string
		wantOk      bool
	}{
		{
			name:        "被新登录顶替",
			err:         authx.ErrSessionReplaced,
			wantCode:    ErrCodeSessionReplaced,
			wantMessage: "session replaced by another login",
			wantOk:      true,
		},
		{
			name:        "令牌已撤销或过期",
			err:         authx.ErrTokenNotFound,
			wantCode:    ErrCodeUnauthorized,
			wantMessage: "token has been revoked",
			wantOk:      true,
		},
		{
			name:   "存储异常失败放行",
			err:    errors.New("redis unavailable"),
			wantOk: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, message, ok := tokenStoreFailure(c.err)
			if ok != c.wantOk {
				t.Fatalf("ok = %v，期望 %v", ok, c.wantOk)
			}
			if code != c.wantCode || message != c.wantMessage {
				t.Fatalf("映射结果 = (%d, %q)，期望 (%d, %q)", code, message, c.wantCode, c.wantMessage)
			}
		})
	}
}
