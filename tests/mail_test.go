// Package tests 模拟客户端对 GameLogic 服务邮件系统接口的集成测试。
// 测试目标：
//
//	GET  /api/mail/list       查询邮件列表
//	POST /api/mail/read       阅读邮件
//	POST /api/mail/receive    领取单个邮件附件
//	POST /api/mail/receiveAll 一键领取全部邮件附件
//	POST /api/mail/delete     删除已读邮件
//
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// 本文件只做正向流程验证，均基于新建账号的无邮件场景（服务端系统邮件模板懒创建不依赖外部注入）。
// 运行方式（需服务已启动）：
//
//	go test ./tests/... -v -run TestMail
package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"kemiluo/pb/cspb"
)

// ---- 路径常量 ----

const (
	mailListPath       = "/api/mail/list"
	mailReadPath       = "/api/mail/read"
	mailReceivePath    = "/api/mail/receive"
	mailReceiveAllPath = "/api/mail/receiveAll"
	mailDeletePath     = "/api/mail/delete"
)

// 邮件列表默认查询窗口（起始索引从0开始，闭区间）
const (
	mailListStart = 0
	mailListEnd   = 19
)

// ---- 辅助函数 ----

// doMailPost 发送邮件相关 POST 请求（protobuf 格式），返回 HTTP 状态码和响应体字节。
// 连接失败时调用 t.Skipf 跳过测试，只能在测试主 goroutine 中调用。
func doMailPost(t *testing.T, path string, token string, body []byte, requestID string) (int, []byte) {
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

// doMailGet 发送邮件相关 GET 请求（protobuf 期望响应），返回 HTTP 状态码和响应体字节。
// 查询参数通过 query 指定；连接失败时调用 t.Skipf 跳过测试。
func doMailGet(t *testing.T, path string, token string, query url.Values, requestID string) (int, []byte) {
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
	return resp.StatusCode, respBytes
}

// doMailList 发送邮件列表查询请求，返回 HTTP 状态码和响应。
func doMailList(t *testing.T, token string, start, end int64, requestID string) (int, *cspb.MailListResp) {
	t.Helper()
	query := url.Values{}
	query.Set("start", fmt.Sprintf("%d", start))
	query.Set("end", fmt.Sprintf("%d", end))
	httpStatus, respBytes := doMailGet(t, mailListPath, token, query, requestID)
	var result cspb.MailListResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doMailReceiveAll 发送一键领取全部附件请求，返回 HTTP 状态码和响应。
// MailReceiveAllReq 无业务字段，发送空请求体即可。
func doMailReceiveAll(t *testing.T, token string, requestID string) (int, *cspb.MailReceiveAllResp) {
	t.Helper()
	httpStatus, respBytes := doMailPost(t, mailReceiveAllPath, token, nil, requestID)
	var result cspb.MailReceiveAllResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doMailDelete 发送删除已读邮件请求，返回 HTTP 状态码和响应。
// MailDeleteReq 无业务字段，发送空请求体即可。
func doMailDelete(t *testing.T, token string, requestID string) (int, *cspb.MailDeleteResp) {
	t.Helper()
	httpStatus, respBytes := doMailPost(t, mailDeletePath, token, nil, requestID)
	var result cspb.MailDeleteResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// ---- 邮件测试 ----

// TestMail_List_Success 邮件列表查询正向流程：
// 新建账号查询列表，期望返回 code=0 且 data 字段存在；
// 若服务端存在懒创建的系统邮件，则每条邮件应包含合法的 mailId、标题与时间戳。
func TestMail_List_Success(t *testing.T) {
	token, playerID := loginAsGuest(t)

	httpStatus, resp := doMailList(t, token, mailListStart, mailListEnd, fmt.Sprintf("mail-list-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("邮件列表 data 字段为 nil")
	}
	for _, mail := range resp.Data.Mails {
		if mail.GetMailId() <= 0 {
			t.Errorf("邮件 mailId = %d，期望 > 0", mail.GetMailId())
		}
		if mail.GetTitle() == nil {
			t.Errorf("邮件 mailId=%d 缺少标题", mail.GetMailId())
		}
		if mail.GetCreateTime() <= 0 {
			t.Errorf("邮件 mailId=%d 创建时间异常：%d", mail.GetMailId(), mail.GetCreateTime())
		}
	}
	t.Logf("邮件列表查询成功：playerId=%d, 邮件数量=%d", playerID, len(resp.Data.Mails))
}

// TestMail_ReceiveAll_Success 一键领取附件正向流程：
// 新建账号调用 receiveAll，期望返回 code=0 且 data 字段存在；count 非负即视为成功。
func TestMail_ReceiveAll_Success(t *testing.T) {
	token, playerID := loginAsGuest(t)

	httpStatus, resp := doMailReceiveAll(t, token, fmt.Sprintf("mail-receive-all-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		t.Fatal("一键领取返回 data 字段为 nil")
	}
	if resp.Data.Count < 0 {
		t.Errorf("领取数量 count = %d，期望 >= 0", resp.Data.Count)
	}
	// 有成功领取邮件时，应同时返回奖励列表（无奖励邮件不会计入领取数量）
	if resp.Data.Count > 0 && len(resp.Data.Rewards) == 0 {
		t.Errorf("count=%d 但 rewards 为空", resp.Data.Count)
	}
	t.Logf("一键领取附件成功：playerId=%d, count=%d, rewards=%d",
		playerID, resp.Data.Count, len(resp.Data.Rewards))
}

// TestMail_Delete_Success 删除已读邮件正向流程：
// 新建账号调用 delete，期望返回 code=0 且 deleted 字段存在（顶层字段，非负）。
func TestMail_Delete_Success(t *testing.T) {
	token, playerID := loginAsGuest(t)

	httpStatus, resp := doMailDelete(t, token, fmt.Sprintf("mail-delete-%s", uuid.NewString()[:8]))

	if httpStatus != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if resp.Code != 0 {
		t.Fatalf("业务错误码 = %d，message = %q，期望 code=0", resp.Code, resp.Message)
	}
	if resp.Deleted < 0 {
		t.Errorf("删除数量 deleted = %d，期望 >= 0", resp.Deleted)
	}
	t.Logf("删除已读邮件成功：playerId=%d, deleted=%d", playerID, resp.Deleted)
}

// TestMail_FullFlow 邮件完整正向流程：
// 列表查询 → 一键领取 → 再次列表查询 → 删除已读邮件，各环节均期望 code=0。
func TestMail_FullFlow(t *testing.T) {
	token, playerID := loginAsGuest(t)

	// 步骤1：查询邮件列表
	_, listResp := doMailList(t, token, mailListStart, mailListEnd, fmt.Sprintf("mail-flow-list-%s", uuid.NewString()[:8]))
	if listResp.Code != 0 {
		t.Fatalf("查询邮件列表失败：code=%d, message=%q", listResp.Code, listResp.Message)
	}
	if listResp.Data == nil {
		t.Fatal("邮件列表 data 字段为 nil")
	}
	beforeCount := len(listResp.Data.Mails)
	t.Logf("步骤1-查询邮件列表成功：数量=%d", beforeCount)

	// 步骤2：一键领取全部附件
	_, receiveResp := doMailReceiveAll(t, token, fmt.Sprintf("mail-flow-receive-%s", uuid.NewString()[:8]))
	if receiveResp.Code != 0 {
		t.Fatalf("一键领取失败：code=%d, message=%q", receiveResp.Code, receiveResp.Message)
	}
	if receiveResp.Data == nil || receiveResp.Data.Count < 0 {
		t.Errorf("一键领取返回值异常：data=%v", receiveResp.Data)
	}
	t.Logf("步骤2-一键领取成功：count=%d", receiveResp.Data.GetCount())

	// 步骤3：再次查询列表（领取后接口仍应正常返回）
	_, listAgainResp := doMailList(t, token, mailListStart, mailListEnd, fmt.Sprintf("mail-flow-list2-%s", uuid.NewString()[:8]))
	if listAgainResp.Code != 0 {
		t.Fatalf("领取后再次查询列表失败：code=%d, message=%q", listAgainResp.Code, listAgainResp.Message)
	}
	if listAgainResp.Data == nil {
		t.Fatal("再次查询邮件列表 data 字段为 nil")
	}
	t.Logf("步骤3-领取后再查列表成功：数量=%d", len(listAgainResp.Data.Mails))

	// 步骤4：删除已读邮件
	_, deleteResp := doMailDelete(t, token, fmt.Sprintf("mail-flow-delete-%s", uuid.NewString()[:8]))
	if deleteResp.Code != 0 {
		t.Fatalf("删除已读邮件失败：code=%d, message=%q", deleteResp.Code, deleteResp.Message)
	}
	if deleteResp.Deleted < 0 {
		t.Errorf("删除数量 deleted = %d，期望 >= 0", deleteResp.Deleted)
	}
	t.Logf("步骤4-删除已读邮件成功：deleted=%d，playerId=%d", deleteResp.Deleted, playerID)
}

// doMailRead 发送单封邮件阅读请求，返回 HTTP 状态码和响应。
func doMailRead(t *testing.T, token string, mailID int64, requestID string) (int, *cspb.MailReadResp) {
	t.Helper()
	body := marshalProtoReq(t, token, &cspb.MailReadReq{MailId: mailID})
	httpStatus, respBytes := doMailPost(t, mailReadPath, token, body, requestID)
	var result cspb.MailReadResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// doMailReceive 发送单封邮件附件领取请求，返回 HTTP 状态码和响应。
func doMailReceive(t *testing.T, token string, mailID int64, requestID string) (int, *cspb.MailReceiveResp) {
	t.Helper()
	body := marshalProtoReq(t, token, &cspb.MailReceiveReq{MailId: mailID})
	httpStatus, respBytes := doMailPost(t, mailReceivePath, token, body, requestID)
	var result cspb.MailReceiveResp
	unmarshalProtoResp(t, token, respBytes, &result)
	return httpStatus, &result
}

// TestMail_ReadAndReceive_NotFound 验证单封阅读与领取对不存在邮件返回业务错误。
func TestMail_ReadAndReceive_NotFound(t *testing.T) {
	token, playerID := loginAsGuest(t)
	mailID := int64(math.MaxInt64)

	readStatus, readResp := doMailRead(t, token, mailID, fmt.Sprintf("mail-read-missing-%s", uuid.NewString()[:8]))
	if readStatus != http.StatusOK {
		t.Errorf("阅读邮件 HTTP 状态码 = %d，期望 200", readStatus)
	}
	if readResp.Code == 0 {
		t.Fatalf("不存在邮件的阅读请求意外成功：response=%+v", readResp)
	}
	if readResp.Data != nil {
		t.Errorf("阅读失败响应不应包含 data：%+v", readResp.Data)
	}

	receiveStatus, receiveResp := doMailReceive(t, token, mailID, fmt.Sprintf("mail-receive-missing-%s", uuid.NewString()[:8]))
	if receiveStatus != http.StatusOK {
		t.Errorf("领取邮件 HTTP 状态码 = %d，期望 200", receiveStatus)
	}
	if receiveResp.Code == 0 {
		t.Fatalf("不存在邮件的领取请求意外成功：response=%+v", receiveResp)
	}
	if receiveResp.Data != nil {
		t.Errorf("领取失败响应不应包含 data：%+v", receiveResp.Data)
	}
	t.Logf("不存在邮件异常路径验证通过：playerId=%d, mailId=%d", playerID, mailID)
}
