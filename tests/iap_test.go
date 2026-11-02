// Package tests 模拟客户端对 GameLogic 服务 IAP 内购接口的集成测试。
// 测试目标：
//
//	POST /api/iap/order       创建内购订单
//	POST /api/iap/claim       领取奖励
//	GET  /api/iap/orders/paid 查询已支付待领取订单
//	GET  /api/iap/order/status 查询单个订单状态
//	POST /api/iap/userTags    用户标签匹配
//
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestIAP
package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"kemiluo/pb/cspb"
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// ---- 路径常量 ----

const (
	// iapCreateOrderPath 创建订单接口路径
	iapCreateOrderPath = "/api/iap/order"
	// iapClaimPath 领取奖励接口路径
	iapClaimPath = "/api/iap/claim"
	// iapPaidOrdersPath 查询已支付待领取订单接口路径
	iapPaidOrdersPath = "/api/iap/orders/paid"
	// iapOrderStatusPath 查询订单状态接口路径
	iapOrderStatusPath = "/api/iap/order/status"
	// iapUserTagsPath 用户标签匹配接口路径
	iapUserTagsPath = "/api/iap/userTags"
)

// ---- 辅助函数 ----

// doCreateOrder 发送创建订单请求（protobuf 格式），返回 HTTP 状态码和响应。
// 连接失败时调用 t.Skipf 跳过测试。
func doCreateOrder(t *testing.T, token string, req *cspb.CreateOrderReq, requestID string) (int, *cspb.CreateOrderResp) {
	t.Helper()

	bodyBytes := marshalProtoReq(t, token, req)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + iapCreateOrderPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Authorization", "Bearer "+token)
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

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取创建订单响应体失败: %v", err)
	}
	var result cspb.CreateOrderResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// doClaimReward 发送领取奖励请求（protobuf 格式），返回 HTTP 状态码和响应。
func doClaimReward(t *testing.T, token string, req *cspb.ClaimRewardReq, requestID string) (int, *cspb.ClaimRewardResp) {
	t.Helper()

	bodyBytes := marshalProtoReq(t, token, req)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + iapClaimPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Authorization", "Bearer "+token)
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

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取领取奖励响应体失败: %v", err)
	}
	var result cspb.ClaimRewardResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// doListPaidOrders 发送查询已支付待领取订单请求（protobuf 格式），返回 HTTP 状态码和响应。
func doListPaidOrders(t *testing.T, token string, requestID string) (int, *cspb.ListPaidOrdersResp) {
	t.Helper()

	// ListPaidOrdersReq 无业务字段，序列化后为空字节；空请求体服务端跳过 body 解析，无需加密
	bodyBytes, err := proto.Marshal(&cspb.ListPaidOrdersReq{})
	if err != nil {
		t.Fatalf("proto 序列化查询待领取订单请求失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + iapPaidOrdersPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Authorization", "Bearer "+token)
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

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取查询待领取订单响应体失败: %v", err)
	}
	var result cspb.ListPaidOrdersResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// doGetOrderStatus 发送查询订单状态请求（JSON 格式 query string），返回 HTTP 状态码和响应。
func doGetOrderStatus(t *testing.T, token string, orderID string, requestID string) (int, *cspb.GetOrderStatusResp) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + iapOrderStatusPath + "?orderId=" + orderID
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Authorization", "Bearer "+token)
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

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取查询订单状态响应体失败: %v", err)
	}
	var result cspb.GetOrderStatusResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// doUserTags 发送用户标签匹配请求（protobuf 格式），返回 HTTP 状态码和响应。
func doUserTags(t *testing.T, token string, req *cspb.UserTagsReq, requestID string) (int, *cspb.UserTagsResp) {
	t.Helper()

	bodyBytes := marshalProtoReq(t, token, req)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + iapUserTagsPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Authorization", "Bearer "+token)
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

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取用户标签响应体失败: %v", err)
	}
	var result cspb.UserTagsResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// doCreateOrderJSON 发送创建订单请求（JSON 格式），返回 HTTP 状态码和 JSON 响应。
func doCreateOrderJSON(t *testing.T, token string, body map[string]any, requestID string) (int, map[string]any) {
	t.Helper()

	bodyBytes, err := sonic.Marshal(body)
	if err != nil {
		t.Fatalf("序列化创建订单 JSON 请求体失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := baseURL() + iapCreateOrderPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
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

	var result map[string]any
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("解析创建订单 JSON 响应体失败: %v", err)
	}
	return resp.StatusCode, result
}

// ---- 创建订单测试 ----

// TestIAP_CreateOrder_Success 创建订单成功：携带合法商品ID和渠道，期望返回 code=0 并含 orderId
func TestIAP_CreateOrder_Success(t *testing.T) {
	token, playerID := loginAsGuest(t)

	req := &cspb.CreateOrderReq{
		PayId:    "cookiecircle99",
		Channel:  "apple",
		AwardTag: "weekly_deal",
		Award:    `{"diamond":99}`,
	}
	httpStatus, resp := doCreateOrder(t, token, req, fmt.Sprintf("iap-create-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil，期望包含订单数据")
	}
	if resp.Data.OrderId == "" {
		t.Error("orderId 为空")
	}
	if resp.Data.PayId != req.PayId {
		t.Errorf("payId = %q，期望 %q", resp.Data.PayId, req.PayId)
	}
	if resp.Data.Channel != req.Channel {
		t.Errorf("channel = %q，期望 %q", resp.Data.Channel, req.Channel)
	}
	if resp.Data.AwardTag != req.AwardTag {
		t.Errorf("awardTag = %q，期望 %q", resp.Data.AwardTag, req.AwardTag)
	}
	// award 字段由服务器原样持久化并回传
	if resp.Data.Award != req.Award {
		t.Errorf("award = %q，期望 %q", resp.Data.Award, req.Award)
	}
	t.Logf("创建订单成功：playerId=%d, orderId=%q, payId=%q, channel=%q, award=%q",
		playerID, resp.Data.OrderId, resp.Data.PayId, resp.Data.Channel, resp.Data.Award)
}

// TestIAP_CreateOrder_NoReuse 相同 payId + awardTag 重复创建：期望每次生成全新订单（orderId 不同），不复用未支付订单
func TestIAP_CreateOrder_NoReuse(t *testing.T) {
	token, playerID := loginAsGuest(t)

	req := &cspb.CreateOrderReq{
		PayId:    "cookiecircle99",
		Channel:  "apple",
		AwardTag: "reuse_deal",
	}

	// 第一次创建
	_, firstResp := doCreateOrder(t, token, req, fmt.Sprintf("iap-noreuse-1-%s", uuid.NewString()[:8]))
	if firstResp.Code != 0 || firstResp.Data == nil {
		t.Fatalf("首次创建订单失败：code=%d, message=%q", firstResp.Code, firstResp.Message)
	}
	firstOrderID := firstResp.Data.OrderId

	// 第二次以相同参数创建，应生成全新订单而非复用首单
	_, secondResp := doCreateOrder(t, token, req, fmt.Sprintf("iap-noreuse-2-%s", uuid.NewString()[:8]))
	if secondResp.Code != 0 || secondResp.Data == nil {
		t.Fatalf("二次创建订单失败：code=%d, message=%q", secondResp.Code, secondResp.Message)
	}
	if secondResp.Data.OrderId == firstOrderID {
		t.Errorf("二次创建 orderId = %q，期望生成新订单而非复用首单 %q", secondResp.Data.OrderId, firstOrderID)
	}
	if secondResp.Data.OrderId == "" {
		t.Error("二次创建 orderId 为空")
	}
	t.Logf("订单不复用验证通过：playerId=%d, firstOrderId=%q, secondOrderId=%q",
		playerID, firstOrderID, secondResp.Data.OrderId)
}

// TestIAP_CreateOrder_MissingPayId 创建订单缺少 payId：期望返回参数错误
func TestIAP_CreateOrder_MissingPayId(t *testing.T) {
	token, _ := loginAsGuest(t)

	req := &cspb.CreateOrderReq{
		PayId:   "",
		Channel: "apple",
	}
	httpStatus, resp := doCreateOrder(t, token, req, fmt.Sprintf("iap-noprod-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码，但收到 code=0")
	}
	t.Logf("缺少 payId 校验通过：code=%d, message=%q", resp.Code, resp.Message)
}

// TestIAP_CreateOrder_InvalidChannel 创建订单无效渠道：期望返回参数错误
func TestIAP_CreateOrder_InvalidChannel(t *testing.T) {
	token, _ := loginAsGuest(t)

	req := &cspb.CreateOrderReq{
		PayId:   "diamond_60",
		Channel: "invalid_channel",
	}
	httpStatus, resp := doCreateOrder(t, token, req, fmt.Sprintf("iap-badchan-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码，但收到 code=0")
	}
	t.Logf("无效渠道校验通过：code=%d, message=%q", resp.Code, resp.Message)
}

// TestIAP_CreateOrder_NoToken 创建订单未携带 Token：期望返回未授权错误
func TestIAP_CreateOrder_NoToken(t *testing.T) {
	req := &cspb.CreateOrderReq{
		PayId:   "diamond_60",
		Channel: "apple",
	}
	// 不传 token，直接发请求
	httpStatus, resp := doCreateOrder(t, "", req, fmt.Sprintf("iap-notoken-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码（未授权），但收到 code=0")
	}
	t.Logf("未携带 Token 校验通过：code=%d, message=%q", resp.Code, resp.Message)
}

// TestIAP_CreateOrder_GoogleChannel 创建 Google 渠道订单成功
func TestIAP_CreateOrder_GoogleChannel(t *testing.T) {
	token, playerID := loginAsGuest(t)

	req := &cspb.CreateOrderReq{
		PayId:    "cookiecircle99",
		Channel:  "google",
		AwardTag: "first_purchase",
	}
	httpStatus, resp := doCreateOrder(t, token, req, fmt.Sprintf("iap-google-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil")
	}
	if resp.Data.Channel != "google" {
		t.Errorf("channel = %q，期望 google", resp.Data.Channel)
	}
	t.Logf("Google 渠道订单创建成功：playerId=%d, orderId=%q", playerID, resp.Data.OrderId)
}

// ---- 查询订单状态测试 ----

// TestIAP_GetOrderStatus_AfterCreate 创建订单后查询状态：期望状态为 WAIT_PAY
func TestIAP_GetOrderStatus_AfterCreate(t *testing.T) {
	token, playerID := loginAsGuest(t)

	// 先创建订单
	createReq := &cspb.CreateOrderReq{
		PayId:   "cookiecircle99",
		Channel: "apple",
	}
	_, createResp := doCreateOrder(t, token, createReq, fmt.Sprintf("iap-status-create-%s", uuid.NewString()[:8]))
	if createResp.Code != 0 || createResp.Data == nil {
		t.Fatalf("创建订单失败：code=%d, message=%q", createResp.Code, createResp.Message)
	}
	orderID := createResp.Data.OrderId

	// 查询订单状态
	httpStatus, statusResp := doGetOrderStatus(t, token, orderID, fmt.Sprintf("iap-status-get-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if statusResp.Code != 0 {
		t.Fatalf("查询订单状态失败：code=%d, message=%q", statusResp.Code, statusResp.Message)
	}
	if statusResp.Data == nil {
		t.Fatal("data 字段为 nil")
	}
	if statusResp.Data.OrderId != orderID {
		t.Errorf("orderId = %q，期望 %q", statusResp.Data.OrderId, orderID)
	}
	if statusResp.Data.Status != "WAIT_PAY" {
		t.Errorf("status = %q，期望 WAIT_PAY", statusResp.Data.Status)
	}
	t.Logf("查询订单状态成功：playerId=%d, orderId=%q, status=%q",
		playerID, orderID, statusResp.Data.Status)
}

// TestIAP_GetOrderStatus_NotFound 查询不存在的订单：期望返回订单不存在错误
func TestIAP_GetOrderStatus_NotFound(t *testing.T) {
	token, _ := loginAsGuest(t)

	httpStatus, resp := doGetOrderStatus(t, token, "nonexistent_order_12345", fmt.Sprintf("iap-status-404-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码（订单不存在），但收到 code=0")
	}
	t.Logf("订单不存在校验通过：code=%d, message=%q", resp.Code, resp.Message)
}

// TestIAP_GetOrderStatus_OtherPlayer 查询其他玩家订单：期望返回订单不存在
func TestIAP_GetOrderStatus_OtherPlayer(t *testing.T) {
	// 玩家 A 创建订单
	tokenA, _ := loginAsGuest(t)
	createReq := &cspb.CreateOrderReq{
		PayId:   "cookiecircle99",
		Channel: "apple",
	}
	_, createResp := doCreateOrder(t, tokenA, createReq, fmt.Sprintf("iap-other-create-%s", uuid.NewString()[:8]))
	if createResp.Code != 0 || createResp.Data == nil {
		t.Fatalf("玩家A创建订单失败：code=%d", createResp.Code)
	}
	orderID := createResp.Data.OrderId

	// 玩家 B 尝试查询玩家 A 的订单
	tokenB, _ := loginAsGuest(t)
	httpStatus, resp := doGetOrderStatus(t, tokenB, orderID, fmt.Sprintf("iap-other-get-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码（他人订单不可查），但收到 code=0")
	}
	t.Logf("跨玩家查询订单隔离验证通过：code=%d, message=%q", resp.Code, resp.Message)
}

// ---- 查询已支付待领取订单测试 ----

// TestIAP_ListPaidOrders_Empty 新账号查询待领取订单：期望返回空列表
func TestIAP_ListPaidOrders_Empty(t *testing.T) {
	token, playerID := loginAsGuest(t)

	httpStatus, resp := doListPaidOrders(t, token, fmt.Sprintf("iap-paid-empty-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("查询待领取订单失败：code=%d, message=%q", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil")
	}
	if len(resp.Data.Orders) != 0 {
		t.Errorf("期望订单列表为空，实际有 %d 条", len(resp.Data.Orders))
	}
	t.Logf("新账号待领取订单为空验证通过：playerId=%d", playerID)
}

// TestIAP_ListPaidOrders_NoToken 未携带 Token 查询待领取订单：期望返回未授权
func TestIAP_ListPaidOrders_NoToken(t *testing.T) {
	httpStatus, resp := doListPaidOrders(t, "", fmt.Sprintf("iap-paid-notoken-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码（未授权），但收到 code=0")
	}
	t.Logf("未授权查询待领取订单校验通过：code=%d, message=%q", resp.Code, resp.Message)
}

// ---- 领取奖励测试 ----

// TestIAP_ClaimReward_OrderNotPaid 领取未支付订单的奖励：期望返回订单状态不允许
func TestIAP_ClaimReward_OrderNotPaid(t *testing.T) {
	token, _ := loginAsGuest(t)

	// 先创建订单（状态为 WAIT_PAY）
	createReq := &cspb.CreateOrderReq{
		PayId:   "cookiecircle99",
		Channel: "apple",
	}
	_, createResp := doCreateOrder(t, token, createReq, fmt.Sprintf("iap-claim-create-%s", uuid.NewString()[:8]))
	if createResp.Code != 0 || createResp.Data == nil {
		t.Fatalf("创建订单失败：code=%d", createResp.Code)
	}

	// 尝试领取（订单还未支付）
	claimReq := &cspb.ClaimRewardReq{
		OrderId: createResp.Data.OrderId,
		Reward:  map[string]int64{"diamond": 60},
	}
	httpStatus, claimResp := doClaimReward(t, token, claimReq, fmt.Sprintf("iap-claim-notpaid-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if claimResp.Code == 0 {
		t.Fatal("期望返回非零错误码（订单未支付不可领取），但收到 code=0")
	}
	t.Logf("未支付订单领取拦截通过：code=%d, message=%q", claimResp.Code, claimResp.Message)
}

// TestIAP_ClaimReward_OrderNotFound 领取不存在的订单：期望返回订单不存在
func TestIAP_ClaimReward_OrderNotFound(t *testing.T) {
	token, _ := loginAsGuest(t)

	claimReq := &cspb.ClaimRewardReq{
		OrderId: "nonexistent_order_99999",
		Reward:  map[string]int64{"diamond": 60},
	}
	httpStatus, resp := doClaimReward(t, token, claimReq, fmt.Sprintf("iap-claim-404-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码（订单不存在），但收到 code=0")
	}
	t.Logf("不存在订单领取拦截通过：code=%d, message=%q", resp.Code, resp.Message)
}

// TestIAP_ClaimReward_MissingOrderId 领取奖励缺少 orderId：期望返回参数错误
func TestIAP_ClaimReward_MissingOrderId(t *testing.T) {
	token, _ := loginAsGuest(t)

	claimReq := &cspb.ClaimRewardReq{
		OrderId: "",
		Reward:  map[string]int64{"diamond": 60},
	}
	httpStatus, resp := doClaimReward(t, token, claimReq, fmt.Sprintf("iap-claim-noid-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码（缺少 orderId），但收到 code=0")
	}
	t.Logf("缺少 orderId 领取拦截通过：code=%d, message=%q", resp.Code, resp.Message)
}

// TestIAP_ClaimReward_OtherPlayerOrder 领取其他玩家的订单：期望返回订单不存在
func TestIAP_ClaimReward_OtherPlayerOrder(t *testing.T) {
	// 玩家 A 创建订单
	tokenA, _ := loginAsGuest(t)
	createReq := &cspb.CreateOrderReq{
		PayId:   "cookiecircle99",
		Channel: "apple",
	}
	_, createResp := doCreateOrder(t, tokenA, createReq, fmt.Sprintf("iap-claim-other-create-%s", uuid.NewString()[:8]))
	if createResp.Code != 0 || createResp.Data == nil {
		t.Fatalf("玩家A创建订单失败：code=%d", createResp.Code)
	}

	// 玩家 B 尝试领取玩家 A 的订单
	tokenB, _ := loginAsGuest(t)
	claimReq := &cspb.ClaimRewardReq{
		OrderId: createResp.Data.OrderId,
		Reward:  map[string]int64{"diamond": 60},
	}
	httpStatus, resp := doClaimReward(t, tokenB, claimReq, fmt.Sprintf("iap-claim-other-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码（他人订单不可领取），但收到 code=0")
	}
	t.Logf("跨玩家领取隔离验证通过：code=%d, message=%q", resp.Code, resp.Message)
}

// ---- 用户标签匹配测试 ----

// TestIAP_UserTags_Success 用户标签匹配成功：期望返回 code=0 和 tags 列表
func TestIAP_UserTags_Success(t *testing.T) {
	token, playerID := loginAsGuest(t)

	req := &cspb.UserTagsReq{
		Enum: "",
	}
	httpStatus, resp := doUserTags(t, token, req, fmt.Sprintf("iap-tags-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("用户标签匹配失败：code=%d, message=%q", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil")
	}
	// tags 可能为空（如配置表中无匹配 enum），但 data.tags 字段应存在
	t.Logf("用户标签匹配成功：playerId=%d, enum=%q, tags=%v",
		playerID, req.Enum, resp.Data.Tags)
}

// TestIAP_UserTags_NoToken 未携带 Token 进行标签匹配：期望返回未授权
func TestIAP_UserTags_NoToken(t *testing.T) {
	req := &cspb.UserTagsReq{
		Enum: "weekly_gift",
	}
	httpStatus, resp := doUserTags(t, "", req, fmt.Sprintf("iap-tags-notoken-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Fatal("期望返回非零错误码（未授权），但收到 code=0")
	}
	t.Logf("未授权标签匹配校验通过：code=%d, message=%q", resp.Code, resp.Message)
}

// ---- 创建订单 JSON 格式测试 ----

// TestIAP_CreateOrder_JSON_Success 使用 JSON 格式创建订单：验证 JSON 响应结构正确
func TestIAP_CreateOrder_JSON_Success(t *testing.T) {
	token, playerID := loginAsGuest(t)

	const jsonPayID = "cookiecircle99"
	body := map[string]any{
		"payId":    jsonPayID,
		"channel":  "google",
		"awardTag": "daily_deal",
		"award":    `{"gold":600}`,
	}
	httpStatus, resp := doCreateOrderJSON(t, token, body, fmt.Sprintf("iap-json-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	code, _ := resp["code"].(float64)
	if code != 0 {
		t.Fatalf("业务错误码 = %v，message = %v，期望 code=0", resp["code"], resp["message"])
	}
	data, ok := resp["data"].(map[string]any)
	if !ok || data == nil {
		t.Fatal("data 字段缺失或类型不对")
	}
	if data["orderId"] == "" {
		t.Error("orderId 为空")
	}
	if data["payId"] != jsonPayID {
		t.Errorf("payId = %v，期望 %q", data["payId"], jsonPayID)
	}
	if data["channel"] != "google" {
		t.Errorf("channel = %v，期望 google", data["channel"])
	}
	if data["award"] != `{"gold":600}` {
		t.Errorf("award = %v，期望 %q", data["award"], `{"gold":600}`)
	}
	t.Logf("JSON 创建订单成功：playerId=%d, orderId=%v, award=%v", playerID, data["orderId"], data["award"])
}

// ---- 综合流程测试 ----

// TestIAP_FullFlow_CreateAndQueryStatus 完整流程：创建订单 → 查询状态 → 查询待领取列表
// 验证创建后订单可查且状态正确，待领取列表中因未支付所以为空
func TestIAP_FullFlow_CreateAndQueryStatus(t *testing.T) {
	token, playerID := loginAsGuest(t)

	// 1. 创建订单
	createReq := &cspb.CreateOrderReq{
		PayId:    "cookiecircle99",
		Channel:  "apple",
		AwardTag: "level_up_gift",
	}
	_, createResp := doCreateOrder(t, token, createReq, fmt.Sprintf("iap-flow-create-%s", uuid.NewString()[:8]))
	if createResp.Code != 0 || createResp.Data == nil {
		t.Fatalf("创建订单失败：code=%d, message=%q", createResp.Code, createResp.Message)
	}
	orderID := createResp.Data.OrderId
	t.Logf("步骤1-创建订单成功：orderId=%q", orderID)

	// 2. 查询订单状态
	_, statusResp := doGetOrderStatus(t, token, orderID, fmt.Sprintf("iap-flow-status-%s", uuid.NewString()[:8]))
	if statusResp.Code != 0 {
		t.Fatalf("查询订单状态失败：code=%d, message=%q", statusResp.Code, statusResp.Message)
	}
	if statusResp.Data.Status != "WAIT_PAY" {
		t.Errorf("订单状态 = %q，期望 WAIT_PAY", statusResp.Data.Status)
	}
	if statusResp.Data.PayId != createReq.PayId {
		t.Errorf("payId = %q，期望 %q", statusResp.Data.PayId, createReq.PayId)
	}
	t.Logf("步骤2-查询状态成功：status=%q, payId=%q", statusResp.Data.Status, statusResp.Data.PayId)

	// 3. 查询待领取订单（应为空，因为未支付）
	_, paidResp := doListPaidOrders(t, token, fmt.Sprintf("iap-flow-paid-%s", uuid.NewString()[:8]))
	if paidResp.Code != 0 {
		t.Fatalf("查询待领取订单失败：code=%d, message=%q", paidResp.Code, paidResp.Message)
	}
	if len(paidResp.Data.Orders) != 0 {
		t.Errorf("待领取订单列表应为空（未支付），实际有 %d 条", len(paidResp.Data.Orders))
	}
	t.Logf("步骤3-待领取列表为空验证通过：playerId=%d", playerID)
}
