// Package tests 模拟客户端对 GameLogic 服务服务器时间接口的集成测试。
// 测试目标：GET /api/serverTime
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestServerTime
package tests

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
)

// ---- 路径常量 ----

const (
	serverTimePath = "/api/serverTime"
)

// ---- 辅助函数 ----

// doGetServerTime 发送 GET /api/serverTime 请求，返回 HTTP 状态码、响应体和响应头。
// contentType 为空时不设置 Content-Type Header（GET 请求通常无需设置）。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doGetServerTime(t *testing.T, contentType string, requestID string) (int, http.Header, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + serverTimePath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", url, err)
		return 0, nil, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	return resp.StatusCode, resp.Header, body
}

// ---- 服务器时间测试 ----

// TestServerTime_JSON_Success 以 JSON 格式获取服务器时间：
// 不设置 Content-Type，期望返回 HTTP 200、code=0，serverTime 为合理的毫秒时间戳。
func TestServerTime_JSON_Success(t *testing.T) {
	requestID := "test-servertime-json-" + uuid.NewString()[:8]
	httpStatus, headers, body := doGetServerTime(t, "", requestID)

	// 客户端接口必须始终返回 HTTP 200
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}

	// 响应头必须携带 X-Server-Time
	xServerTime := headers.Get("X-Server-Time")
	if xServerTime == "" {
		t.Error("响应头缺少 X-Server-Time")
	}

	// 解析 JSON 响应体
	var resp cspb.ServerTimeResp
	if err := sonic.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析 JSON 响应体失败: %v，原始响应: %s", err, string(body))
	}

	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil")
	}

	// serverTime 必须为毫秒时间戳：在合理范围内（前后 1 分钟）
	now := time.Now().UnixMilli()
	diff := resp.Data.ServerTime - now
	if diff < -60_000 || diff > 60_000 {
		t.Errorf("serverTime = %d 与本地时间偏差超过 1 分钟（本地: %d，差值: %d ms）",
			resp.Data.ServerTime, now, diff)
	}

	t.Logf("JSON 获取服务器时间成功：serverTime=%d，X-Server-Time=%s", resp.Data.ServerTime, xServerTime)
}

// TestServerTime_Proto_Success 以 Protobuf 格式获取服务器时间：
// 设置 Content-Type: application/x-protobuf，期望响应 Content-Type 同为 protobuf，
// 反序列化后 code=0，serverTime 为合理的毫秒时间戳。
func TestServerTime_Proto_Success(t *testing.T) {
	requestID := "test-servertime-proto-" + uuid.NewString()[:8]
	httpStatus, headers, body := doGetServerTime(t, "application/x-protobuf", requestID)

	// 客户端接口必须始终返回 HTTP 200
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}

	// 校验响应 Content-Type 为 protobuf
	ct := headers.Get("Content-Type")
	if ct != "application/x-protobuf" {
		t.Errorf("响应 Content-Type = %q，期望 application/x-protobuf", ct)
	}

	// 响应头必须携带 X-Server-Time
	xServerTime := headers.Get("X-Server-Time")
	if xServerTime == "" {
		t.Error("响应头缺少 X-Server-Time")
	}

	// 反序列化 proto 响应体
	var resp cspb.ServerTimeResp
	if err := proto.Unmarshal(body, &resp); err != nil {
		t.Fatalf("proto 反序列化响应失败: %v", err)
	}

	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil")
	}

	// serverTime 必须为毫秒时间戳：在合理范围内（前后 1 分钟）
	now := time.Now().UnixMilli()
	diff := resp.Data.ServerTime - now
	if diff < -60_000 || diff > 60_000 {
		t.Errorf("serverTime = %d 与本地时间偏差超过 1 分钟（本地: %d，差值: %d ms）",
			resp.Data.ServerTime, now, diff)
	}

	t.Logf("Proto 获取服务器时间成功：serverTime=%d，X-Server-Time=%s", resp.Data.ServerTime, xServerTime)
}

// TestServerTime_NoAuth_Accessible 无 Authorization Header 仍可正常访问：
// 验证此接口不需要鉴权。
func TestServerTime_NoAuth_Accessible(t *testing.T) {
	requestID := "test-servertime-noauth-" + uuid.NewString()[:8]
	httpStatus, _, body := doGetServerTime(t, "", requestID)

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200（接口无需鉴权）", httpStatus)
	}

	var resp cspb.ServerTimeResp
	if err := sonic.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析响应体失败: %v，原始响应: %s", err, string(body))
	}
	if resp.Code != 0 {
		t.Errorf("业务错误码 = %d，期望 0（无需鉴权即可访问）", resp.Code)
	}

	t.Logf("无需鉴权验证通过：serverTime=%d", resp.Data.GetServerTime())
}

// TestServerTime_Milliseconds 验证 serverTime 为毫秒级时间戳（而非秒级）：
// 毫秒时间戳应为 13 位数字，秒级时间戳仅 10 位，通过数量级区分。
func TestServerTime_Milliseconds(t *testing.T) {
	requestID := "test-servertime-ms-" + uuid.NewString()[:8]
	_, _, body := doGetServerTime(t, "", requestID)

	var resp cspb.ServerTimeResp
	if err := sonic.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析响应体失败: %v，原始响应: %s", err, string(body))
	}
	if resp.Code != 0 || resp.Data == nil {
		t.Fatalf("获取时间失败：code=%d", resp.Code)
	}

	// 毫秒时间戳（2001-09-09 以后）最小值为 1_000_000_000_000（13位）
	// 秒级时间戳（2033年前）最大值约为 2_000_000_000（10位）
	const minMillis int64 = 1_000_000_000_000
	if resp.Data.ServerTime < minMillis {
		t.Errorf("serverTime = %d 疑似秒级时间戳（期望毫秒级，应 >= %d）",
			resp.Data.ServerTime, minMillis)
	}

	t.Logf("毫秒时间戳验证通过：serverTime=%d", resp.Data.ServerTime)
}
