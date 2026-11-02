package httpcli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

// --- 配置默认值测试 ---

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Timeout != 30*time.Second {
		t.Errorf("DefaultConfig Timeout = %v, want 30s", cfg.Timeout)
	}
	if cfg.UserAgent != "httpcli" {
		t.Errorf("DefaultConfig UserAgent = %q, want %q", cfg.UserAgent, "httpcli")
	}
	if cfg.BaseURL != "" {
		t.Errorf("DefaultConfig BaseURL = %q, want empty", cfg.BaseURL)
	}
	if cfg.Retry != nil {
		t.Error("DefaultConfig Retry should be nil")
	}
	if cfg.Headers != nil {
		t.Error("DefaultConfig Headers should be nil")
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.InitialInterval != time.Second {
		t.Errorf("InitialInterval = %v, want 1s", cfg.InitialInterval)
	}
	if cfg.MaxBackoffTime != 32*time.Second {
		t.Errorf("MaxBackoffTime = %v, want 32s", cfg.MaxBackoffTime)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("Multiplier = %f, want 2.0", cfg.Multiplier)
	}
	if cfg.MaxRetryCount != 3 {
		t.Errorf("MaxRetryCount = %d, want 3", cfg.MaxRetryCount)
	}
}

// --- NewClient 测试 ---

func TestNewClient_NilConfig(t *testing.T) {
	c := NewClient(nil)
	if c == nil {
		t.Fatal("NewClient(nil) returned nil")
	}
	if c.Raw() == nil {
		t.Fatal("Raw() returned nil")
	}
	if c.cfg.UserAgent != "httpcli" {
		t.Errorf("cfg.UserAgent = %q, want %q", c.cfg.UserAgent, "httpcli")
	}
}

func TestNewClient_CustomConfig(t *testing.T) {
	cfg := &Config{
		BaseURL:   "https://api.example.com",
		Timeout:   10 * time.Second,
		UserAgent: "test-agent",
		Headers: map[string]string{
			"X-Custom": "value1",
			"X-Token":  "abc",
		},
	}
	c := NewClient(cfg)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.cfg.BaseURL != cfg.BaseURL {
		t.Errorf("cfg.BaseURL = %q, want %q", c.cfg.BaseURL, cfg.BaseURL)
	}
}

func TestNewClient_WithRetry(t *testing.T) {
	retryCfg := DefaultRetryConfig()
	c := NewClient(&Config{Retry: retryCfg})
	if c == nil {
		t.Fatal("NewClient with retry returned nil")
	}
	if c.cfg.Retry == nil {
		t.Fatal("cfg.Retry should not be nil")
	}
	if c.cfg.Retry.MaxRetryCount != 3 {
		t.Errorf("cfg.Retry.MaxRetryCount = %d, want 3", c.cfg.Retry.MaxRetryCount)
	}
}

func TestClient_Close(t *testing.T) {
	c := NewClient(nil)
	// Close 不应 panic
	c.Close()
}

// --- Option 函数测试 ---

func TestWithHeader(t *testing.T) {
	cfg := applyOptions(WithHeader("X-Key", "val"))
	if cfg.Header == nil {
		t.Fatal("Header map is nil")
	}
	if cfg.Header["X-Key"] != "val" {
		t.Errorf("Header[X-Key] = %q, want %q", cfg.Header["X-Key"], "val")
	}
}

func TestWithHeader_Multiple(t *testing.T) {
	cfg := applyOptions(
		WithHeader("X-A", "1"),
		WithHeader("X-B", "2"),
	)
	if len(cfg.Header) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(cfg.Header))
	}
	if cfg.Header["X-A"] != "1" || cfg.Header["X-B"] != "2" {
		t.Errorf("unexpected headers: %v", cfg.Header)
	}
}

func TestWithHeaders(t *testing.T) {
	headers := map[string]string{"X-A": "1", "X-B": "2"}
	cfg := applyOptions(WithHeaders(headers))
	if len(cfg.Header) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(cfg.Header))
	}
}

func TestWithHeaders_CombinedWithSingle(t *testing.T) {
	cfg := applyOptions(
		WithHeader("X-A", "1"),
		WithHeaders(map[string]string{"X-B": "2", "X-C": "3"}),
	)
	if len(cfg.Header) != 3 {
		t.Fatalf("expected 3 headers, got %d", len(cfg.Header))
	}
}

func TestWithParam(t *testing.T) {
	cfg := applyOptions(WithParam("key", "val"))
	if cfg.Param == nil {
		t.Fatal("Param map is nil")
	}
	if cfg.Param["key"] != "val" {
		t.Errorf("Param[key] = %q, want %q", cfg.Param["key"], "val")
	}
}

func TestWithParam_Multiple(t *testing.T) {
	cfg := applyOptions(
		WithParam("a", "1"),
		WithParam("b", "2"),
	)
	if len(cfg.Param) != 2 {
		t.Fatalf("expected 2 params, got %d", len(cfg.Param))
	}
}

func TestWithParams(t *testing.T) {
	params := map[string]string{"a": "1", "b": "2"}
	cfg := applyOptions(WithParams(params))
	if len(cfg.Param) != 2 {
		t.Fatalf("expected 2 params, got %d", len(cfg.Param))
	}
}

func TestWithCookie(t *testing.T) {
	cfg := applyOptions(WithCookie("session", "abc123"))
	if cfg.Cookie == nil {
		t.Fatal("Cookie map is nil")
	}
	if cfg.Cookie["session"] != "abc123" {
		t.Errorf("Cookie[session] = %q, want %q", cfg.Cookie["session"], "abc123")
	}
}

func TestWithCookies(t *testing.T) {
	cookies := map[string]string{"session": "abc", "lang": "zh"}
	cfg := applyOptions(WithCookies(cookies))
	if len(cfg.Cookie) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cfg.Cookie))
	}
}

func TestWithTimeout(t *testing.T) {
	cfg := applyOptions(WithTimeout(5 * time.Second))
	if cfg.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", cfg.Timeout)
	}
}

func TestWithUserAgent(t *testing.T) {
	cfg := applyOptions(WithUserAgent("custom-agent"))
	if cfg.UserAgent != "custom-agent" {
		t.Errorf("UserAgent = %q, want %q", cfg.UserAgent, "custom-agent")
	}
}

func TestWithMaxRedirects(t *testing.T) {
	cfg := applyOptions(WithMaxRedirects(5))
	if cfg.MaxRedirects != 5 {
		t.Errorf("MaxRedirects = %d, want 5", cfg.MaxRedirects)
	}
}

func TestApplyOptions_Empty(t *testing.T) {
	cfg := applyOptions()
	if cfg.Header != nil || cfg.Param != nil || cfg.Cookie != nil {
		t.Error("empty options should not initialize maps")
	}
	if cfg.Timeout != 0 {
		t.Errorf("empty Timeout = %v, want 0", cfg.Timeout)
	}
	if cfg.UserAgent != "" {
		t.Errorf("empty UserAgent = %q, want empty", cfg.UserAgent)
	}
}

func TestApplyOptions_AllOptions(t *testing.T) {
	cfg := applyOptions(
		WithHeader("X-H", "1"),
		WithParam("p", "2"),
		WithCookie("c", "3"),
		WithTimeout(10*time.Second),
		WithUserAgent("ua"),
		WithMaxRedirects(3),
	)
	if cfg.Header["X-H"] != "1" {
		t.Errorf("Header = %v", cfg.Header)
	}
	if cfg.Param["p"] != "2" {
		t.Errorf("Param = %v", cfg.Param)
	}
	if cfg.Cookie["c"] != "3" {
		t.Errorf("Cookie = %v", cfg.Cookie)
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if cfg.UserAgent != "ua" {
		t.Errorf("UserAgent = %q", cfg.UserAgent)
	}
	if cfg.MaxRedirects != 3 {
		t.Errorf("MaxRedirects = %d", cfg.MaxRedirects)
	}
}

// --- HTTP 请求集成测试（使用 httptest.Server）---

// startTestServer 创建一个测试 HTTP 服务器，根据请求方法返回对应的状态码和响应体。
func startTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 回显请求头到响应
		if ua := r.Header.Get("User-Agent"); ua != "" {
			w.Header().Set("X-Echo-UserAgent", ua)
		}
		if h := r.Header.Get("X-Custom"); h != "" {
			w.Header().Set("X-Echo-Custom", h)
		}
		if q := r.URL.Query().Get("key"); q != "" {
			w.Header().Set("X-Echo-Param", q)
		}

		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message":"get ok"}`))

		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var reqData map[string]any
			if len(body) > 0 {
				_ = sonic.Unmarshal(body, &reqData)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			resp := map[string]any{
				"method": "POST",
				"data":   reqData,
			}
			_, _ = w.Write(func() []byte {
				b, _ := sonic.Marshal(resp)
				return b
			}())

		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message":"put ok"}`))

		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message":"delete ok"}`))

		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message":"patch ok"}`))

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func TestClient_Get(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Get(ctx, ts.URL+"/test")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode(), http.StatusOK)
	}

	var data map[string]string
	if err := resp.JSON(&data); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if data["message"] != "get ok" {
		t.Errorf("JSON message = %q, want %q", data["message"], "get ok")
	}
}

func TestClient_Get_WithOptions(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Get(ctx, ts.URL+"/test",
		WithHeader("X-Custom", "test-value"),
		WithParam("key", "hello"),
		WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("Get with options failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode(), http.StatusOK)
	}
	if resp.Header("X-Echo-Custom") != "test-value" {
		t.Errorf("echo X-Custom header = %q, want %q", resp.Header("X-Echo-Custom"), "test-value")
	}
	if resp.Header("X-Echo-Param") != "hello" {
		t.Errorf("echo param = %q, want %q", resp.Header("X-Echo-Param"), "hello")
	}
}

func TestClient_Post(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	body := map[string]string{"name": "alice", "role": "admin"}
	resp, err := c.Post(ctx, ts.URL+"/users", body)
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode() != http.StatusCreated {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode(), http.StatusCreated)
	}

	var data map[string]any
	if err := resp.JSON(&data); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if data["method"] != "POST" {
		t.Errorf("method = %v, want POST", data["method"])
	}
}

func TestClient_Post_NilBody(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Post(ctx, ts.URL+"/users", nil)
	if err != nil {
		t.Fatalf("Post with nil body failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode() != http.StatusCreated {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode(), http.StatusCreated)
	}
}

func TestClient_Post_BytesBody(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Post(ctx, ts.URL+"/data", []byte(`raw bytes`))
	if err != nil {
		t.Fatalf("Post with bytes body failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode() != http.StatusCreated {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode(), http.StatusCreated)
	}
}

func TestClient_Put(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Put(ctx, ts.URL+"/users/1", map[string]string{"name": "bob"})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode(), http.StatusOK)
	}
}

func TestClient_Delete(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Delete(ctx, ts.URL+"/users/1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode(), http.StatusOK)
	}
}

func TestClient_Patch(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Patch(ctx, ts.URL+"/users/1", map[string]string{"name": "carol"})
	if err != nil {
		t.Fatalf("Patch failed: %v", err)
	}
	defer resp.Close()

	if resp.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode(), http.StatusOK)
	}
}

func TestClient_Get_InvalidURL(t *testing.T) {
	c := NewClient(&Config{Timeout: 1 * time.Second})
	defer c.Close()

	ctx := context.Background()
	_, err := c.Get(ctx, "http://127.0.0.1:1/nonexistent")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

// --- Response 测试 ---

func TestResponse_String(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Get(ctx, ts.URL+"/test")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Close()

	s := resp.String()
	if s != `{"message":"get ok"}` {
		t.Errorf("String() = %q, want %q", s, `{"message":"get ok"}`)
	}
}

func TestResponse_Body(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Get(ctx, ts.URL+"/test")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Close()

	body := resp.Body()
	if len(body) == 0 {
		t.Fatal("Body() returned empty slice")
	}
	if string(body) != `{"message":"get ok"}` {
		t.Errorf("Body() = %q", string(body))
	}
}

func TestResponse_Raw(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Get(ctx, ts.URL+"/test")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Close()

	if resp.Raw() == nil {
		t.Fatal("Raw() returned nil")
	}
}

func TestResponse_IsStreaming(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Get(ctx, ts.URL+"/test")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Close()

	if resp.IsStreaming() {
		t.Error("expected IsStreaming() to be false for regular response")
	}
}

func TestResponse_BodyStream(t *testing.T) {
	ts := startTestServer(t)
	defer ts.Close()

	c := NewClient(nil)
	defer c.Close()

	ctx := context.Background()
	resp, err := c.Get(ctx, ts.URL+"/test")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Close()

	reader := resp.BodyStream()
	if reader == nil {
		t.Fatal("BodyStream() returned nil")
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading stream failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("BodyStream returned empty data")
	}
}
