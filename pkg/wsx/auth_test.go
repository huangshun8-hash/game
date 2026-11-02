package wsx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"

	"kemiluo/pkg/authx"
	"kemiluo/pkg/httpx"
)

// authResponse 握手鉴权失败时的统一 JSON 错误信封
type authResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// doHandshake 用给定的令牌校验器发起一次握手请求（不做真正的协议升级），返回解析后的错误信封。
// 参数：t 测试上下文；validate 令牌校验器；query 请求 query（含 token）
// 返回：resp 响应信封；status HTTP 状态码
func doHandshake(t *testing.T, validate TokenValidator, query string) (resp authResponse, status int) {
	t.Helper()
	app := fiber.New()
	app.Get("/ws", AuthMiddleware(validate), func(c fiber.Ctx) error {
		return httpx.OK(c, nil)
	})
	req := httptest.NewRequest(fiber.MethodGet, "/ws"+query, nil)
	httpResp, err := app.Test(req)
	if err != nil {
		t.Fatalf("发起握手请求失败: %v", err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	if err := sonic.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析响应体失败: %v（原始内容 %q）", err, string(body))
	}
	return resp, httpResp.StatusCode
}

// TestAuthMiddlewareRejectsRevokedToken 校验自定义校验器返回「令牌被撤销」时，升级前仍能拒绝并给出可区分提示语。
//
// 标准两段式配置（HandshakeAuth）不会走到这里：令牌撤销属于会话级结论，必须放在升级后下发，
// 否则浏览器读不到响应体；本用例保障自定义 TokenValidator 的行为不退化。
func TestAuthMiddlewareRejectsRevokedToken(t *testing.T) {
	validate := func(c fiber.Ctx, token string) (string, error) {
		return "", ErrTokenRevoked
	}
	resp, status := doHandshake(t, validate, "?token=revoked")
	// 对客户端接口统一返回 HTTP 200，业务结果由 code 字段表达
	if status != fiber.StatusOK {
		t.Fatalf("HTTP 状态码应为 200，实际为 %d", status)
	}
	if resp.Code != httpx.ErrCodeUnauthorized {
		t.Fatalf("业务错误码应为 %d，实际为 %d", httpx.ErrCodeUnauthorized, resp.Code)
	}
	if resp.Message != "token has been revoked" {
		t.Fatalf("提示语应区分「令牌被撤销」，实际为 %q", resp.Message)
	}
}

// TestAuthMiddlewareRejectsInvalidToken 校验签名无效时的提示语与「令牌被撤销」不同
func TestAuthMiddlewareRejectsInvalidToken(t *testing.T) {
	validate := func(c fiber.Ctx, token string) (string, error) {
		return "", errors.New("signature mismatch")
	}
	resp, _ := doHandshake(t, validate, "?token=bad")
	if resp.Code != httpx.ErrCodeUnauthorized {
		t.Fatalf("业务错误码应为 %d，实际为 %d", httpx.ErrCodeUnauthorized, resp.Code)
	}
	if resp.Message != "invalid or expired token" {
		t.Fatalf("签名无效应返回 invalid or expired token，实际为 %q", resp.Message)
	}
}

// TestAuthMiddlewareRejectsMissingToken 校验缺少 token 时直接拒绝，不进入校验器
func TestAuthMiddlewareRejectsMissingToken(t *testing.T) {
	called := false
	validate := func(c fiber.Ctx, token string) (string, error) {
		called = true
		return "1", nil
	}
	resp, _ := doHandshake(t, validate, "")
	if called {
		t.Fatal("缺少 token 时不应调用令牌校验器")
	}
	if resp.Message != "missing authorization token" {
		t.Fatalf("应提示缺少令牌，实际为 %q", resp.Message)
	}
}

// TestAuthMiddlewareSkipsWhenValidatorNil 校验 validate 为 nil 时跳过鉴权直接放行
func TestAuthMiddlewareSkipsWhenValidatorNil(t *testing.T) {
	resp, _ := doHandshake(t, nil, "")
	if resp.Code != 0 {
		t.Fatalf("validate 为 nil 时应直接放行，实际业务码为 %d", resp.Code)
	}
}

// TestJWTAuthWithoutStoreOnlyChecksSignature 校验未提供 TokenStore 时退化为纯签名校验。
// 该分支仅适用于 Redis 未就绪的场景；生产环境必须传入 TokenStore，否则封禁无法拦截新握手。
func TestJWTAuthWithoutStoreOnlyChecksSignature(t *testing.T) {
	// NewJWTAuth 应等价于 NewJWTAuthWithStore(j, nil)：有升级前签名校验，无升级后会话校验
	for _, auth := range []*HandshakeAuth{NewJWTAuth(nil), NewJWTAuthWithStore(nil, nil)} {
		if auth == nil {
			t.Fatal("构造函数不应返回 nil")
		}
		if auth.Validate == nil {
			t.Fatal("升级前签名校验器不应为 nil")
		}
		if auth.CheckSession != nil {
			t.Fatal("未提供 TokenStore 时不应启用升级后会话校验")
		}
	}
}

// TestUpgradeRejectsReplacedSession 校验「被新登录顶替」在升级后以 Close 码 4002 补发顶号通知。
//
// 浏览器 WebSocket API 读不到握手失败（非 101）的响应体，只能看到 1006；
// 顶号结论必须通过升级后的 Close 帧告知。客户端离线期间收不到原本的 4002，
// 重连时补发同一个码，语义与「已建连后被踢」一致，不再额外下发业务错误帧。
func TestUpgradeRejectsReplacedSession(t *testing.T) {
	cases := []struct {
		name       string
		sessionErr error
		wantClose  int
	}{
		{name: "被新登录顶替", sessionErr: ErrSessionReplaced, wantClose: CloseCodeConnReplaced},
		{name: "令牌已撤销", sessionErr: ErrTokenRevoked, wantClose: CloseCodeInvalidIdentity},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := startUpgradeTestServer(t, &HandshakeAuth{
				Validate: func(_ fiber.Ctx, _ string) (string, error) { return "1001", nil },
				CheckSession: func(_ context.Context, _, _, _ string) error {
					return c.sessionErr
				},
			})

			conn, _, err := websocket.DefaultDialer.Dial(env.url, nil)
			if err != nil {
				t.Fatalf("升级应成功（鉴权结论通过关闭码下发），实际失败: %v", err)
			}
			defer func() { _ = conn.Close() }()

			// 升级后立即以对应 Close 码断开，且不夹带任何业务帧
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			messageType, data, readErr := conn.ReadMessage()
			if readErr == nil {
				t.Fatalf("拒绝建连时不应下发业务帧，实际收到 type=%d data=%q", messageType, string(data))
			}
			if !websocket.IsCloseError(readErr, c.wantClose) {
				t.Fatalf("应收到关闭码 %d，实际错误为 %v", c.wantClose, readErr)
			}
		})
	}
}

// startUpgradeTestServer 启动仅挂载 WebSocket 端点的测试服务（含升级后会话校验）。
// 参数：t 测试上下文；auth 两段式握手鉴权配置
// 返回：指向该端点的测试环境（地址已带 token 参数）
func startUpgradeTestServer(t *testing.T, auth *HandshakeAuth) *testEnv {
	t.Helper()

	hub := NewHub()
	app := fiber.New()
	Register(app, "/ws", hub, newTestConfig(), auth)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听测试端口失败: %v", err)
	}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	t.Cleanup(func() {
		_ = app.Shutdown()
		<-serveDone
	})

	return &testEnv{hub: hub, url: "ws://" + ln.Addr().String() + "/ws?token=t&platform=test"}
}

// TestAuthMiddlewareRejectsReplacedSession 校验自定义校验器返回「被新登录顶替」时，
// 升级前拒绝仍使用独立错误码 10402（与 10401 区分），保障自定义 TokenValidator 不退化。
// 标准两段式配置下该结论由升级后的 error 帧 + Close 4002 下发，见 TestUpgradeRejectsReplacedSession。
func TestAuthMiddlewareRejectsReplacedSession(t *testing.T) {
	validate := func(c fiber.Ctx, token string) (string, error) {
		return "", ErrSessionReplaced
	}
	resp, status := doHandshake(t, validate, "?token=old")
	if status != fiber.StatusOK {
		t.Fatalf("HTTP 状态码应为 200，实际为 %d", status)
	}
	if resp.Code != httpx.ErrCodeSessionReplaced {
		t.Fatalf("业务错误码应为 %d，实际为 %d", httpx.ErrCodeSessionReplaced, resp.Code)
	}
	if resp.Message != "session replaced by another login" {
		t.Fatalf("提示语应区分「被新登录顶替」，实际为 %q", resp.Message)
	}
}

// TestMapTokenStoreError 校验 TokenStore 错误到握手错误的映射，
// 以及「存储异常返回 nil（失败放行）」这一容错分支。
func TestMapTokenStoreError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{name: "被新登录顶替", err: authx.ErrSessionReplaced, want: ErrSessionReplaced},
		{name: "令牌已撤销或过期", err: authx.ErrTokenNotFound, want: ErrTokenRevoked},
		{name: "存储异常失败放行", err: errors.New("redis unavailable"), want: nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mapTokenStoreError(c.err)
			if c.want == nil {
				if got != nil {
					t.Fatalf("存储异常应返回 nil，实际为 %v", got)
				}
				return
			}
			if !errors.Is(got, c.want) {
				t.Fatalf("映射结果 = %v，期望 %v", got, c.want)
			}
		})
	}
}
