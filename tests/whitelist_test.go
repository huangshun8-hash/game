// Package tests 模拟 Web 管理后台 + 客户端对充值白名单的集成测试。
// 测试目标：
//
//	POST /api/gmt/whitelist/add     添加白名单（account 或 playerId）
//	POST /api/gmt/whitelist/delete  删除白名单（物理删除）
//	GET  /api/gmt/whitelist/list    分页查询白名单
//	（配合客户端下单验证白名单自动支付）
//
// 相关服务：
//   - GMT 基准 URL 通过环境变量 TEST_GMT_BASE_URL 指定，默认 http://localhost:8082；
//   - GMT 管理员凭据通过 GMT_ADMIN_USERNAME / GMT_ADMIN_PASSWORD 指定（必须为已引导的超管账号）；
//   - GameLogic 基准 URL 通过 TEST_BASE_URL 指定，默认 http://localhost:8081。
//
// 运行方式（需 GMT / GameLogic / Platform 均已启动且已引导超管）：
//
//	go test ./tests/... -v -run TestWhitelist
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"kemiluo/pb/cspb"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
)

// ---- 路径常量 ----

const (
	// whitelistAddPath 添加白名单接口路径
	whitelistAddPath = "/api/gmt/whitelist/add"
	// whitelistDeletePath 删除白名单接口路径
	whitelistDeletePath = "/api/gmt/whitelist/delete"
	// whitelistListPath 查询白名单接口路径
	whitelistListPath = "/api/gmt/whitelist/list"
	// defaultGmtBaseURL GMT 本地开发默认地址
	defaultGmtBaseURL = "http://localhost:8082"
)

// gmtdtoResp GMT 接口统一响应信封（与 httpx.Response 结构对应）
type gmtdtoResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// gmtBaseURL 返回 GMT 服务基准地址
func gmtBaseURL() string {
	if u := os.Getenv("TEST_GMT_BASE_URL"); u != "" {
		return u
	}
	return defaultGmtBaseURL
}

// loginAsGMAdmin 管理员登录 GMT 获取访问令牌。
// 凭据来自环境变量 GMT_ADMIN_USERNAME / GMT_ADMIN_PASSWORD；
// 未配置密码时跳过测试（需先以 GMT_BOOTSTRAP_ADMIN_* 引导超管）。
func loginAsGMAdmin(t *testing.T) string {
	t.Helper()

	username := os.Getenv("GMT_ADMIN_USERNAME")
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("GMT_ADMIN_PASSWORD")
	if password == "" {
		t.Skipf("跳过测试：未设置 GMT_ADMIN_PASSWORD，无法登录 GMT 管理后台")
		return ""
	}

	bodyBytes, err := sonic.Marshal(map[string]any{"username": username, "password": password})
	if err != nil {
		t.Fatalf("序列化登录请求失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := gmtBaseURL() + "/api/admin/login"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建登录请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Request-Id", fmt.Sprintf("gmt-login-%s", uuid.NewString()[:8]))

	resp, err := (&http.Client{Timeout: requestTimeout}).Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到 GMT（%v），请先启动 gmt 服务", err)
		return ""
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取登录响应失败: %v", err)
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := sonic.Unmarshal(respBody, &envelope); err != nil {
		t.Fatalf("解析登录响应失败: %v，响应体=%q", err, respBody)
	}
	if envelope.Code != 0 {
		t.Fatalf("GMT 管理员登录失败：code=%d，message=%q", envelope.Code, string(respBody))
	}
	if envelope.Data.Token == "" {
		t.Fatal("登录响应缺少 token")
	}
	return envelope.Data.Token
}

// doWhitelistJSON 向 GMT 白名单接口发送 JSON 请求（POST），返回响应信封。
// 连接失败时调用 t.Skipf 跳过测试。
func doWhitelistJSON(t *testing.T, path, token string, body map[string]any, requestID string) *gmtdtoResp {
	t.Helper()

	bodyBytes, err := sonic.Marshal(body)
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := gmtBaseURL() + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}

	resp, err := (&http.Client{Timeout: requestTimeout}).Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到 GMT %s（%v），请先启动 gmt 服务", url, err)
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	var envelope gmtdtoResp
	if err := sonic.Unmarshal(respBody, &envelope); err != nil {
		t.Fatalf("解析响应失败: %v，响应体=%q", err, respBody)
	}
	return &envelope
}

// doWhitelistQuery 向 GMT 白名单接口发送查询请求（GET），返回响应信封。
// 连接失败时调用 t.Skipf 跳过测试。
func doWhitelistQuery(t *testing.T, path, token string, params map[string]string, requestID string) *gmtdtoResp {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	endpoint, _ := url.Parse(gmtBaseURL() + path)
	q := endpoint.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	endpoint.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}

	resp, err := (&http.Client{Timeout: requestTimeout}).Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到 GMT %s（%v），请先启动 gmt 服务", endpoint.String(), err)
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	var envelope gmtdtoResp
	if err := sonic.Unmarshal(respBody, &envelope); err != nil {
		t.Fatalf("解析响应失败: %v，响应体=%q", err, respBody)
	}
	return &envelope
}

// whitelistQueryContainsOpenID 分页遍历白名单全量列表，返回 openID 是否出现在列表中。
// 查询接口无需过滤条件，故逐页扫描以判定目标 openId 是否在列表内。
func whitelistQueryContainsOpenID(t *testing.T, adminToken, openID string) (bool, error) {
	t.Helper()
	const pageSize = 100
	page := int64(1)
	for {
		listEnv := doWhitelistQuery(t, whitelistListPath, adminToken,
			map[string]string{"page": fmt.Sprintf("%d", page), "pageSize": fmt.Sprintf("%d", pageSize)},
			fmt.Sprintf("wl-list-%s", uuid.NewString()[:8]))
		if listEnv == nil {
			return false, fmt.Errorf("查询白名单失败：无法连接或空响应")
		}
		if listEnv.Code != 0 {
			return false, fmt.Errorf("查询白名单失败：code=%d message=%q", listEnv.Code, listEnv.Message)
		}
		var listData struct {
			Items []struct {
				OpenID string `json:"openId"`
			} `json:"items"`
			TotalPages int64 `json:"totalPages"`
		}
		if err := sonic.Unmarshal(listEnv.Data, &listData); err != nil {
			return false, fmt.Errorf("解析查询响应失败: %w", err)
		}
		for _, it := range listData.Items {
			if it.OpenID == openID {
				return true, nil
			}
		}
		if listData.TotalPages <= page {
			return false, nil
		}
		page++
	}
}

// TestWhitelist_Add_ByAccount 按 account(openId) 添加白名单成功，且再查列表命中。
func TestWhitelist_Add_ByAccount(t *testing.T) {
	adminToken := loginAsGMAdmin(t)
	account := "whitelist_guest_" + uuid.NewString()[:8]

	env := doWhitelistJSON(t, whitelistAddPath, adminToken,
		map[string]any{"account": account}, fmt.Sprintf("wl-add-acc-%s", uuid.NewString()[:8]))
	if env.Code != 0 {
		t.Fatalf("按 account 添加失败：code=%d, message=%q", env.Code, env.Message)
	}
	var addData struct {
		OpenID        string `json:"openId"`
		AlreadyExists bool   `json:"alreadyExists"`
		CreatedAt     int64  `json:"createdAt"`
	}
	if err := sonic.Unmarshal(env.Data, &addData); err != nil {
		t.Fatalf("解析添加响应失败: %v", err)
	}
	if addData.OpenID != account {
		t.Errorf("返回 openId = %q，期望 %q", addData.OpenID, account)
	}
	if addData.AlreadyExists {
		t.Error("新账号不应标记为 alreadyExists")
	}
	if addData.CreatedAt <= 0 {
		t.Error("createdAt 应为正数")
	}

	// 列表（无条件分页）应包含新增账号
	contains, err := whitelistQueryContainsOpenID(t, adminToken, account)
	if err != nil {
		t.Fatalf("查询白名单失败: %v", err)
	}
	if !contains {
		t.Errorf("列表中应包含新添加的账号 %q", account)
	}

	// 清理：删除该白名单，避免污染后续测试
	_ = doWhitelistJSON(t, whitelistDeletePath, adminToken,
		map[string]any{"openId": account}, fmt.Sprintf("wl-del-acc-%s", uuid.NewString()[:8]))
}

// TestWhitelist_Add_ByPlayerId 按现存玩家 playerId 添加白名单成功并在列表按 playerId 命中。
func TestWhitelist_Add_ByPlayerId(t *testing.T) {
	adminToken := loginAsGMAdmin(t)
	// 登录游客创建玩家，保证 playerId 为现存账号
	_, playerID := loginAsGuest(t)

	env := doWhitelistJSON(t, whitelistAddPath, adminToken,
		map[string]any{"playerId": playerID}, fmt.Sprintf("wl-add-pid-%s", uuid.NewString()[:8]))
	if env.Code != 0 {
		t.Fatalf("按 playerId 添加失败：code=%d, message=%q", env.Code, env.Message)
	}
	var addData struct {
		OpenID string `json:"openId"`
	}
	if err := sonic.Unmarshal(env.Data, &addData); err != nil {
		t.Fatalf("解析添加响应失败: %v", err)
	}
	if addData.OpenID == "" {
		t.Error("按 playerId 添加应返回解析出的 openId")
	}

	// 列表（无条件分页）应包含按 playerId 解析出的 openId
	contains, err := whitelistQueryContainsOpenID(t, adminToken, addData.OpenID)
	if err != nil {
		t.Fatalf("查询白名单失败: %v", err)
	}
	if !contains {
		t.Errorf("列表中应包含账号 %q", addData.OpenID)
	}

	// 清理：删除该白名单（删除仅按账号 openId）
	_ = doWhitelistJSON(t, whitelistDeletePath, adminToken,
		map[string]any{"openId": addData.OpenID}, fmt.Sprintf("wl-del-pid-%s", uuid.NewString()[:8]))
}

// TestWhitelist_Add_ByNonExistentPlayerId 按不存在或已注销的 playerId 添加应被拒绝（只校验现存账号）。
func TestWhitelist_Add_ByNonExistentPlayerId(t *testing.T) {
	adminToken := loginAsGMAdmin(t)

	env := doWhitelistJSON(t, whitelistAddPath, adminToken,
		map[string]any{"playerId": 999999999}, fmt.Sprintf("wl-add-none-%s", uuid.NewString()[:8]))
	if env.Code == 0 {
		t.Fatal("不存在的 playerId 添加白名单不应成功")
	}
	t.Logf("不存在 playerId 拒绝校验通过：code=%d, message=%q", env.Code, env.Message)
}

// TestWhitelist_Add_NoIdentity 未提供 account 或 playerId 时返回参数错误。
func TestWhitelist_Add_NoIdentity(t *testing.T) {
	adminToken := loginAsGMAdmin(t)

	env := doWhitelistJSON(t, whitelistAddPath, adminToken,
		map[string]any{}, fmt.Sprintf("wl-add-noid-%s", uuid.NewString()[:8]))
	if env.Code == 0 {
		t.Fatal("未提供身份凭证时不应成功")
	}
	t.Logf("缺少身份凭证校验通过：code=%d, message=%q", env.Code, env.Message)
}

// TestWhitelist_Delete 删除白名单（物理删除）；删除后列表不再命中，重复删除仍幂等成功。
func TestWhitelist_Delete(t *testing.T) {
	adminToken := loginAsGMAdmin(t)
	account := "whitelist_del_" + uuid.NewString()[:8]

	if env := doWhitelistJSON(t, whitelistAddPath, adminToken,
		map[string]any{"account": account}, fmt.Sprintf("wl-add-del-%s", uuid.NewString()[:8])); env.Code != 0 {
		t.Fatalf("前置添加失败：code=%d", env.Code)
	}

	if env := doWhitelistJSON(t, whitelistDeletePath, adminToken,
		map[string]any{"openId": account}, fmt.Sprintf("wl-del-%s", uuid.NewString()[:8])); env.Code != 0 {
		t.Fatalf("删除失败：code=%d, message=%q", env.Code, env.Message)
	}

	// 删除后列表不再包含该账号
	contains, err := whitelistQueryContainsOpenID(t, adminToken, account)
	if err != nil {
		t.Fatalf("查询白名单失败: %v", err)
	}
	if contains {
		t.Errorf("删除后列表中不应再包含账号 %q", account)
	}

	// 重复删除幂等成功
	if env := doWhitelistJSON(t, whitelistDeletePath, adminToken,
		map[string]any{"openId": account}, fmt.Sprintf("wl-del2-%s", uuid.NewString()[:8])); env.Code != 0 {
		t.Fatalf("重复删除应幂等成功：code=%d, message=%q", env.Code, env.Message)
	}
}

// TestWhitelist_AutoPay_MakesOrderPaid 白名单自动支付：账号加入白名单后下单，订单应直接变为已支付（PAID）。
func TestWhitelist_AutoPay_MakesOrderPaid(t *testing.T) {
	adminToken := loginAsGMAdmin(t)

	// 游客登录创建玩家
	token, playerID := loginAsGuest(t)

	// 将该玩家加入白名单，记录解析出的 openId 供删除（删除仅按账号 openId）
	addEnv := doWhitelistJSON(t, whitelistAddPath, adminToken,
		map[string]any{"playerId": playerID}, fmt.Sprintf("wl-autopay-add-%s", uuid.NewString()[:8]))
	if addEnv.Code != 0 {
		t.Fatalf("前置添加白名单失败：code=%d, message=%q", addEnv.Code, addEnv.Message)
	}
	var addData struct {
		OpenID string `json:"openId"`
	}
	if err := sonic.Unmarshal(addEnv.Data, &addData); err != nil {
		t.Fatalf("解析添加响应失败: %v", err)
	}
	if addData.OpenID == "" {
		t.Fatal("添加响应缺少 openId")
	}
	// 清理白名单，避免污染后续测试
	defer doWhitelistJSON(t, whitelistDeletePath, adminToken,
		map[string]any{"openId": addData.OpenID}, fmt.Sprintf("wl-autopay-del-%s", uuid.NewString()[:8]))

	// 白名单账号下单，期望订单直接支付完成
	orderReq := &cspb.CreateOrderReq{
		PayId:    "cookiecircle99",
		Channel:  "apple",
		AwardTag: "whitelist_autopay",
	}
	_, createResp := doCreateOrder(t, token, orderReq, fmt.Sprintf("wl-autopay-create-%s", uuid.NewString()[:8]))
	if createResp.Code != 0 || createResp.Data == nil {
		t.Fatalf("创建订单失败：code=%d, message=%q", createResp.Code, createResp.Message)
	}
	orderID := createResp.Data.OrderId

	// 轮询订单状态直至 PAID（白名单自动支付 + 通知 GameLogic 存在异步时序）
	deadline := time.Now().Add(10 * time.Second)
	paid := false
	for time.Now().Before(deadline) {
		_, statusResp := doGetOrderStatus(t, token, orderID, fmt.Sprintf("wl-autopay-status-%s", uuid.NewString()[:8]))
		if statusResp.Code == 0 && statusResp.Data != nil && statusResp.Data.Status == "PAID" {
			paid = true
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !paid {
		t.Fatalf("白名单账号订单在超时时间内未变为 PAID")
	}
	t.Logf("白名单自动支付验证通过：playerId=%d, orderId=%q 为 PAID", playerID, orderID)
}
