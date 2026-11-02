// Package tests 模拟客户端对 GameLogic 服务自定义配置接口的集成测试。
// 测试目标：GET /api/customConfig
//
//	该接口无需 JWT 鉴权，返回统一 JSON 信封 {code, message, data}，
//	data 为服务端缓存中的配置对象；Redis 读取失败时降级返回空对象 {}。
//
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 本文件只做正向流程验证：确认接口可达且业务成功（code=0）。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestCustomConfig
package tests

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
)

// 自定义配置接口路径常量
const customConfigPath = "/api/customConfig"

// customConfigResp 自定义配置接口响应信封结构
type customConfigResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// ---- 辅助函数 ----

// doCustomConfigGet 发送自定义配置查询请求（无需鉴权），返回 HTTP 状态码和解析后的响应。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doCustomConfigGet(t *testing.T, requestID string) (int, *customConfigResp) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	reqURL := baseURL() + customConfigPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", reqURL, err)
		return 0, nil
	}
	defer resp.Body.Close()

	// 校验响应 Content-Type 为 JSON，允许携带标准 charset 参数
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("响应 Content-Type = %q，期望 application/json", ct)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	var result customConfigResp
	if err := sonic.ConfigDefault.Unmarshal(respBytes, &result); err != nil {
		t.Fatalf("解析自定义配置 JSON 响应体失败: %v", err)
	}
	return resp.StatusCode, &result
}

// ---- 自定义配置测试 ----

// TestCustomConfig_Get_Success 自定义配置查询正向流程：
// 无需携带 Token 请求，期望返回 HTTP 200 且 code=0；data 可为配置对象或为空。
func TestCustomConfig_Get_Success(t *testing.T) {
	httpStatus, resp := doCustomConfigGet(t, fmt.Sprintf("custom-config-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	t.Logf("自定义配置查询成功：code=%d, message=%q, data=%v", resp.Code, resp.Message, resp.Data)
}
