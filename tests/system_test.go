// Package tests 模拟客户端对 GameLogic 服务系统接口的集成测试。
// 测试目标：GET /health、GET /version。
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestSystem
package tests

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
)

const (
	healthPath   = "/health"
	versionPath  = "/version"
	minUnixMilli = int64(1_000_000_000_000)
)

type healthStatusResp struct {
	Status string `json:"status"`
	Time   int64  `json:"time"`
}

type versionStatusResp struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Time    int64  `json:"time"`
}

// doSystemGet 发送系统接口 GET 请求并返回 HTTP 状态码和原始响应体。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doSystemGet(t *testing.T, path string, requestID string) (int, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}

	resp, err := (&http.Client{Timeout: requestTimeout}).Do(req)
	if err != nil {
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", url, err)
		return 0, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取系统接口响应体失败: %v", err)
	}
	return resp.StatusCode, body
}

// TestSystem_Health 验证健康检查接口返回服务状态和毫秒级时间戳。
func TestSystem_Health(t *testing.T) {
	httpStatus, body := doSystemGet(t, healthPath, "system-health")
	if httpStatus != http.StatusOK {
		t.Fatalf("HTTP 状态码 = %d，期望 200", httpStatus)
	}

	var resp healthStatusResp
	if err := sonic.Unmarshal(body, &resp); err != nil {
		t.Fatalf("反序列化健康检查响应失败: %v，响应体=%q", err, body)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q，期望 %q", resp.Status, "ok")
	}
	if resp.Time < minUnixMilli {
		t.Errorf("time = %d，期望 Unix 毫秒时间戳", resp.Time)
	}
}

// TestSystem_Version 验证版本接口返回当前服务、非空版本和毫秒级时间戳。
func TestSystem_Version(t *testing.T) {
	httpStatus, body := doSystemGet(t, versionPath, "system-version")
	if httpStatus != http.StatusOK {
		t.Fatalf("HTTP 状态码 = %d，期望 200", httpStatus)
	}

	var resp versionStatusResp
	if err := sonic.Unmarshal(body, &resp); err != nil {
		t.Fatalf("反序列化版本响应失败: %v，响应体=%q", err, body)
	}
	if resp.Service != "GameLogic" {
		t.Errorf("service = %q，期望 %q", resp.Service, "GameLogic")
	}
	if resp.Version == "" {
		t.Error("version 为空")
	}
	if resp.Time < minUnixMilli {
		t.Errorf("time = %d，期望 Unix 毫秒时间戳", resp.Time)
	}
}
