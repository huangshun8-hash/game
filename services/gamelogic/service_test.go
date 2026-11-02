package main

import (
	"net"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/wsx"
	"kemiluo/services/shared/authdto"
)

// sessionKickTestPlayerID 顶号订阅测试使用的玩家ID
const sessionKickTestPlayerID int64 = 94001

// startSessionKickTestServer 启动仅挂载 WebSocket 端点的测试服务，供顶号订阅逻辑使用。
// 返回 Hub 与连接地址前缀（地址前缀已带 platform 与 token 参数）。
func startSessionKickTestServer(t *testing.T) (*wsx.Hub, string) {
	t.Helper()

	cfg := wsx.DefaultConfig()
	cfg.EnableCompression = false
	cfg.ReadWait = 5 * time.Second
	cfg.HandshakeTimeout = 2 * time.Second

	hub := wsx.NewHub()
	app := fiber.New()
	// 固定返回同一玩家ID：测试关注顶号过滤，不校验真实 JWT
	validate := func(c fiber.Ctx, token string) (string, error) {
		return "94001", nil
	}
	wsx.Register(app, "/ws", hub, cfg, &wsx.HandshakeAuth{Validate: validate})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听测试端口失败: %v", err)
	}
	go func() {
		_ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	t.Cleanup(func() { _ = app.Shutdown() })

	return hub, "ws://" + ln.Addr().String() + "/ws?platform=test&token="
}

// dialSessionKickConn 建立测试连接并消费握手帧，返回可用连接
func dialSessionKickConn(t *testing.T, baseURL, token string) *websocket.Conn {
	t.Helper()

	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.Dial(baseURL+token, nil)
	if err != nil {
		t.Fatalf("建立测试连接失败: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取握手帧失败: %v", err)
	}
	var frame cspb.WsMessage
	if err := proto.Unmarshal(data, &frame); err != nil {
		t.Fatalf("解析握手帧失败: %v", err)
	}
	if frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为握手帧，实际为 %s", frame.Type.String())
	}
	return conn
}

// waitSessionKickConnections 轮询等待在线连接数达到期望值
func waitSessionKickConnections(t *testing.T, hub *wsx.Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Stats().TotalConnections == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待在线连接数为 %d 超时，当前为 %d", want, hub.Stats().TotalConnections)
}

// TestHandleSessionKickedMessage 校验顶号广播订阅端的两个关键过滤：
//  1. 跳过本实例发出的消息（本地已在登录链路完成通知与断开）；
//  2. 跳过令牌摘要与广播一致的新登录端连接，只踢旧 Token 的连接。
func TestHandleSessionKickedMessage(t *testing.T) {
	hub, baseURL := startSessionKickTestServer(t)
	svc := &gamelogicService{hub: hub, instanceID: "inst-a"}

	// 场景一：自实例广播应被跳过，不影响任何连接
	selfConn := dialSessionKickConn(t, baseURL, "self-token")
	waitSessionKickConnections(t, hub, 1)

	selfBody, err := sonic.Marshal(&authdto.SessionKickedMessage{
		PlayerID:       sessionKickTestPlayerID,
		OriginInstance: "inst-a",
		RequestID:      "self-origin",
	})
	if err != nil {
		t.Fatalf("序列化自实例广播失败: %v", err)
	}
	svc.handleSessionKickedMessage(string(selfBody))
	if got := hub.Stats().TotalConnections; got != 1 {
		t.Fatalf("自实例广播不应断开连接，实际在线 %d", got)
	}
	_ = selfConn.Close()
	waitSessionKickConnections(t, hub, 0)

	// 场景二：其它实例广播，只踢旧 Token 连接，保留新 Token 连接
	oldConn := dialSessionKickConn(t, baseURL, "old-token")
	defer func() { _ = oldConn.Close() }()
	newConn := dialSessionKickConn(t, baseURL, "new-token")
	defer func() { _ = newConn.Close() }()
	waitSessionKickConnections(t, hub, 2)

	remoteBody, err := sonic.Marshal(&authdto.SessionKickedMessage{
		PlayerID:       sessionKickTestPlayerID,
		LoginAt:        time.Now().UnixMilli(),
		Platform:       "android",
		DeviceName:     "Pixel 9",
		DeviceID:       "device-new",
		IP:             "198.51.100.9",
		TokenDigest:    wsx.TokenDigest("new-token"),
		OriginInstance: "inst-b",
		RequestID:      "remote-origin",
	})
	if err != nil {
		t.Fatalf("序列化跨实例广播失败: %v", err)
	}
	svc.handleSessionKickedMessage(string(remoteBody))

	// 旧连接：先收到携带新登录端信息的通知帧
	_ = oldConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := oldConn.ReadMessage()
	if err != nil {
		t.Fatalf("读取顶号通知帧失败: %v", err)
	}
	var notice cspb.WsMessage
	if err := proto.Unmarshal(data, &notice); err != nil {
		t.Fatalf("解析顶号通知帧失败: %v", err)
	}
	if notice.MsgId != cspb.WsMsgId_WsMsgIdKickedOfflinePush {
		t.Fatalf("通知帧 msgId 应为 KickedOfflinePush，实际为 %s", notice.MsgId.String())
	}
	var offline cspb.KickedOfflinePush
	if err := proto.Unmarshal(notice.Data, &offline); err != nil {
		t.Fatalf("解析顶号通知负载失败: %v", err)
	}
	if offline.DeviceId != "device-new" || offline.Ip != "198.51.100.9" {
		t.Fatalf("顶号通知应携带新登录端设备与 IP，实际为 %+v", &offline)
	}

	// 旧连接随后以 4002 关闭
	var readErr error
	for readErr == nil {
		_, _, readErr = oldConn.ReadMessage()
	}
	if !websocket.IsCloseError(readErr, wsx.CloseCodeConnReplaced) {
		t.Fatalf("旧连接应收到关闭码 %d，实际错误为 %v", wsx.CloseCodeConnReplaced, readErr)
	}

	// 新连接必须保留，且仍能收到推送
	waitSessionKickConnections(t, hub, 1)
	result := hub.PushToPlayer(sessionKickTestPlayerID, &wsx.PushPayload{
		BizID: "probe",
		Data:  &cspb.KickedOfflinePush{},
	})
	if result.Online != 1 || result.Delivered != 1 {
		t.Fatalf("新连接应仍然在线，实际 online=%d delivered=%d", result.Online, result.Delivered)
	}
	_ = newConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, probeData, err := newConn.ReadMessage()
	if err != nil {
		t.Fatalf("新连接应能收到推送，实际错误为 %v", err)
	}
	var probe cspb.WsMessage
	if err := proto.Unmarshal(probeData, &probe); err != nil {
		t.Fatalf("解析探测推送失败: %v", err)
	}
	if probe.BizId != "probe" {
		t.Fatalf("新连接应收到探测推送，实际 bizId=%q", probe.BizId)
	}
}
