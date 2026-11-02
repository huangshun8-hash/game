// Package tests 模拟客户端对 GameLogic 服务好友系统接口的集成测试。
// 测试目标：
//
//	POST /api/friend/apply          发送好友申请
//	POST /api/friend/apply/handle   同意/拒绝好友申请
//	POST /api/friend/apply/cancel   撤销发出的申请
//	GET  /api/friend/apply/in       收到的申请列表
//	GET  /api/friend/apply/out      发出的申请列表
//	GET  /api/friend/list           好友列表
//	POST /api/friend/rank           好友排行榜
//	POST /api/friend/delete         删除好友
//
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 本文件只做正向流程验证（申请→列表→同意→好友列表→排行→删除）。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestFriend
package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"kemiluo/pb/cspb"
)

// ---- 路径常量 ----

const (
	friendApplyPath       = "/api/friend/apply"
	friendApplyHandlePath = "/api/friend/apply/handle"
	friendApplyCancelPath = "/api/friend/apply/cancel"
	friendApplyInPath     = "/api/friend/apply/in"
	friendApplyOutPath    = "/api/friend/apply/out"
	friendListPath        = "/api/friend/list"
	friendRankPath        = "/api/friend/rank"
	friendDeletePath      = "/api/friend/delete"
)

// 好友分页默认查询窗口
const (
	friendPageStart = 1
	friendPageEnd   = 20
)

// ---- 辅助函数 ----

// doFriendPost 发送好友相关 POST 请求（protobuf 格式），返回 HTTP 状态码和响应体字节。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doFriendPost(t *testing.T, path string, token string, body []byte, requestID string) (int, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	reqURL := baseURL() + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
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

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	return resp.StatusCode, respBytes
}

// doFriendGet 发送好友相关 GET 请求（protobuf 期望响应），返回 HTTP 状态码和响应体字节。
// 查询参数通过 query 指定；连接失败时调用 t.Skipf 跳过测试。
func doFriendGet(t *testing.T, path string, token string, query url.Values, requestID string) (int, http.Header, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	reqURL := baseURL() + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
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
		return 0, nil, nil
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
	return resp.StatusCode, resp.Header, respBytes
}

// doFriendApply 发送好友申请请求，返回 HTTP 状态码和响应。
func doFriendApply(t *testing.T, token string, targetUid int64, requestID string) (int, *cspb.FriendApplyResp) {
	t.Helper()
	body := marshalProtoReq(t, token, &cspb.FriendApplyReq{TargetUid: targetUid})
	httpStatus, respBytes := doFriendPost(t, friendApplyPath, token, body, requestID)
	var result cspb.FriendApplyResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doFriendApplyHandle 发送处理好友申请请求，返回 HTTP 状态码和响应。
func doFriendApplyHandle(t *testing.T, token string, applyUid int64, accept bool, requestID string) (int, *cspb.FriendApplyHandleResp) {
	t.Helper()
	body := marshalProtoReq(t, token, &cspb.FriendApplyHandleReq{ApplyUid: applyUid, Accept: accept})
	httpStatus, respBytes := doFriendPost(t, friendApplyHandlePath, token, body, requestID)
	var result cspb.FriendApplyHandleResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doFriendApplyCancel 发送撤销申请请求，返回 HTTP 状态码和响应。
func doFriendApplyCancel(t *testing.T, token string, targetUid int64, requestID string) (int, *cspb.FriendApplyCancelResp) {
	t.Helper()
	body := marshalProtoReq(t, token, &cspb.FriendApplyCancelReq{TargetUid: targetUid})
	httpStatus, respBytes := doFriendPost(t, friendApplyCancelPath, token, body, requestID)
	var result cspb.FriendApplyCancelResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doFriendApplyList 发送申请列表查询请求（received=true 查收到的，false 查发出的），返回 HTTP 状态码和响应。
func doFriendApplyList(t *testing.T, token string, start, end int32, received bool, requestID string) (int, *cspb.FriendApplyListResp) {
	t.Helper()
	path := friendApplyOutPath
	if received {
		path = friendApplyInPath
	}
	query := url.Values{}
	query.Set("start", fmt.Sprintf("%d", start))
	query.Set("end", fmt.Sprintf("%d", end))
	httpStatus, _, respBytes := doFriendGet(t, path, token, query, requestID)
	var result cspb.FriendApplyListResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doFriendList 发送好友列表查询请求，返回 HTTP 状态码和响应。
func doFriendList(t *testing.T, token string, start, end int32, requestID string) (int, *cspb.FriendListResp) {
	t.Helper()
	query := url.Values{}
	query.Set("start", fmt.Sprintf("%d", start))
	query.Set("end", fmt.Sprintf("%d", end))
	httpStatus, _, respBytes := doFriendGet(t, friendListPath, token, query, requestID)
	var result cspb.FriendListResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doFriendRank 发送好友排行榜查询请求（POST），返回 HTTP 状态码和响应。
// friends 为额外好友的第三方账号（account）列表，可为 nil。
func doFriendRank(t *testing.T, token string, rankType cspb.RankType, friends []string, requestID string) (int, *cspb.FriendRankResp) {
	t.Helper()
	body := marshalProtoReq(t, token, &cspb.FriendRankReq{Type: rankType, Friends: friends})
	httpStatus, respBytes := doFriendPost(t, friendRankPath, token, body, requestID)
	var result cspb.FriendRankResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doFriendDelete 发送删除好友请求，返回 HTTP 状态码和响应。
func doFriendDelete(t *testing.T, token string, friendUid int64, requestID string) (int, *cspb.FriendDeleteResp) {
	t.Helper()
	body := marshalProtoReq(t, token, &cspb.FriendDeleteReq{FriendUid: friendUid})
	httpStatus, respBytes := doFriendPost(t, friendDeletePath, token, body, requestID)
	var result cspb.FriendDeleteResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// assertFriendListContains 断言好友列表中包含指定玩家ID。
func assertFriendListContains(t *testing.T, resp *cspb.FriendListResp, playerID int64) {
	t.Helper()
	if resp.Data == nil {
		t.Fatal("好友列表 data 字段为 nil")
	}
	for _, item := range resp.Data.Items {
		if item.GetFriendUid() == playerID {
			return
		}
	}
	t.Errorf("好友列表未包含玩家 %d，实际好友: %v", playerID, resp.Data.Items)
}

// assertFriendListNotContains 断言好友列表中不包含指定玩家ID。
func assertFriendListNotContains(t *testing.T, resp *cspb.FriendListResp, playerID int64) {
	t.Helper()
	if resp.Data == nil {
		t.Fatal("好友列表 data 字段为 nil")
	}
	for _, item := range resp.Data.Items {
		if item.GetFriendUid() == playerID {
			t.Errorf("好友列表仍包含已删除的玩家 %d", playerID)
			return
		}
	}
}

// ---- 好友测试 ----

// TestFriend_Apply_Success 好友申请正向流程：
// 玩家A向玩家B发送好友申请，期望返回 code=0。
func TestFriend_Apply_Success(t *testing.T) {
	tokenA, playerA := loginAsGuest(t)
	_, playerB := loginAsGuest(t)

	httpStatus, resp := doFriendApply(t, tokenA, playerB, fmt.Sprintf("friend-apply-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	t.Logf("好友申请成功：playerA=%d，targetUid=%d", playerA, playerB)
}

// TestFriend_ApplyList_Visibility 申请双向可见：
// 玩家A向玩家B发送申请后，A 的"发出的申请"列表中能看到 B，B 的"收到的申请"列表中能看到 A。
func TestFriend_ApplyList_Visibility(t *testing.T) {
	tokenA, playerA := loginAsGuest(t)
	tokenB, playerB := loginAsGuest(t)

	// 步骤1：A 向 B 发送申请
	_, applyResp := doFriendApply(t, tokenA, playerB, fmt.Sprintf("friend-vis-apply-%s", uuid.NewString()[:8]))
	if applyResp.Code != 0 {
		t.Fatalf("发送申请失败：code=%d, message=%q", applyResp.Code, applyResp.Message)
	}

	// 步骤2：A 查询发出的申请，应包含 B
	_, outResp := doFriendApplyList(t, tokenA, friendPageStart, friendPageEnd, false, fmt.Sprintf("friend-vis-out-%s", uuid.NewString()[:8]))
	if outResp.Code != 0 {
		t.Fatalf("查询发出的申请失败：code=%d, message=%q", outResp.Code, outResp.Message)
	}
	if outResp.Data == nil || len(outResp.Data.Items) == 0 {
		t.Fatal("发出的申请列表为空，期望包含至少一条申请")
	}
	foundOut := false
	for _, item := range outResp.Data.Items {
		if item.GetTargetUid() == playerB {
			foundOut = true
			if item.GetApplyUid() != playerA {
				t.Errorf("发出的申请 applyUid = %d，期望 %d", item.GetApplyUid(), playerA)
			}
		}
	}
	if !foundOut {
		t.Errorf("发出的申请列表未包含 targetUid=%d", playerB)
	}

	// 步骤3：B 查询收到的申请，应包含 A
	_, inResp := doFriendApplyList(t, tokenB, friendPageStart, friendPageEnd, true, fmt.Sprintf("friend-vis-in-%s", uuid.NewString()[:8]))
	if inResp.Code != 0 {
		t.Fatalf("查询收到的申请失败：code=%d, message=%q", inResp.Code, inResp.Message)
	}
	if inResp.Data == nil || len(inResp.Data.Items) == 0 {
		t.Fatal("收到的申请列表为空，期望包含至少一条申请")
	}
	foundIn := false
	for _, item := range inResp.Data.Items {
		if item.GetApplyUid() == playerA {
			foundIn = true
			if item.GetTargetUid() != playerB {
				t.Errorf("收到的申请 targetUid = %d，期望 %d", item.GetTargetUid(), playerB)
			}
		}
	}
	if !foundIn {
		t.Errorf("收到的申请列表未包含 applyUid=%d", playerA)
	}

	t.Logf("申请双向可见验证通过：playerA=%d, playerB=%d", playerA, playerB)
}

// TestFriend_Apply_Cancel 撤销申请正向流程：
// 玩家A申请后主动撤销，双方申请列表恢复为空。
func TestFriend_Apply_Cancel(t *testing.T) {
	tokenA, playerA := loginAsGuest(t)
	tokenB, playerB := loginAsGuest(t)

	// 步骤1：A 向 B 发送申请
	_, applyResp := doFriendApply(t, tokenA, playerB, fmt.Sprintf("friend-cancel-apply-%s", uuid.NewString()[:8]))
	if applyResp.Code != 0 {
		t.Fatalf("发送申请失败：code=%d, message=%q", applyResp.Code, applyResp.Message)
	}

	// 步骤2：A 撤销申请
	httpStatus, cancelResp := doFriendApplyCancel(t, tokenA, playerB, fmt.Sprintf("friend-cancel-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if cancelResp.Code != 0 {
		t.Fatalf("撤销申请失败：code=%d, message=%q", cancelResp.Code, cancelResp.Message)
	}
	if cancelResp.Data == nil || cancelResp.Data.TargetUid != playerB {
		t.Errorf("撤销申请返回 data.targetUid = %v，期望 %d", cancelResp.Data, playerB)
	}

	// 步骤3：A 发出的申请列表应为空
	_, outResp := doFriendApplyList(t, tokenA, friendPageStart, friendPageEnd, false, fmt.Sprintf("friend-cancel-out-%s", uuid.NewString()[:8]))
	if outResp.Code != 0 {
		t.Fatalf("查询发出的申请失败：code=%d", outResp.Code)
	}
	if outResp.Data != nil && len(outResp.Data.Items) != 0 {
		t.Errorf("撤销后发出的申请列表仍有 %d 条记录", len(outResp.Data.Items))
	}

	// 步骤4：B 收到的申请列表应为空
	_, inResp := doFriendApplyList(t, tokenB, friendPageStart, friendPageEnd, true, fmt.Sprintf("friend-cancel-in-%s", uuid.NewString()[:8]))
	if inResp.Code != 0 {
		t.Fatalf("查询收到的申请失败：code=%d", inResp.Code)
	}
	if inResp.Data != nil && len(inResp.Data.Items) != 0 {
		t.Errorf("撤销后收到的申请列表仍有 %d 条记录", len(inResp.Data.Items))
	}

	t.Logf("撤销申请验证通过：playerA=%d, playerB=%d", playerA, playerB)
}

// TestFriend_Accept_BecomesFriend 同意申请正向流程：
// 玩家A申请、玩家B同意后，双方好友列表互相包含对方。
func TestFriend_Accept_BecomesFriend(t *testing.T) {
	tokenA, playerA := loginAsGuest(t)
	tokenB, playerB := loginAsGuest(t)

	// 步骤1：A 向 B 发送申请
	_, applyResp := doFriendApply(t, tokenA, playerB, fmt.Sprintf("friend-accept-apply-%s", uuid.NewString()[:8]))
	if applyResp.Code != 0 {
		t.Fatalf("发送申请失败：code=%d, message=%q", applyResp.Code, applyResp.Message)
	}

	// 步骤2：B 同意申请
	httpStatus, handleResp := doFriendApplyHandle(t, tokenB, playerA, true, fmt.Sprintf("friend-accept-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if handleResp.Code != 0 {
		t.Fatalf("同意申请失败：code=%d, message=%q", handleResp.Code, handleResp.Message)
	}
	if handleResp.Data == nil || !handleResp.Data.Accept {
		t.Errorf("处理申请返回 data.accept = %v，期望 true", handleResp.Data)
	}

	// 步骤3：A 的好友列表包含 B
	_, listAResp := doFriendList(t, tokenA, friendPageStart, friendPageEnd, fmt.Sprintf("friend-accept-listA-%s", uuid.NewString()[:8]))
	if listAResp.Code != 0 {
		t.Fatalf("查询 A 好友列表失败：code=%d, message=%q", listAResp.Code, listAResp.Message)
	}
	assertFriendListContains(t, listAResp, playerB)

	// 步骤4：B 的好友列表包含 A
	_, listBResp := doFriendList(t, tokenB, friendPageStart, friendPageEnd, fmt.Sprintf("friend-accept-listB-%s", uuid.NewString()[:8]))
	if listBResp.Code != 0 {
		t.Fatalf("查询 B 好友列表失败：code=%d, message=%q", listBResp.Code, listBResp.Message)
	}
	assertFriendListContains(t, listBResp, playerA)

	t.Logf("同意申请建立好友关系验证通过：playerA=%d, playerB=%d", playerA, playerB)
}

// TestFriend_Rank_Level 好友等级排行榜正向流程：
// 建立好友关系后，查询等级排行（type=1），列表包含双方与本人且无重复，self 字段固定返回；
// 再传入额外好友账号列表（含空串与未知账号、以及超过 100 个的超量列表）验证不影响既有排行结果（去重、忽略无效账号、超量截断）。
func TestFriend_Rank_Level(t *testing.T) {
	tokenA, playerA := loginAsGuest(t)
	tokenB, playerB := loginAsGuest(t)

	// 步骤1：A 申请、B 同意，建立好友关系
	_, applyResp := doFriendApply(t, tokenA, playerB, fmt.Sprintf("friend-rank-apply-%s", uuid.NewString()[:8]))
	if applyResp.Code != 0 {
		t.Fatalf("发送申请失败：code=%d", applyResp.Code)
	}
	_, handleResp := doFriendApplyHandle(t, tokenB, playerA, true, fmt.Sprintf("friend-rank-accept-%s", uuid.NewString()[:8]))
	if handleResp.Code != 0 {
		t.Fatalf("同意申请失败：code=%d", handleResp.Code)
	}

	// 步骤2：A 查询等级排行榜
	httpStatus, rankResp := doFriendRank(t, tokenA, cspb.RankType_RankTypeLevel, nil, fmt.Sprintf("friend-rank-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if rankResp.Code != 0 {
		t.Fatalf("查询排行榜失败：code=%d, message=%q", rankResp.Code, rankResp.Message)
	}
	if rankResp.Data == nil {
		t.Fatal("排行榜 data 字段为 nil")
	}

	// 列表应包含 A 与 B（好友+本人），且同一玩家不重复出现
	playerIDsInRank := make(map[int64]bool)
	for _, item := range rankResp.Data.Items {
		if playerIDsInRank[item.GetPlayerUid()] {
			t.Errorf("排行榜出现重复玩家 playerUid = %d", item.GetPlayerUid())
		}
		playerIDsInRank[item.GetPlayerUid()] = true
		if item.GetRank() < 1 {
			t.Errorf("排行榜名次 rank = %d，期望 >= 1", item.GetRank())
		}
	}
	if !playerIDsInRank[playerA] {
		t.Error("排行榜未包含本人 playerA")
	}
	if !playerIDsInRank[playerB] {
		t.Error("排行榜未包含好友 playerB")
	}

	// self 字段固定返回本人信息
	if rankResp.Data.Self == nil {
		t.Fatal("排行榜 self 字段为 nil")
	}
	if rankResp.Data.Self.PlayerUid != playerA {
		t.Errorf("排行榜 self.playerUid = %d，期望 %d", rankResp.Data.Self.PlayerUid, playerA)
	}
	if rankResp.Data.Self.Rank < 1 {
		t.Errorf("排行榜 self.rank = %d，期望 >= 1", rankResp.Data.Self.Rank)
	}

	// 步骤3：传入额外好友账号（含空串与不存在的账号）不应影响既有排行榜结果：
	// 空账号被忽略、未知账号解析不到玩家，候选集与不传时一致，仍包含本人与好友且无重复
	extraStatus, extraResp := doFriendRank(t, tokenA, cspb.RankType_RankTypeLevel,
		[]string{"", "unknown-account-" + uuid.NewString()[:8]}, fmt.Sprintf("friend-rank-extra-%s", uuid.NewString()[:8]))
	if extraStatus != http.StatusOK {
		t.Errorf("带额外好友排行榜 HTTP 状态码 = %d，期望 200", extraStatus)
	}
	if extraResp.Code != 0 {
		t.Fatalf("带额外好友查询排行榜失败：code=%d, message=%q", extraResp.Code, extraResp.Message)
	}
	if extraResp.Data == nil {
		t.Fatal("带额外好友的排行榜 data 字段为 nil")
	}
	if len(extraResp.Data.Items) != len(rankResp.Data.Items) {
		t.Errorf("带额外好友的排行榜条数 = %d，期望与不传时一致 = %d", len(extraResp.Data.Items), len(rankResp.Data.Items))
	}
	extraIDs := make(map[int64]bool)
	for _, item := range extraResp.Data.Items {
		if extraIDs[item.GetPlayerUid()] {
			t.Errorf("带额外好友的排行榜出现重复玩家 playerUid = %d", item.GetPlayerUid())
		}
		extraIDs[item.GetPlayerUid()] = true
	}
	if !extraIDs[playerA] || !extraIDs[playerB] {
		t.Errorf("带额外好友的排行榜未同时包含本人与好友：playerA=%v, playerB=%v", extraIDs[playerA], extraIDs[playerB])
	}

	// 步骤4：传入超过 100 个额外好友账号不报错，仅取前 100 个
	tooManyFriends := make([]string, 0, 150)
	for i := 0; i < 150; i++ {
		tooManyFriends = append(tooManyFriends, fmt.Sprintf("unknown-account-%d-%s", i, uuid.NewString()[:8]))
	}
	cappedStatus, tooManyResp := doFriendRank(t, tokenA, cspb.RankType_RankTypeLevel, tooManyFriends, fmt.Sprintf("friend-rank-cap-%s", uuid.NewString()[:8]))
	if cappedStatus != http.StatusOK {
		t.Errorf("超量额外好友排行榜 HTTP 状态码 = %d，期望 200", cappedStatus)
	}
	if tooManyResp.Code != 0 {
		t.Fatalf("超量额外好友查询排行榜失败：code=%d, message=%q", tooManyResp.Code, tooManyResp.Message)
	}
	if tooManyResp.Data == nil {
		t.Fatal("超量额外好友的排行榜 data 字段为 nil")
	}
	if len(tooManyResp.Data.Items) != len(rankResp.Data.Items) {
		t.Errorf("超量额外好友的排行榜条数 = %d，期望与不传时一致 = %d（无效账号被忽略）", len(tooManyResp.Data.Items), len(rankResp.Data.Items))
	}

	t.Logf("等级排行榜验证通过：playerA=%d, playerB=%d, items=%d, selfRank=%d",
		playerA, playerB, len(rankResp.Data.Items), rankResp.Data.Self.Rank)
}

// TestFriend_Delete_RemovesRelation 删除好友正向流程：
// 删除后双方好友列表不再包含对方，且可重新建立好友关系。
func TestFriend_Delete_RemovesRelation(t *testing.T) {
	tokenA, playerA := loginAsGuest(t)
	tokenB, playerB := loginAsGuest(t)

	// 步骤1：建立好友关系
	_, applyResp := doFriendApply(t, tokenA, playerB, fmt.Sprintf("friend-del-apply-%s", uuid.NewString()[:8]))
	if applyResp.Code != 0 {
		t.Fatalf("发送申请失败：code=%d", applyResp.Code)
	}
	_, handleResp := doFriendApplyHandle(t, tokenB, playerA, true, fmt.Sprintf("friend-del-accept-%s", uuid.NewString()[:8]))
	if handleResp.Code != 0 {
		t.Fatalf("同意申请失败：code=%d", handleResp.Code)
	}

	// 步骤2：A 删除好友 B
	httpStatus, delResp := doFriendDelete(t, tokenA, playerB, fmt.Sprintf("friend-del-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if delResp.Code != 0 {
		t.Fatalf("删除好友失败：code=%d, message=%q", delResp.Code, delResp.Message)
	}
	if delResp.Data == nil || delResp.Data.FriendUid != playerB {
		t.Errorf("删除好友返回 data.friendUid = %v，期望 %d", delResp.Data, playerB)
	}

	// 步骤3：双方好友列表不再包含对方
	_, listAResp := doFriendList(t, tokenA, friendPageStart, friendPageEnd, fmt.Sprintf("friend-del-listA-%s", uuid.NewString()[:8]))
	if listAResp.Code != 0 {
		t.Fatalf("查询 A 好友列表失败：code=%d", listAResp.Code)
	}
	assertFriendListNotContains(t, listAResp, playerB)

	_, listBResp := doFriendList(t, tokenB, friendPageStart, friendPageEnd, fmt.Sprintf("friend-del-listB-%s", uuid.NewString()[:8]))
	if listBResp.Code != 0 {
		t.Fatalf("查询 B 好友列表失败：code=%d", listBResp.Code)
	}
	assertFriendListNotContains(t, listBResp, playerA)

	// 步骤4：B 可重新向 A 发送申请（删除后关系解绑）
	_, reApplyResp := doFriendApply(t, tokenB, playerA, fmt.Sprintf("friend-del-reapply-%s", uuid.NewString()[:8]))
	if reApplyResp.Code != 0 {
		t.Fatalf("删除后重新申请失败：code=%d, message=%q", reApplyResp.Code, reApplyResp.Message)
	}

	t.Logf("删除好友验证通过：playerA=%d, playerB=%d", playerA, playerB)
}

// TestFriend_Reject_RemovesPendingApply 验证拒绝申请后不建立好友关系且双方可重新申请。
func TestFriend_Reject_RemovesPendingApply(t *testing.T) {
	tokenA, playerA := loginAsGuest(t)
	tokenB, playerB := loginAsGuest(t)

	_, applyResp := doFriendApply(t, tokenA, playerB, fmt.Sprintf("friend-reject-apply-%s", uuid.NewString()[:8]))
	if applyResp.Code != 0 {
		t.Fatalf("发送申请失败：code=%d, message=%q", applyResp.Code, applyResp.Message)
	}

	httpStatus, handleResp := doFriendApplyHandle(t, tokenB, playerA, false, fmt.Sprintf("friend-reject-handle-%s", uuid.NewString()[:8]))
	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if handleResp.Code != 0 {
		t.Fatalf("拒绝申请失败：code=%d, message=%q", handleResp.Code, handleResp.Message)
	}
	if handleResp.Data == nil || handleResp.Data.Accept {
		t.Fatalf("拒绝申请返回 data.accept = %v，期望 false", handleResp.Data)
	}

	_, outResp := doFriendApplyList(t, tokenA, friendPageStart, friendPageEnd, false, fmt.Sprintf("friend-reject-out-%s", uuid.NewString()[:8]))
	if outResp.Code != 0 || outResp.Data == nil {
		t.Fatalf("查询发出申请失败：response=%+v", outResp)
	}
	for _, item := range outResp.Data.Items {
		if item.GetApplyUid() == playerA && item.GetTargetUid() == playerB {
			t.Error("拒绝后发出的申请列表仍包含该申请")
		}
	}

	_, inResp := doFriendApplyList(t, tokenB, friendPageStart, friendPageEnd, true, fmt.Sprintf("friend-reject-in-%s", uuid.NewString()[:8]))
	if inResp.Code != 0 || inResp.Data == nil {
		t.Fatalf("查询收到申请失败：response=%+v", inResp)
	}
	for _, item := range inResp.Data.Items {
		if item.GetApplyUid() == playerA && item.GetTargetUid() == playerB {
			t.Error("拒绝后收到的申请列表仍包含该申请")
		}
	}

	_, listAResp := doFriendList(t, tokenA, friendPageStart, friendPageEnd, fmt.Sprintf("friend-reject-list-a-%s", uuid.NewString()[:8]))
	if listAResp.Code != 0 {
		t.Fatalf("查询 A 好友列表失败：code=%d, message=%q", listAResp.Code, listAResp.Message)
	}
	assertFriendListNotContains(t, listAResp, playerB)

	_, listBResp := doFriendList(t, tokenB, friendPageStart, friendPageEnd, fmt.Sprintf("friend-reject-list-b-%s", uuid.NewString()[:8]))
	if listBResp.Code != 0 {
		t.Fatalf("查询 B 好友列表失败：code=%d, message=%q", listBResp.Code, listBResp.Message)
	}
	assertFriendListNotContains(t, listBResp, playerA)

	_, reApplyResp := doFriendApply(t, tokenB, playerA, fmt.Sprintf("friend-reject-reapply-%s", uuid.NewString()[:8]))
	if reApplyResp.Code != 0 {
		t.Fatalf("拒绝后反向重新申请失败：code=%d, message=%q", reApplyResp.Code, reApplyResp.Message)
	}
	t.Logf("拒绝申请状态转换验证通过：playerA=%d, playerB=%d", playerA, playerB)
}
