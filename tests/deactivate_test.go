// 测试目标：POST /auth/deactivate（账号注销，需 JWT）
//
// 注销语义：不删除玩家数据，而是永久作废登录身份（openId 追加 del_ 前缀释放）。
// 注销后：① 原 token 立即失效；② 以原游客 UUID 重新登录会创建全新玩家（isNew=true，playerId 不同）。
//
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestDeactivate
package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
)

// deactivatePath 账号注销接口路径
const deactivatePath = "/auth/deactivate"

// doDeactivate 携带 JWT 发送账号注销请求（protobuf 格式），返回 HTTP 状态码与响应。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doDeactivate(t *testing.T, token string, requestID string) (int, *cspb.DeactivateResp) {
	t.Helper()

	bodyBytes := marshalProtoReq(t, token, &cspb.DeactivateReq{})

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	reqURL := baseURL() + deactivatePath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	// token 为空表示模拟未鉴权场景，此时不下发 Authorization 头
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
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

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	var result cspb.DeactivateResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return resp.StatusCode, &result
}

// doLoginGuestByUUID 以指定游客 UUID 登录，返回 HTTP 状态码与完整登录响应（供断言 isNew/playerId）。
// 登录成功后记录 token 对应的收发包秘钥，使该 token 后续的鉴权接口能正确加解密。
func doLoginGuestByUUID(t *testing.T, uuidStr string) (int, *cspb.LoginResp) {
	t.Helper()
	req := &cspb.LoginReq{
		LoginType:  "guest",
		Platform:   "ios",
		Uuid:       uuidStr,
		ClientInfo: newTestClientInfo(),
	}
	status, resp := doLogin(t, req, fmt.Sprintf("deactivate-relogin-%s", uuid.NewString()[:8]))
	if resp != nil && resp.Code == 0 && resp.Data != nil {
		rememberPacketKey(resp.Data.Token, resp.Data.SaveKey)
	}
	return status, resp
}

// isTokenRejected 用（已注销的）token 调用需鉴权的玩家查询接口，返回该 token 是否已被拒绝。
// 注销后 token 被撤销，鉴权中间件会在收发包解密前直接拒绝，其错误响应为明文（未加密），
// 故此处按明文 proto 解析响应，避免因尝试解密明文错误体而误判。
func isTokenRejected(t *testing.T, token string, playerID int64) bool {
	t.Helper()

	bodyBytes := marshalProtoReq(t, token, &cspb.PlayerQueryReq{PlayerId: playerID})

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	reqURL := baseURL() + playerQueryPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("创建 HTTP 请求失败: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("X-Request-Id", fmt.Sprintf("deactivate-token-check-%s", uuid.NewString()[:8]))

	httpClient := &http.Client{Timeout: requestTimeout}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		t.Skipf("跳过测试：无法连接到服务 %s（%v），请先启动 gamelogic 服务", reqURL, err)
		return false
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	// 被拒绝时错误响应为明文 proto；成功时响应经加密。若明文解析成功且 code==0 则说明未被拒。
	var result cspb.PlayerQueryResp
	if err := proto.Unmarshal(respBytes, &result); err != nil {
		// 明文解析失败通常意味着响应体是加密的成功响应，即 token 仍有效
		return false
	}
	return result.Code != 0
}

// TestDeactivate_Success 注销正向流程：
// 登录 → 注销 → 期望 code=0，且原 token 立即失效（后续鉴权接口被拒）。
func TestDeactivate_Success(t *testing.T) {
	token, playerID := loginAsGuest(t)

	httpStatus, resp := doDeactivate(t, token, fmt.Sprintf("deactivate-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("注销业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	t.Logf("账号注销成功：playerId=%d", playerID)

	// 注销后原 token 应立即失效：用旧 token 调用需鉴权的玩家查询接口，期望被拒
	if !isTokenRejected(t, token, playerID) {
		t.Errorf("注销后原 token 仍可访问鉴权接口，期望被拒")
	}
}

// TestDeactivate_ReloginCreatesNewPlayer 注销后以原身份重登：
// 同一游客 UUID 注销后再次登录，应创建全新玩家（isNew=true 且 playerId 与注销前不同），
// 验证 openId 已被彻底释放、不再命中旧记录（不串号）。
func TestDeactivate_ReloginCreatesNewPlayer(t *testing.T) {
	// 固定同一 UUID，先登录得到旧 playerId
	fixedUUID := uuid.NewString()
	firstStatus, firstResp := doLoginGuestByUUID(t, fixedUUID)
	if firstStatus != http.StatusOK || firstResp.Code != 0 || firstResp.Data == nil {
		t.Fatalf("首次登录失败：status=%d, code=%d, message=%q", firstStatus, firstResp.Code, firstResp.Message)
	}
	oldPlayerID := firstResp.Data.PlayerId
	oldToken := firstResp.Data.Token

	// 注销该账号
	deStatus, deResp := doDeactivate(t, oldToken, fmt.Sprintf("deactivate-relogin-%s", uuid.NewString()[:8]))
	if deStatus != http.StatusOK || deResp.Code != 0 {
		t.Fatalf("注销失败：status=%d, code=%d, message=%q", deStatus, deResp.Code, deResp.Message)
	}

	// 以相同 UUID 重新登录：应创建全新玩家
	secondStatus, secondResp := doLoginGuestByUUID(t, fixedUUID)
	if secondStatus != http.StatusOK || secondResp.Code != 0 || secondResp.Data == nil {
		t.Fatalf("重登失败：status=%d, code=%d, message=%q", secondStatus, secondResp.Code, secondResp.Message)
	}
	if !secondResp.Data.IsNew {
		t.Errorf("注销后重登 isNew = false，期望创建全新玩家（true）")
	}
	if secondResp.Data.PlayerId == oldPlayerID {
		t.Errorf("注销后重登 playerId = %d 与注销前相同，期望全新 playerId（旧身份未释放会串号）", secondResp.Data.PlayerId)
	}
	t.Logf("注销后重登创建全新玩家：oldPlayerId=%d, newPlayerId=%d, isNew=%v", oldPlayerID, secondResp.Data.PlayerId, secondResp.Data.IsNew)
}

// TestDeactivate_NoAuth 未携带 JWT 调用注销接口应被拒绝（缺少授权令牌）。
func TestDeactivate_NoAuth(t *testing.T) {
	// token 为空：不设置 Authorization，marshalProtoReq 输出裸 protobuf 明文
	httpStatus, resp := doDeactivate(t, "", fmt.Sprintf("deactivate-noauth-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code == 0 {
		t.Errorf("无 JWT 注销意外成功，期望被拒：resp=%+v", resp)
	}
}
