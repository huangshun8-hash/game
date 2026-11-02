// Package tests 模拟客户端对 GameLogic 服务玩家信息查询接口的集成测试。
// 测试目标：POST /api/player/query
//
//	支持按玩家ID（playerId）或邀请码（inviteCode）两种方式查询玩家基本信息。
//
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 本文件只做正向流程验证：查询自身资料，昵称应为新账号默认昵称 "Player{playerId}"。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestPlayer
package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/invitex"
)

// 玩家查询接口路径常量
const playerQueryPath = "/api/player/query"

// ---- 辅助函数 ----

// doPlayerQuery 发送玩家信息查询请求（protobuf 格式），返回 HTTP 状态码和响应。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doPlayerQuery(t *testing.T, token string, req *cspb.PlayerQueryReq, requestID string) (int, *cspb.PlayerQueryResp) {
	t.Helper()

	bodyBytes := marshalProtoReq(t, token, req)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	reqURL := baseURL() + playerQueryPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
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
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", reqURL, err)
		return 0, nil
	}
	defer resp.Body.Close()

	// 校验响应 Content-Type 为 protobuf，与请求协商格式一致
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-protobuf" {
		t.Errorf("响应 Content-Type = %q，期望 application/x-protobuf", ct)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	var result cspb.PlayerQueryResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// ---- 玩家查询测试 ----

// TestPlayer_Query_ByPlayerId 按玩家ID查询正向流程：
// 查询自身资料，期望返回 code=0，playerId 与自身一致，昵称为默认昵称。
func TestPlayer_Query_ByPlayerId(t *testing.T) {
	token, playerID := loginAsGuest(t)

	req := &cspb.PlayerQueryReq{PlayerId: playerID}
	httpStatus, resp := doPlayerQuery(t, token, req, fmt.Sprintf("player-id-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil，期望包含玩家信息")
	}
	if resp.Data.PlayerId != playerID {
		t.Errorf("返回 playerId = %d，期望 %d", resp.Data.PlayerId, playerID)
	}
	expectedNickname := "Player" + strconv.FormatInt(playerID, 10)
	if resp.Data.Nickname != expectedNickname {
		t.Errorf("昵称 = %q，期望默认昵称 %q", resp.Data.Nickname, expectedNickname)
	}
	if resp.Data.Level < 0 {
		t.Errorf("等级 level = %d，期望 >= 0", resp.Data.Level)
	}
	t.Logf("按 playerId 查询成功：playerId=%d, nickname=%q, level=%d, avatar=%q",
		resp.Data.PlayerId, resp.Data.Nickname, resp.Data.Level, resp.Data.Avatar)
}

// TestPlayer_Query_ByInviteCode 按邀请码查询正向流程：
// 由自身 playerId 计算 6 位邀请码，查询后 playerId 应还原为自身。
func TestPlayer_Query_ByInviteCode(t *testing.T) {
	token, playerID := loginAsGuest(t)

	inviteCode, err := invitex.UID2InviteCode(playerID)
	if err != nil {
		t.Fatalf("生成邀请码失败: %v", err)
	}

	req := &cspb.PlayerQueryReq{InviteCode: inviteCode}
	httpStatus, resp := doPlayerQuery(t, token, req, fmt.Sprintf("player-code-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("data 字段为 nil，期望包含玩家信息")
	}
	if resp.Data.PlayerId != playerID {
		t.Errorf("返回 playerId = %d，期望 %d（邀请码 %q 反解不一致）", resp.Data.PlayerId, playerID, inviteCode)
	}
	expectedNickname := "Player" + strconv.FormatInt(playerID, 10)
	if resp.Data.Nickname != expectedNickname {
		t.Errorf("昵称 = %q，期望默认昵称 %q", resp.Data.Nickname, expectedNickname)
	}
	t.Logf("按邀请码查询成功：inviteCode=%q, playerId=%d, nickname=%q, level=%d",
		inviteCode, resp.Data.PlayerId, resp.Data.Nickname, resp.Data.Level)
}

// TestPlayer_Query_MissingTarget 验证请求未提供玩家ID和邀请码时返回参数错误。
func TestPlayer_Query_MissingTarget(t *testing.T) {
	token, _ := loginAsGuest(t)

	httpStatus, resp := doPlayerQuery(t, token, &cspb.PlayerQueryReq{}, fmt.Sprintf("player-missing-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 2450 {
		t.Fatalf("业务错误码 = %d，期望 2450（缺少查询参数），message=%q", resp.Code, resp.Message)
	}
	if resp.Data != nil {
		t.Errorf("失败响应不应包含 data：%+v", resp.Data)
	}
}

// TestPlayer_Query_InvalidInviteCode 验证非法邀请码返回对应业务错误。
func TestPlayer_Query_InvalidInviteCode(t *testing.T) {
	token, _ := loginAsGuest(t)

	httpStatus, resp := doPlayerQuery(t, token, &cspb.PlayerQueryReq{InviteCode: "bad"}, fmt.Sprintf("player-invalid-code-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 2451 {
		t.Fatalf("业务错误码 = %d，期望 2451（邀请码非法），message=%q", resp.Code, resp.Message)
	}
	if resp.Data != nil {
		t.Errorf("失败响应不应包含 data：%+v", resp.Data)
	}
}

// TestPlayer_Query_PlayerIDHasPriority 验证同时提供查询参数时优先使用 playerId。
func TestPlayer_Query_PlayerIDHasPriority(t *testing.T) {
	token, playerID := loginAsGuest(t)

	httpStatus, resp := doPlayerQuery(t, token, &cspb.PlayerQueryReq{
		PlayerId:   playerID,
		InviteCode: "bad",
	}, fmt.Sprintf("player-priority-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("同时传入 playerId 和非法邀请码后查询失败：code=%d, message=%q", resp.Code, resp.Message)
	}
	if resp.Data == nil || resp.Data.PlayerId != playerID {
		t.Fatalf("优先级查询返回 playerId = %v，期望 %d", resp.Data, playerID)
	}
}
