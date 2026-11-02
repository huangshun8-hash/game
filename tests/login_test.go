// Package tests 模拟客户端对 GameLogic 服务登录接口的集成测试。
// 测试目标：POST /auth/login
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestLogin_Guest_Success
//	go test ./tests/... -v -run TestLogin_Guest_Proto
//	TEST_BASE_URL=http://192.168.40.107:8081 go test ./tests/... -v -run TestLogin_Guest_Proto
package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
)

// ---- 常量与配置 ----

const (
	// loginPath 登录接口路径
	loginPath = "/auth/login"

	// defaultBaseURL 本地开发默认地址
	defaultBaseURL = "http://localhost:8081"

	// requestTimeout 单个请求超时时间
	requestTimeout = 10 * time.Second
)

// baseURL 返回测试目标基准地址（可通过环境变量覆盖）
func baseURL() string {
	if u := os.Getenv("TEST_BASE_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return defaultBaseURL
}

// ---- 辅助函数 ----

// newTestClientInfo 返回一个模拟 iOS 客户端的 ClientInfo
func newTestClientInfo() *cspb.ClientInfo {
	return &cspb.ClientInfo{
		DeviceId:      "test-device-001",
		DeviceName:    "iPhone 15",
		Brand:         "Apple",
		Model:         "iPhone15,2",
		Os:            "iOS",
		OsVersion:     "17.4",
		Language:      "zh-Hans",
		Country:       "CN",
		Timezone:      "Asia/Shanghai",
		Network:       "WiFi",
		ScreenWidth:   393,
		ScreenHeight:  852,
		ClientVersion: "1.0.0",
		BundleVersion: "100",
		Channel:       "AppStore",
		SdkVersion:    "1.0.0",
		PackageName:   "com.example.game",
		InstallId:     "install-" + uuid.NewString(),
		IsEmulator:    false,
	}
}

// newTestClientInfoWithDevice 返回指定设备标识的客户端信息（用于模拟多端登录）
func newTestClientInfoWithDevice(deviceID string) *cspb.ClientInfo {
	ci := newTestClientInfo()
	ci.DeviceId = deviceID
	return ci
}

// doLogin 向服务端发送登录请求，返回解析后的响应和 HTTP 状态码。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doLogin(t *testing.T, req *cspb.LoginReq, requestID string) (int, *cspb.LoginResp) {
	t.Helper()

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + loginPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", url, err)
		return 0, nil
	}
	defer resp.Body.Close()

	var result cspb.LoginResp
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("解析响应体失败: %v", err)
	}
	return resp.StatusCode, &result
}

// doLoginProto 向服务端发送 protobuf 格式的登录请求，返回解析后的响应和 HTTP 状态码。
// 请求体使用 proto.Marshal 序列化，响应体使用 proto.Unmarshal 反序列化。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doLoginProto(t *testing.T, req *cspb.LoginReq, requestID string) (int, *cspb.LoginResp) {
	t.Helper()

	bodyBytes, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("proto 序列化请求体失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + loginPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	// 声明请求体格式为 protobuf，同时告知服务端期望以 protobuf 格式返回响应
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", url, err)
		return 0, nil
	}
	defer resp.Body.Close()

	// 校验响应的 Content-Type 是否为 protobuf
	ct := resp.Header.Get("Content-Type")
	if ct != "application/x-protobuf" {
		t.Errorf("响应 Content-Type = %q，期望 application/x-protobuf", ct)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}

	var result cspb.LoginResp
	if err := proto.Unmarshal(respBytes, &result); err != nil {
		t.Fatalf("proto 反序列化响应失败: %v", err)
	}
	return resp.StatusCode, &result
}

// ---- 登录测试 ----

// TestLogin_Guest_Success 游客登录成功：携带合法 UUID，期望返回 code=0 并含 token/playerId
func TestLogin_Guest_Success(t *testing.T) {
	req := &cspb.LoginReq{
		LoginType:  "guest",
		Platform:   "ios",
		Uuid:       uuid.NewString(),
		ClientInfo: newTestClientInfo(),
	}

	httpStatus, resp := doLogin(t, req, fmt.Sprintf("test-guest-%s", uuid.NewString()[:8]))

	// 客户端接口必须始终返回 HTTP 200
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil，期望包含登录数据")
	}
	if resp.Data.Token == "" {
		t.Error("token 为空")
	}
	if resp.Data.PlayerId == 0 {
		t.Error("playerId 为 0")
	}
	if resp.Data.ServerId != 1 {
		t.Errorf("serverId = %d，期望新玩家默认 1 服", resp.Data.ServerId)
	}
	if len(resp.Data.SaveKey) == 0 {
		t.Error("saveKey 为空，期望登录时下发存档加密秘钥")
	}
	if len(resp.Data.SaveKey) != 32 {
		t.Errorf("saveKey 长度 = %d 字节，期望 32（原始 AES 密钥二进制）", len(resp.Data.SaveKey))
	}
	t.Logf("游客登录成功：playerId=%d, isNew=%v, saveKey 前12字节=%q, token 前20字符=%q",
		resp.Data.PlayerId, resp.Data.IsNew, resp.Data.SaveKey[:min(12, len(resp.Data.SaveKey))], resp.Data.Token[:min(20, len(resp.Data.Token))])
}

// TestLogin_Guest_Proto 游客登录（Protobuf 传输）：
// 请求体以 proto.Marshal 二进制格式发送
// 验证服务端正确返回 Content-Type: application/x-protobuf 且响应可正常反序列化。
func TestLogin_Guest_Proto(t *testing.T) {
	req := &cspb.LoginReq{
		LoginType:  "guest",
		Platform:   "android",
		Uuid:       uuid.NewString(),
		ClientInfo: newTestClientInfo(),
	}

	httpStatus, resp := doLoginProto(t, req, fmt.Sprintf("test-proto-%s", uuid.NewString()[:8]))

	// 客户端接口必须始终返回 HTTP 200
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil，期望包含登录数据")
	}
	if resp.Data.Token == "" {
		t.Error("token 为空")
	}
	if resp.Data.PlayerId == 0 {
		t.Error("playerId 为 0")
	}
	if resp.Data.ServerId != 1 {
		t.Errorf("serverId = %d，期望新玩家默认 1 服", resp.Data.ServerId)
	}
	if len(resp.Data.SaveKey) == 0 {
		t.Error("saveKey 为空，期望登录时下发存档加密秘钥")
	}
	if len(resp.Data.SaveKey) != 32 {
		t.Errorf("saveKey 长度 = %d 字节，期望 32（原始 AES 密钥二进制）", len(resp.Data.SaveKey))
	}
	t.Logf("游客登录(proto)成功：playerId=%d, isNew=%v, saveKey 前12字节=%q, token 前20字符=%q",
		resp.Data.PlayerId, resp.Data.IsNew, resp.Data.SaveKey[:min(12, len(resp.Data.SaveKey))], resp.Data.Token[:min(20, len(resp.Data.Token))])
}

// TestLogin_InvalidRequests 验证登录入口在调用平台服务前拒绝无效客户端参数。
func TestLogin_InvalidRequests(t *testing.T) {
	testCases := []struct {
		name string
		req  *cspb.LoginReq
	}{
		{
			name: "guestInvalidUUID",
			req: &cspb.LoginReq{
				LoginType: "guest",
				Platform:  "ios",
				Uuid:      "invalid uuid",
			},
		},
		{
			name: "unsupportedLoginType",
			req: &cspb.LoginReq{
				LoginType: "unsupported",
				Platform:  "ios",
			},
		},
		{
			name: "missingPlatform",
			req: &cspb.LoginReq{
				LoginType: "guest",
				Uuid:      uuid.NewString(),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			httpStatus, resp := doLogin(t, testCase.req, fmt.Sprintf("login-invalid-%s", testCase.name))
			if httpStatus != http.StatusOK {
				t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
			}
			if resp.Code == 0 {
				t.Fatalf("无效登录请求意外成功：response=%+v", resp)
			}
			if resp.Data != nil {
				t.Errorf("失败响应不应包含 data：%+v", resp.Data)
			}
		})
	}
}

// loginErrCodeBadRequest 登录入口参数错误码（gamelogic model.ErrCodeBadRequest）。
// tests 为外部包，无法引用 services/gamelogic/internal/model，故在此显式声明。
const loginErrCodeBadRequest = 2400

// TestLogin_RequiresDeviceId 校验严格单端的前提：登录必须上报 clientInfo.deviceId。
// 缺失时无法判定是否换端，若不拒绝会陷入「静默踢人」或「单端策略被绕过」，故入口直接报错。
func TestLogin_RequiresDeviceId(t *testing.T) {
	cases := []struct {
		name       string
		clientInfo *cspb.ClientInfo
	}{
		{name: "缺少clientInfo", clientInfo: nil},
		{name: "deviceId为空", clientInfo: &cspb.ClientInfo{DeviceName: "iPhone"}},
		{name: "deviceId为空白", clientInfo: &cspb.ClientInfo{DeviceId: "   "}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &cspb.LoginReq{
				LoginType:  "guest",
				Platform:   "ios",
				Uuid:       uuid.NewString(),
				ClientInfo: c.clientInfo,
			}
			_, resp := doLogin(t, req, fmt.Sprintf("login-device-%s", uuid.NewString()[:8]))
			if resp.Code != loginErrCodeBadRequest {
				t.Fatalf("业务错误码 = %d，期望 %d", resp.Code, loginErrCodeBadRequest)
			}
			if resp.Message != "deviceId is required" {
				t.Fatalf("提示语 = %q，期望 deviceId is required", resp.Message)
			}
		})
	}
}
