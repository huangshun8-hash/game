// Package tests 模拟客户端连接 GameLogic WebSocket 并验证实时推送。
// 测试目标：GET /ws 的握手、心跳与支付成功推送。
// 基准 URL 通过环境变量 TEST_BASE_URL 指定，默认 http://localhost:8081。
// WebSocket 路径可通过 TEST_WS_PATH 指定，默认 /ws。
// 支付成功推送测试要求 Platform 运行于 local 环境，使其模拟补单任务能够回调 GameLogic。
// 运行方式（需服务及依赖已启动）：
//
//	go test ./tests/... -v -run TestWS
package tests

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
)

const (
	defaultWSPath       = "/ws"
	wsFrameWait         = 5 * time.Second
	wsPaySuccessTimeout = 20 * time.Second
	wsMinUnixMilli      = int64(1_000_000_000_000)
)

// testWSPath 返回测试目标的 WebSocket 路径，默认使用服务配置的默认路径。
func testWSPath(t *testing.T) string {
	t.Helper()

	path := strings.TrimSpace(os.Getenv("TEST_WS_PATH"))
	if path == "" {
		return defaultWSPath
	}
	if !strings.HasPrefix(path, "/") {
		t.Fatalf("TEST_WS_PATH 必须以 / 开头，实际为 %q", path)
	}
	return path
}

// testWSURL 基于 HTTP 测试地址构造携带认证令牌的 WebSocket 连接地址。
func testWSURL(t *testing.T, token string) string {
	t.Helper()

	endpoint, err := url.Parse(baseURL())
	if err != nil {
		t.Fatalf("解析 TEST_BASE_URL 失败: %v", err)
	}
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	default:
		t.Fatalf("TEST_BASE_URL 协议必须为 http 或 https，实际为 %q", endpoint.Scheme)
	}
	endpoint.Path = testWSPath(t)
	query := endpoint.Query()
	query.Set("token", token)
	query.Set("platform", "test")
	endpoint.RawQuery = query.Encode()
	return endpoint.String()
}

// dialWS 建立通过 JWT 鉴权的 WebSocket 连接。
func dialWS(t *testing.T, token string) *websocket.Conn {
	t.Helper()

	wsURL := testWSURL(t, token)
	t.Logf("wsUrl:%s", wsURL)
	dialer := websocket.Dialer{HandshakeTimeout: requestTimeout}
	conn, response, err := dialer.Dial(wsURL, nil)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			t.Fatalf("建立 WebSocket 连接失败：url=%s, status=%s, err=%v", wsURL, response.Status, err)
		}
		t.Fatalf("建立 WebSocket 连接失败：url=%s, err=%v", wsURL, err)
	}
	return conn
}

// readWSFrame 读取并反序列化一条服务端 WebSocket 二进制帧。
func readWSFrame(t *testing.T, conn *websocket.Conn, deadline time.Time) *cspb.WsMessage {
	t.Helper()

	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("设置 WebSocket 读取超时失败: %v", err)
	}
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取 WebSocket 下行帧失败: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("WebSocket 下行消息类型 = %d，期望二进制帧", messageType)
	}
	frame := new(cspb.WsMessage)
	if err := proto.Unmarshal(data, frame); err != nil {
		t.Fatalf("反序列化 WebSocket 下行帧失败: %v", err)
	}
	return frame
}

// writeWSFrame 将 protobuf 消息帧以二进制格式发送给服务端。
func writeWSFrame(t *testing.T, conn *websocket.Conn, frame *cspb.WsMessage) {
	t.Helper()

	data, err := proto.Marshal(frame)
	if err != nil {
		t.Fatalf("序列化 WebSocket 上行帧失败: %v", err)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(wsFrameWait)); err != nil {
		t.Fatalf("设置 WebSocket 写入超时失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		t.Fatalf("发送 WebSocket 上行帧失败: %v", err)
	}
}

// assertWSHandshake 验证连接首帧为携带正确玩家身份的握手帧。
func assertWSHandshake(t *testing.T, conn *websocket.Conn, playerID int64) {
	t.Helper()

	frame := readWSFrame(t, conn, time.Now().Add(wsFrameWait))
	if frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧 type = %s，期望 WsMsgTypeHandshake", frame.Type)
	}
	info := new(cspb.WsHandshakeInfo)
	if err := proto.Unmarshal(frame.Data, info); err != nil {
		t.Fatalf("反序列化握手负载失败: %v", err)
	}
	if info.PlayerId != playerID {
		t.Errorf("握手 playerId = %d，期望 %d", info.PlayerId, playerID)
	}
	if info.ServerTime < wsMinUnixMilli {
		t.Errorf("握手 serverTime = %d，期望 Unix 毫秒时间戳", info.ServerTime)
	}
}

// waitWSFrame 等待满足条件的下行帧，并自动应答服务端主动心跳。
func waitWSFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration, matched func(*cspb.WsMessage) bool) *cspb.WsMessage {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		frame := readWSFrame(t, conn, deadline)
		switch frame.Type {
		case cspb.WsMsgType_WsMsgTypeHeartbeat:
			writeWSFrame(t, conn, &cspb.WsMessage{
				Type:      cspb.WsMsgType_WsMsgTypeHeartbeatAck,
				Timestamp: time.Now().UnixMilli(),
			})
			continue
		case cspb.WsMsgType_WsMsgTypeError:
			wsError := new(cspb.WsErrorInfo)
			if err := proto.Unmarshal(frame.Data, wsError); err != nil {
				t.Fatalf("反序列化 WebSocket 错误帧失败: %v", err)
			}
			t.Fatalf("服务端返回 WebSocket 错误帧：code=%d, message=%q", wsError.Code, wsError.Message)
		}
		if matched(frame) {
			return frame
		}
		t.Logf("忽略非目标 WebSocket 帧：type=%s, bizId=%q, msgId=%s", frame.Type, frame.BizId, frame.MsgId)
	}
}

// TestWS_HandshakeAndHeartbeat 验证认证建连、握手负载与客户端主动心跳应答。
func TestWS_HandshakeAndHeartbeat1(t *testing.T) {
	token, playerID := loginAsGuest(t)
	conn := dialWS(t, token)
	defer func() { _ = conn.Close() }()

	assertWSHandshake(t, conn, playerID)

	senderTime := time.Now().UnixMilli()
	heartbeatData, err := proto.Marshal(&cspb.WsHeartbeatReq{SenderTime: senderTime})
	if err != nil {
		t.Fatalf("序列化心跳负载失败: %v", err)
	}
	writeWSFrame(t, conn, &cspb.WsMessage{
		Type:      cspb.WsMsgType_WsMsgTypeHeartbeat,
		Data:      heartbeatData,
		Timestamp: senderTime,
	})

	ack := waitWSFrame(t, conn, wsFrameWait, func(frame *cspb.WsMessage) bool {
		return frame.Type == cspb.WsMsgType_WsMsgTypeHeartbeatAck
	})
	ackData := new(cspb.WsHeartbeatResp)
	if err := proto.Unmarshal(ack.Data, ackData); err != nil {
		t.Fatalf("反序列化心跳应答负载失败: %v", err)
	}
	if ackData.ServerTime < wsMinUnixMilli {
		t.Errorf("心跳应答 serverTime = %d，期望 Unix 毫秒时间戳", ackData.ServerTime)
	}
	t.Logf("WebSocket 握手与心跳验证通过：playerId=%d, serverTime=%d", playerID, ackData.ServerTime)
}

// TestWS_PaySuccessPush 验证本地模拟支付回调后在线玩家能收到支付成功推送。
func TestWS_PaySuccessPush(t *testing.T) {
	token, playerID := loginAsGuest(t)
	conn := dialWS(t, token)
	defer func() { _ = conn.Close() }()

	assertWSHandshake(t, conn, playerID)

	createReq := &cspb.CreateOrderReq{
		PayId:    "cookiecircle99",
		Channel:  "apple",
		AwardTag: "wsPaySuccess",
	}
	httpStatus, createResp := doCreateOrder(t, token, createReq, fmt.Sprintf("ws-pay-create-%s", uuid.NewString()[:8]))
	if httpStatus != 200 {
		t.Fatalf("创建订单 HTTP 状态码 = %d，期望 200", httpStatus)
	}
	if createResp.Code != 0 || createResp.Data == nil || createResp.Data.OrderId == "" {
		t.Fatalf("创建订单失败：response=%+v", createResp)
	}
	orderID := createResp.Data.OrderId

	pushFrame := waitWSFrame(t, conn, wsPaySuccessTimeout, func(frame *cspb.WsMessage) bool {
		return frame.Type == cspb.WsMsgType_WsMsgTypePush && frame.BizId == orderID
	})
	if pushFrame.MsgId != cspb.WsMsgId_WsMsgIdPaySuccessPush {
		t.Fatalf("推送 msgId = %s，期望 WsMsgIdPaySuccessPush", pushFrame.MsgId)
	}
	if pushFrame.Timestamp < wsMinUnixMilli {
		t.Errorf("推送 timestamp = %d，期望 Unix 毫秒时间戳", pushFrame.Timestamp)
	}

	pushData := new(cspb.PaySuccessPush)
	if err := proto.Unmarshal(pushFrame.Data, pushData); err != nil {
		t.Fatalf("反序列化支付成功推送负载失败: %v", err)
	}
	if pushData.OrderId != orderID {
		t.Errorf("推送 orderId = %q，期望 %q", pushData.OrderId, orderID)
	}
	if pushData.PayId != createReq.PayId {
		t.Errorf("推送 payId = %q，期望 %q", pushData.PayId, createReq.PayId)
	}
	if pushData.Channel != createReq.Channel {
		t.Errorf("推送 channel = %q，期望 %q", pushData.Channel, createReq.Channel)
	}
	if pushData.AwardTag != createReq.AwardTag {
		t.Errorf("推送 awardTag = %q，期望 %q", pushData.AwardTag, createReq.AwardTag)
	}
	if pushData.PaidAt < wsMinUnixMilli {
		t.Errorf("推送 paidAt = %d，期望 Unix 毫秒时间戳", pushData.PaidAt)
	}
	t.Logf("支付成功 WebSocket 推送验证通过：playerId=%d, orderId=%q", playerID, orderID)
}

// TestWS_KickedOfflineByNewDevice 端到端验证严格单端：
// 新设备登录后，旧端先收到 KickedOfflinePush（携带新登录端 IP/设备ID），再以 4002 断开；
// 旧 Token 重新握手返回独立错误码 10402，与令牌过期的 10401 区分。
func TestWS_KickedOfflineByNewDevice(t *testing.T) {
	guestUUID := uuid.NewString()
	deviceA := "ws-kick-a-" + uuid.NewString()[:8]
	deviceB := "ws-kick-b-" + uuid.NewString()[:8]

	login := func(deviceID string) *cspb.LoginResp {
		req := &cspb.LoginReq{
			LoginType:  "guest",
			Platform:   "ios",
			Uuid:       guestUUID,
			ClientInfo: newTestClientInfoWithDevice(deviceID),
		}
		_, resp := doLogin(t, req, fmt.Sprintf("ws-kick-%s", uuid.NewString()[:8]))
		if resp.Code != 0 || resp.Data == nil {
			t.Fatalf("登录失败：code=%d, message=%q", resp.Code, resp.Message)
		}
		return resp
	}

	first := login(deviceA)
	conn := dialWS(t, first.Data.Token)
	defer func() { _ = conn.Close() }()
	assertWSHandshake(t, conn, first.Data.PlayerId)

	// 新设备登录：旧端应被顶号
	_ = login(deviceB)

	notice := waitWSFrame(t, conn, wsFrameWait, func(frame *cspb.WsMessage) bool {
		return frame.MsgId == cspb.WsMsgId_WsMsgIdKickedOfflinePush
	})
	payload := new(cspb.KickedOfflinePush)
	if err := proto.Unmarshal(notice.Data, payload); err != nil {
		t.Fatalf("反序列化顶号通知负载失败: %v", err)
	}
	if payload.DeviceId != deviceB {
		t.Errorf("顶号通知 deviceId = %q，期望 %q", payload.DeviceId, deviceB)
	}
	if payload.Ip == "" {
		t.Error("顶号通知 ip 为空，期望携带新登录端客户端 IP")
	}
	if notice.BizId == "" {
		t.Error("顶号通知 bizId 为空，期望复用登录请求ID")
	}

	// 旧连接随后以 4002 关闭
	_ = conn.SetReadDeadline(time.Now().Add(wsFrameWait))
	var readErr error
	for readErr == nil {
		_, _, readErr = conn.ReadMessage()
	}
	if !websocket.IsCloseError(readErr, 4002) {
		t.Fatalf("旧连接应收到关闭码 4002，实际错误为 %v", readErr)
	}

	// 旧 Token 重新握手：升级成功，随后以 4002 补发顶号通知（客户端离线时收不到原本的 4002）。
	// 浏览器 WebSocket API 读不到握手失败（非 101）的响应体，故顶号结论只能靠 Close 码送达。
	replacedConn := dialWS(t, first.Data.Token)
	defer func() { _ = replacedConn.Close() }()

	_ = replacedConn.SetReadDeadline(time.Now().Add(wsFrameWait))
	var closeErr error
	for closeErr == nil {
		_, _, closeErr = replacedConn.ReadMessage()
	}
	if !websocket.IsCloseError(closeErr, 4002) {
		t.Fatalf("旧 Token 连接应以关闭码 4002 断开，实际错误为 %v", closeErr)
	}
}
