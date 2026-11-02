package wsx

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	// websocket 为 fasthttp 版实现，此处用作测试客户端；
	// fiberws 为服务端升级中间件（与生产代码同款），用于自建端点以观察会话收尾时序
	"github.com/fasthttp/websocket"
	fiberws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
)

// testEnv 单个测试用的 WebSocket 服务端环境
type testEnv struct {
	hub  *Hub
	url  string
	stop func()
}

// newTestConfig 返回适合测试的连接配置：缩短各类超时，避免测试等待过久
func newTestConfig() Config {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 50 * time.Millisecond
	cfg.ReadWait = 2 * time.Second
	cfg.WriteWait = time.Second
	cfg.HandshakeTimeout = 2 * time.Second
	// 压缩会引入额外的后台状态，测试聚焦会话生命周期，关闭以减少干扰
	cfg.EnableCompression = false
	return cfg
}

// startTestServer 启动仅挂载 WebSocket 端点的 Fiber 应用。
// 参数：cfg 连接配置；authUserID 握手鉴权返回的玩家ID字符串（用于构造非法ID场景）
func startTestServer(t *testing.T, cfg Config, authUserID string) *testEnv {
	t.Helper()

	hub := NewHub()
	app := fiber.New()
	validate := func(c fiber.Ctx, token string) (string, error) {
		return authUserID, nil
	}
	Register(app, "/ws", hub, cfg, &HandshakeAuth{Validate: validate})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听测试端口失败: %v", err)
	}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()

	env := &testEnv{
		hub: hub,
		url: "ws://" + ln.Addr().String() + "/ws?token=t&platform=test",
		stop: func() {
			_ = app.Shutdown()
			<-serveDone
		},
	}
	t.Cleanup(env.stop)
	return env
}

// dial 建立一条测试客户端连接
func dial(t *testing.T, env *testEnv) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.Dial(env.url, nil)
	if err != nil {
		t.Fatalf("建立测试连接失败: %v", err)
	}
	return conn
}

// dialWithToken 使用指定 token 建立测试连接，用于构造「同玩家不同登录会话」场景
func dialWithToken(t *testing.T, env *testEnv, token string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	wsURL := strings.Replace(env.url, "token=t", "token="+token, 1)
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("建立测试连接失败: %v", err)
	}
	return conn
}

// readFrame 读取一条服务端下行帧并解析
func readFrame(t *testing.T, conn *websocket.Conn) *cspb.WsMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取下行帧失败: %v", err)
	}
	frame := &cspb.WsMessage{}
	if err := proto.Unmarshal(data, frame); err != nil {
		t.Fatalf("解析下行帧失败: %v", err)
	}
	return frame
}

// writeFrame 向服务端发送一条上行帧
func writeFrame(t *testing.T, conn *websocket.Conn, frame *cspb.WsMessage) {
	t.Helper()
	data, err := proto.Marshal(frame)
	if err != nil {
		t.Fatalf("编码上行帧失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		t.Fatalf("发送上行帧失败: %v", err)
	}
}

// waitStats 轮询等待在线统计满足期望，避免依赖固定 sleep 造成偶发失败
func waitStats(t *testing.T, hub *Hub, wantConnections int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Stats().TotalConnections == wantConnections {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待在线连接数为 %d 超时，当前为 %d", wantConnections, hub.Stats().TotalConnections)
}

// TestSessionWelcomeFrame 校验建连后服务端下发欢迎帧且携带正确的玩家ID
func TestSessionWelcomeFrame(t *testing.T) {
	env := startTestServer(t, newTestConfig(), "10001")
	conn := dial(t, env)
	defer func() { _ = conn.Close() }()

	frame := readFrame(t, conn)
	if frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为欢迎帧，实际为 %s", frame.Type)
	}
	welcome := &cspb.WsHandshakeInfo{}
	if err := proto.Unmarshal(frame.Data, welcome); err != nil {
		t.Fatalf("解析欢迎帧负载失败: %v", err)
	}
	if welcome.PlayerId != 10001 {
		t.Fatalf("欢迎帧玩家ID应为 10001，实际为 %d", welcome.PlayerId)
	}
}

// TestPushDuringAbruptDisconnect 是核心回归测试：客户端在服务端持续推送期间突然断开。
//
// 修复前 Run 不等待写协程退出，Fiber 会在 Run 返回后将 *websocket.Conn 置空并归还对象池，
// 在途写入随即命中 nil 底层连接（进程崩溃）或写入被复用后属于其他连接的对象（数据串号）。
// 该测试在 -race 下反复制造「推送 + 断开」竞态，用于守住这一修复。
func TestPushDuringAbruptDisconnect(t *testing.T) {
	env := startTestServer(t, newTestConfig(), "20001")

	for round := 0; round < 30; round++ {
		conn := dial(t, env)
		waitStats(t, env.hub, 1)

		var wg sync.WaitGroup
		stop := make(chan struct{})
		// 多个协程并发推送，模拟业务侧广播与定向推送同时进行
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					env.hub.PushToPlayer(20001, &PushPayload{Data: []byte("payload"), BizID: "b1"})
					env.hub.PushToPlayers([]int64{20001}, &PushPayload{Data: []byte("batch")})
				}
			}()
		}

		// 推送进行中直接断开底层 TCP 连接（不发 Close 帧），触发读协程报错退出
		time.Sleep(5 * time.Millisecond)
		_ = conn.UnderlyingConn().Close()
		_ = conn.Close()

		// 会话必须被完整回收，不能残留幽灵会话
		waitStats(t, env.hub, 0)
		close(stop)
		wg.Wait()
	}
}

// isClosedChan 非阻塞判断信号通道是否已关闭
func isClosedChan(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TestRunWaitsWritePumpBeforeReturn 直接校验 Run 的收尾契约：返回时写协程必须已退出。
//
// 该契约是必需的，因为 Fiber 的 websocket 中间件在升级回调返回后会执行 releaseConn，
// 把 *websocket.Conn 的底层连接置为 nil 并将对象归还 sync.Pool。若此时还有写入在途，
// 要么命中 nil 底层连接使进程崩溃，要么写进被复用后属于其他玩家的连接造成数据串号。
//
// 测试构造出「写协程确定仍在运行」的场景：预先灌满下行缓冲，且客户端从不读取，
// 写协程因 TCP 背压阻塞在真实写入上；随后客户端突然断开触发会话收尾。
// 修复前 Run 会在写协程尚未 unwind 时就返回，violated 被置位。
func TestRunWaitsWritePumpBeforeReturn(t *testing.T) {
	cfg := newTestConfig()
	// 拉长写超时，确保写协程是被 TCP 背压真实阻塞，而不是很快因超时返回
	cfg.WriteWait = 3 * time.Second
	cfg.HeartbeatInterval = time.Second

	hub := NewHub()
	app := fiber.New()

	var violated atomic.Bool
	handlerDone := make(chan struct{})

	// 不复用 Register：需要在 Run 返回后立刻观察写协程状态，并复刻 releaseConn 的置空行为
	app.Get("/wsraw", fiberws.New(func(conn *fiberws.Conn) {
		defer close(handlerDone)

		s := newSession(conn, hub, cfg, 90001, "test", "127.0.0.1", "conn-1", TokenDigest("test-token"))
		hub.register(s)

		// 预先灌满下行缓冲；客户端从不读取，写协程启动后将阻塞在真实的 TCP 写入上
		payload := make([]byte, 32*1024)
		frame, err := proto.Marshal(&cspb.WsMessage{
			Type:      cspb.WsMsgType_WsMsgTypePush,
			Data:      payload,
			Timestamp: time.Now().UnixMilli(),
		})
		if err != nil {
			t.Errorf("编码测试推送帧失败: %v", err)
			return
		}
		for i := 0; i < cap(s.send); i++ {
			if !s.enqueue(frame) {
				break
			}
		}

		s.Run(nil)

		// Run 已返回：Fiber 紧接着就会执行 releaseConn，此刻写协程必须已经退出
		if !isClosedChan(s.writeDone) {
			violated.Store(true)
		}
		// 复刻 releaseConn 的关键动作：置空底层连接。
		// 若仍有写入在途，此后的任何写入都会因 nil 底层连接直接 panic。
		conn.Conn = nil
		time.Sleep(150 * time.Millisecond)
	}, fiberws.Config{
		HandshakeTimeout: cfg.HandshakeTimeout,
		ReadBufferSize:   cfg.ReadBufferSize,
		WriteBufferSize:  cfg.WriteBufferSize,
		AllowEmptyOrigin: true,
	}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听测试端口失败: %v", err)
	}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	t.Cleanup(func() {
		_ = app.Shutdown()
		<-serveDone
	})

	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.Dial("ws://"+ln.Addr().String()+"/wsraw", nil)
	if err != nil {
		t.Fatalf("建立测试连接失败: %v", err)
	}

	// 等待写协程被背压阻塞，随后突然断开底层 TCP 连接（客户端全程不读取任何数据）
	time.Sleep(200 * time.Millisecond)
	_ = conn.UnderlyingConn().Close()

	select {
	case <-handlerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("等待升级回调结束超时")
	}
	if violated.Load() {
		t.Fatal("Run 返回时写协程尚未退出：底层连接归还池后仍可能被写入（崩溃/数据串号）")
	}
	if got := hub.Stats().TotalConnections; got != 0 {
		t.Fatalf("会话未被回收，当前在线连接数为 %d", got)
	}
}

// TestBusinessHandlerPanicKeepsConnection 校验业务回调 panic 被拦截：
// 连接保持可用，且断开后会话正常回收（修复前 panic 会击穿 goroutine 顶层并绕过会话清理）
func TestBusinessHandlerPanicKeepsConnection(t *testing.T) {
	env := startTestServer(t, newTestConfig(), "30001")

	var called sync.WaitGroup
	called.Add(1)
	var once sync.Once
	env.hub.SetMessageHandler(func(playerID int64, msgType cspb.WsMsgType, bizID string, data []byte) {
		once.Do(called.Done)
		panic("业务回调故意 panic")
	})

	conn := dial(t, env)
	if frame := readFrame(t, conn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为欢迎帧，实际为 %s", frame.Type)
	}

	writeFrame(t, conn, &cspb.WsMessage{
		Type:      cspb.WsMsgType_WsMsgTypeBusiness,
		Data:      []byte("business"),
		Timestamp: time.Now().UnixMilli(),
	})
	called.Wait()

	// panic 被拦截后连接应仍然可用：发心跳并期望收到心跳应答
	writeFrame(t, conn, &cspb.WsMessage{
		Type:      cspb.WsMsgType_WsMsgTypeHeartbeat,
		Timestamp: time.Now().UnixMilli(),
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("业务回调 panic 后未能收到心跳应答，连接已不可用")
		}
		frame := readFrame(t, conn)
		if frame.Type == cspb.WsMsgType_WsMsgTypeHeartbeatAck {
			break
		}
	}

	_ = conn.Close()
	waitStats(t, env.hub, 0)
}

// TestRejectInvalidPlayerID 校验握手玩家ID非法时拒绝建连，避免所有异常连接落入 playerID=0 的会话桶。
// 拒绝只下发 Close 控制帧 + 私有状态码，不夹带业务帧，也不直接断开 TCP。
func TestRejectInvalidPlayerID(t *testing.T) {
	env := startTestServer(t, newTestConfig(), "not-a-number")
	conn := dial(t, env)
	defer func() { _ = conn.Close() }()

	// 连接必须被服务端关闭，且携带可识别的关闭状态码；拒绝期间不应下发任何业务帧
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	messageType, data, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("非法玩家ID的连接应被服务端关闭，实际收到业务帧 type=%d data=%q", messageType, string(data))
	}
	if !websocket.IsCloseError(err, CloseCodeInvalidIdentity) {
		t.Fatalf("应收到关闭码 %d 的 Close 帧，实际错误为 %v", CloseCodeInvalidIdentity, err)
	}
	if got := env.hub.Stats().TotalConnections; got != 0 {
		t.Fatalf("非法连接不应注册为会话，当前在线连接数为 %d", got)
	}
}

// TestCloseReasonIsSentToClient 校验服务端主动断开时下发带原因的 Close 控制帧。
// 客户端据此区分「被踢下线需重新登录」与「网络抖动可重连」，否则只能无脑重连。
func TestCloseReasonIsSentToClient(t *testing.T) {
	cases := []struct {
		name      string
		authID    string
		playerID  int64
		wantCode  int
		trigger   func(env *testEnv, conn *websocket.Conn)
		configure func(cfg *Config)
	}{
		{
			name:     "封禁踢人",
			authID:   "91001",
			playerID: 91001,
			wantCode: CloseCodeKicked,
			trigger: func(env *testEnv, _ *websocket.Conn) {
				env.hub.KickPlayer(91001)
			},
		},
		{
			name:      "上行限流超限",
			authID:    "91002",
			playerID:  91002,
			wantCode:  CloseCodeRateLimited,
			configure: func(cfg *Config) { cfg.MaxMessagesPerSecond = 2; cfg.InboundBurst = 2 },
			trigger: func(_ *testEnv, conn *websocket.Conn) {
				data, _ := proto.Marshal(&cspb.WsMessage{Type: cspb.WsMsgType_WsMsgTypeHeartbeat})
				for i := 0; i < 100; i++ {
					if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
						return
					}
				}
			},
		},
		{
			name:      "连接数超限被踢",
			authID:    "91003",
			playerID:  91003,
			wantCode:  CloseCodeConnLimit,
			configure: func(cfg *Config) { cfg.MaxConnectionsPerPlayer = 1 },
			trigger: func(env *testEnv, _ *websocket.Conn) {
				// 再建一条连接，触发踢除最早连接
				dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
				newer, _, err := dialer.Dial(env.url, nil)
				if err == nil {
					_ = newer.Close()
				}
			},
		},
		{
			name:     "服务停机",
			authID:   "91004",
			playerID: 91004,
			wantCode: CloseCodeServerShutdown,
			trigger: func(env *testEnv, _ *websocket.Conn) {
				env.hub.CloseAll()
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := newTestConfig()
			if c.configure != nil {
				c.configure(&cfg)
			}
			env := startTestServer(t, cfg, c.authID)
			conn := dial(t, env)
			defer func() { _ = conn.Close() }()
			waitStats(t, env.hub, 1)

			c.trigger(env, conn)

			// 持续读取直至连接关闭，断言拿到的是携带期望状态码的 Close 帧
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			var err error
			for err == nil {
				_, _, err = conn.ReadMessage()
			}
			if !websocket.IsCloseError(err, c.wantCode) {
				t.Fatalf("应收到关闭码 %d 的 Close 帧，实际错误为 %v", c.wantCode, err)
			}
		})
	}
}

// TestHeartbeatDisabledServerSendsNoProbe 校验默认配置（服务端主动心跳关闭）下，
// 服务端不会主动下发任何心跳探测帧：客户端主动心跳后仅收到应答，之后无任何下行帧。
// 守卫「心跳默认关闭」这一新默认——若写协程仍按 HeartbeatInterval 定时下发探测帧，此测试失败。
func TestHeartbeatDisabledServerSendsNoProbe(t *testing.T) {
	cfg := newTestConfig()
	cfg.HeartbeatInterval = 0
	cfg.ReadWait = 2 * time.Second
	env := startTestServer(t, cfg, "10101")
	conn := dial(t, env)
	defer func() { _ = conn.Close() }()

	if frame := readFrame(t, conn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为欢迎帧，实际为 %s", frame.Type)
	}

	// 客户端主动心跳：应收到应答
	writeFrame(t, conn, &cspb.WsMessage{Type: cspb.WsMsgType_WsMsgTypeHeartbeat, Timestamp: time.Now().UnixMilli()})
	for {
		frame := readFrame(t, conn)
		if frame.Type == cspb.WsMsgType_WsMsgTypeHeartbeatAck {
			break
		}
	}

	// 观察窗口内不应再有服务端主动下发的任何帧（心跳关闭后无探测帧）
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, data, err := conn.ReadMessage(); err == nil {
		frame := &cspb.WsMessage{}
		if uerr := proto.Unmarshal(data, frame); uerr == nil && frame.Type != cspb.WsMsgType_WsMsgTypeNone {
			t.Fatalf("心跳关闭后服务端不应主动下发帧，实际收到 %s", frame.Type)
		}
	}
}

// TestHeartbeatDisabledClientHeartbeatKeepsAlive 校验服务端主动心跳关闭时，
// 客户端周期性主动心跳足以维持连接活性（读超时 ReadWait 被持续刷新），
// 活跃连接不会被误判失联。持续时长跨越多个读超时窗口，仍能收到心跳应答即证明刷新正确。
func TestHeartbeatDisabledClientHeartbeatKeepsAlive(t *testing.T) {
	cfg := newTestConfig()
	cfg.HeartbeatInterval = 0
	cfg.ReadWait = 500 * time.Millisecond
	env := startTestServer(t, cfg, "10201")
	conn := dial(t, env)
	defer func() { _ = conn.Close() }()

	if frame := readFrame(t, conn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为欢迎帧，实际为 %s", frame.Type)
	}

	// 以短于读超时的周期持续发送客户端心跳，每轮都必须收到应答
	for i := 0; i < 8; i++ {
		time.Sleep(150 * time.Millisecond)
		writeFrame(t, conn, &cspb.WsMessage{Type: cspb.WsMsgType_WsMsgTypeHeartbeat, Timestamp: time.Now().UnixMilli()})
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("第 %d 轮心跳后连接被断开: %v", i+1, err)
			}
			frame := &cspb.WsMessage{}
			if uerr := proto.Unmarshal(data, frame); uerr == nil && frame.Type == cspb.WsMsgType_WsMsgTypeHeartbeatAck {
				break
			}
		}
	}
	// 全程约 1.2s，跨越多个 ReadWait(500ms) 窗口，连接仍存活
	if got := env.hub.Stats().TotalConnections; got != 1 {
		t.Fatalf("活跃连接不应被误判失联，当前在线连接数为 %d", got)
	}
}

// TestSilentConnectionClosedWithHeartbeatTimeout 校验默认配置（服务端主动心跳关闭）下，
// 完全静默的连接在读超时 ReadWait 后被判定失联，并以关闭码 4005 断开。
// 修复前该路径静默断开：既不下发关闭码，也无日志，客户端只能看到「连接被重置」。
func TestSilentConnectionClosedWithHeartbeatTimeout(t *testing.T) {
	cfg := newTestConfig()
	cfg.HeartbeatInterval = 0
	cfg.ReadWait = 300 * time.Millisecond
	env := startTestServer(t, cfg, "10301")
	conn := dial(t, env)
	defer func() { _ = conn.Close() }()

	if frame := readFrame(t, conn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为欢迎帧，实际为 %s", frame.Type)
	}

	// 建连后不再发送任何帧：应被判失联并以 4005 关闭码断开
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var err error
	for err == nil {
		_, _, err = conn.ReadMessage()
	}
	if !websocket.IsCloseError(err, CloseCodeHeartbeatTimeout) {
		t.Fatalf("静默连接应以关闭码 %d 断开，实际错误为 %v", CloseCodeHeartbeatTimeout, err)
	}
	waitStats(t, env.hub, 0)
}

// TestInboundBurstAllowsShortSpikeThenThrottles 校验令牌桶语义：
// 建连后允许一轮突发（桶初始为满），但长期速率被压到 MaxMessagesPerSecond 之内。
// 固定窗口实现会在窗口边界放行两倍突发，令牌桶不会。
func TestInboundBurstAllowsShortSpikeThenThrottles(t *testing.T) {
	cfg := newTestConfig()
	cfg.MaxMessagesPerSecond = 10
	cfg.InboundBurst = 5
	s := &Session{cfg: cfg, inboundTokens: float64(cfg.InboundBurst), inboundLastRefill: time.Now()}

	// 满桶允许连续 5 条，第 6 条必须被拒（同一瞬间几乎没有补充）
	for i := 0; i < cfg.InboundBurst; i++ {
		if !s.allowInbound() {
			t.Fatalf("第 %d 条突发消息应被放行", i+1)
		}
	}
	if s.allowInbound() {
		t.Fatal("超出桶容量的消息应被拒绝")
	}

	// 令牌按 10 条/秒补充：回拨补充时间 300ms 应恰好补出 3 个令牌
	s.inboundLastRefill = time.Now().Add(-300 * time.Millisecond)
	allowed := 0
	for i := 0; i < 10; i++ {
		if s.allowInbound() {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("300ms 应补充 3 个令牌，实际放行 %d 条", allowed)
	}
}

// TestInboundRateLimitDisabled 校验 MaxMessagesPerSecond <= 0 时不限流
func TestInboundRateLimitDisabled(t *testing.T) {
	cfg := newTestConfig()
	cfg.MaxMessagesPerSecond = 0
	s := &Session{cfg: cfg}
	for i := 0; i < 1000; i++ {
		if !s.allowInbound() {
			t.Fatalf("关闭限流后第 %d 条消息不应被拒绝", i+1)
		}
	}
}

// TestEvictsOldestConnectionBySeq 校验连接数超限时踢除的是最早建立的连接。
// 顺序依据 Hub 分配的单调序列号而非毫秒时间戳：同一毫秒内建立的多条连接
// 用时间戳无法排序，会让「踢最早」退化成「随机踢一条」。
func TestEvictsOldestConnectionBySeq(t *testing.T) {
	cfg := newTestConfig()
	cfg.MaxConnectionsPerPlayer = 2
	hub := NewHub()

	// 同一时间戳构造三条会话，仅序列号不同
	sameMilli := time.Now().UnixMilli()
	sessions := make([]*Session, 0, 3)
	for i := 0; i < 3; i++ {
		s := &Session{
			hub:         hub,
			cfg:         cfg,
			playerID:    99001,
			connID:      strconv.Itoa(i),
			send:        make(chan outboundMessage, cfg.SendBufferSize),
			done:        make(chan struct{}),
			writeDone:   make(chan struct{}),
			connectedAt: sameMilli,
			seq:         hub.nextSeq(),
		}
		// 无底层连接，close 路径不可用；此处只关心 Hub 的选取逻辑，故手动接管注销
		sessions = append(sessions, s)
	}

	set := map[*Session]struct{}{sessions[0]: {}, sessions[1]: {}, sessions[2]: {}}

	// 三条连接超过上限 2，应选出 seq 最小的 sessions[0] 作为踢除对象
	evict := pickEvictTarget(set, sessions[2], cfg.MaxConnectionsPerPlayer)
	if evict == nil {
		t.Fatal("超出上限时应选出踢除对象")
	}
	if evict != sessions[0] {
		t.Fatalf("应踢除最早建立的连接（seq=%d），实际选中 seq=%d", sessions[0].seq, evict.seq)
	}

	// 未超上限时不应踢除任何连接
	if got := pickEvictTarget(map[*Session]struct{}{sessions[0]: {}, sessions[1]: {}}, sessions[1], cfg.MaxConnectionsPerPlayer); got != nil {
		t.Fatalf("未超上限时不应踢除连接，实际选中 seq=%d", got.seq)
	}
	// 不限制时（limit <= 0）永不踢除
	if got := pickEvictTarget(set, sessions[2], 0); got != nil {
		t.Fatalf("limit<=0 表示不限制，不应踢除连接，实际选中 seq=%d", got.seq)
	}
}

// TestMaxConnectionsPerPlayer 校验单玩家连接数超限时踢除最早连接，在线连接数稳定在上限
func TestMaxConnectionsPerPlayer(t *testing.T) {
	cfg := newTestConfig()
	cfg.MaxConnectionsPerPlayer = 2
	env := startTestServer(t, cfg, "40001")

	conns := make([]*websocket.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		conn := dial(t, env)
		conns = append(conns, conn)
		// 逐条建连以保证 connectedAt 有明确先后顺序
		time.Sleep(10 * time.Millisecond)
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()

	waitStats(t, env.hub, 2)
}

// TestInboundRateLimit 校验上行限流生效：超过每秒上限后连接被断开
func TestInboundRateLimit(t *testing.T) {
	cfg := newTestConfig()
	cfg.MaxMessagesPerSecond = 5
	env := startTestServer(t, cfg, "50001")

	conn := dial(t, env)
	defer func() { _ = conn.Close() }()

	frame := &cspb.WsMessage{Type: cspb.WsMsgType_WsMsgTypeHeartbeat, Timestamp: time.Now().UnixMilli()}
	data, err := proto.Marshal(frame)
	if err != nil {
		t.Fatalf("编码上行帧失败: %v", err)
	}
	// 连续快速发送远超上限的消息，服务端应在窗口内断开连接
	for i := 0; i < 100; i++ {
		if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			break
		}
	}
	waitStats(t, env.hub, 0)
}

// TestKickPlayer 校验 KickPlayer 立即断开玩家全部在线连接（封禁/踢人联动依赖此行为）
func TestKickPlayer(t *testing.T) {
	env := startTestServer(t, newTestConfig(), "60001")

	conns := make([]*websocket.Conn, 0, 2)
	for i := 0; i < 2; i++ {
		conns = append(conns, dial(t, env))
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	waitStats(t, env.hub, 2)

	if closed := env.hub.KickPlayer(60001); closed != 2 {
		t.Fatalf("KickPlayer 应关闭 2 条连接，实际为 %d", closed)
	}
	waitStats(t, env.hub, 0)

	// 客户端侧应能感知连接已断开
	for _, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			_, _, err := c.ReadMessage()
			if err != nil {
				break
			}
		}
	}
}

// TestKickPlayerWithNotice 校验顶号链路：客户端必须先收到 KickedOfflinePush 通知帧，
// 随后才收到关闭码 4002。顺序错误（先断后推）会让客户端只看到「连接被顶替」而拿不到业务说明。
func TestKickPlayerWithNotice(t *testing.T) {
	env := startTestServer(t, newTestConfig(), "92001")
	conn := dial(t, env)
	defer func() { _ = conn.Close() }()
	waitStats(t, env.hub, 1)

	// 丢弃建连握手帧
	if frame := readFrame(t, conn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为握手帧，实际为 %s", frame.Type.String())
	}

	closed := env.hub.KickPlayerWithNotice(92001, &PushPayload{
		BizID: "req-kick-1",
		Data: &cspb.KickedOfflinePush{
			LoginAt:    1730000000000,
			Platform:   "ios",
			DeviceName: "iPhone 15",
			Ip:         "203.0.113.7",
			DeviceId:   "device-b",
		},
	})
	if closed != 1 {
		t.Fatalf("KickPlayerWithNotice 应处理 1 条连接，实际为 %d", closed)
	}

	// 断开前必须先收到通知帧，且 msgId / bizId 正确
	notice := readFrame(t, conn)
	if notice.Type != cspb.WsMsgType_WsMsgTypePush {
		t.Fatalf("通知帧类型应为 Push，实际为 %s", notice.Type.String())
	}
	if notice.MsgId != cspb.WsMsgId_WsMsgIdKickedOfflinePush {
		t.Fatalf("通知帧 msgId 应为 KickedOfflinePush，实际为 %s", notice.MsgId.String())
	}
	if notice.BizId != "req-kick-1" {
		t.Fatalf("通知帧 bizId 应透传登录请求ID，实际为 %q", notice.BizId)
	}
	var payload cspb.KickedOfflinePush
	if err := proto.Unmarshal(notice.Data, &payload); err != nil {
		t.Fatalf("解析顶号通知负载失败: %v", err)
	}
	if payload.Platform != "ios" || payload.LoginAt != 1730000000000 {
		t.Fatalf("顶号通知负载不符，实际为 %+v", &payload)
	}
	if payload.Ip != "203.0.113.7" || payload.DeviceId != "device-b" {
		t.Fatalf("顶号通知应携带新登录端 IP 与设备ID，实际为 %+v", &payload)
	}

	// 随后连接应以 4002 关闭，客户端据此不再重连
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var err error
	for err == nil {
		_, _, err = conn.ReadMessage()
	}
	if !websocket.IsCloseError(err, CloseCodeConnReplaced) {
		t.Fatalf("应收到关闭码 %d，实际错误为 %v", CloseCodeConnReplaced, err)
	}
}

// TestKickPlayerWithNoticeSkipsReplacedToken 校验跨实例顶号广播的过滤能力：
// 只踢旧 Token 的连接，保留已用新 Token 建立的新连接。
// 若不过滤，延迟到达的广播会把刚建好的新连接一并踢掉（客户端看到「账号已在其它设备登录」）。
func TestKickPlayerWithNoticeSkipsReplacedToken(t *testing.T) {
	env := startTestServer(t, newTestConfig(), "93001")
	oldConn := dialWithToken(t, env, "old-token")
	defer func() { _ = oldConn.Close() }()
	newConn := dialWithToken(t, env, "new-token")
	defer func() { _ = newConn.Close() }()
	waitStats(t, env.hub, 2)

	// 丢弃两条连接各自的握手帧
	if frame := readFrame(t, oldConn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("旧连接首帧应为握手帧，实际为 %s", frame.Type.String())
	}
	if frame := readFrame(t, newConn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("新连接首帧应为握手帧，实际为 %s", frame.Type.String())
	}

	closed := env.hub.KickPlayerWithNoticeExcept(93001, &PushPayload{
		BizID: "req-kick-2",
		Data: &cspb.KickedOfflinePush{
			LoginAt:    1730000000001,
			Platform:   "android",
			DeviceName: "Pixel 9",
			Ip:         "198.51.100.9",
			DeviceId:   "device-new",
		},
	}, TokenDigest("new-token"))
	if closed != 1 {
		t.Fatalf("应只关闭旧 Token 的 1 条连接，实际为 %d", closed)
	}

	// 旧连接：先收到通知帧，再以 4002 关闭
	notice := readFrame(t, oldConn)
	if notice.MsgId != cspb.WsMsgId_WsMsgIdKickedOfflinePush {
		t.Fatalf("通知帧 msgId 应为 KickedOfflinePush，实际为 %s", notice.MsgId.String())
	}
	_ = oldConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var err error
	for err == nil {
		_, _, err = oldConn.ReadMessage()
	}
	if !websocket.IsCloseError(err, CloseCodeConnReplaced) {
		t.Fatalf("旧连接应收到关闭码 %d，实际错误为 %v", CloseCodeConnReplaced, err)
	}

	// 新连接必须仍然在线且可正常收到推送
	waitStats(t, env.hub, 1)
	result := env.hub.PushToPlayer(93001, &PushPayload{BizID: "probe", Data: &cspb.KickedOfflinePush{}})
	if result.Online != 1 || result.Delivered != 1 {
		t.Fatalf("新连接应仍然在线，实际 online=%d delivered=%d", result.Online, result.Delivered)
	}
	if probe := readFrame(t, newConn); probe.BizId != "probe" {
		t.Fatalf("新连接应收到探测推送，实际 bizId=%q", probe.BizId)
	}
}

// TestPushToPlayersSharesEncodedFrame 校验批量推送复用同一份编码字节后，各玩家收到的负载依然正确
func TestPushToPlayersSharesEncodedFrame(t *testing.T) {
	cfg := newTestConfig()
	env1 := startTestServer(t, cfg, "70001")

	conn := dial(t, env1)
	defer func() { _ = conn.Close() }()
	if frame := readFrame(t, conn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为欢迎帧，实际为 %s", frame.Type)
	}
	waitStats(t, env1.hub, 1)

	result := env1.hub.PushToPlayers([]int64{70001, 70002}, &PushPayload{
		Data:  &cspb.WsHandshakeInfo{PlayerId: 70001, ServerTime: 1},
		BizID: "biz-1",
	})
	if result.Total != 2 || result.Delivered != 1 || result.Offline != 1 {
		t.Fatalf("批量推送结果异常: %+v", result)
	}

	frame := readFrame(t, conn)
	if frame.Type != cspb.WsMsgType_WsMsgTypePush {
		t.Fatalf("应收到推送帧，实际为 %s", frame.Type)
	}
	if frame.BizId != "biz-1" {
		t.Fatalf("推送帧 bizId 应为 biz-1，实际为 %q", frame.BizId)
	}
	if frame.MsgId != cspb.WsMsgId_WsMsgIdNone {
		t.Fatalf("未注册负载类型 msgId 应为 WsMsgIdNone，实际为 %s", frame.MsgId)
	}
	payload := &cspb.WsHandshakeInfo{}
	if err := proto.Unmarshal(frame.Data, payload); err != nil {
		t.Fatalf("解析推送负载失败: %v", err)
	}
	if payload.PlayerId != 70001 {
		t.Fatalf("推送负载内容错误: %+v", payload)
	}
}

// TestPushPaySuccessCarriesMsgId 校验支付成功推送帧携带 WsMsgIdPaySuccessPush，
// 客户端可据此识别 data 为 PaySuccessPush 并正确解析
func TestPushPaySuccessCarriesMsgId(t *testing.T) {
	cfg := newTestConfig()
	env := startTestServer(t, cfg, "71001")

	conn := dial(t, env)
	defer func() { _ = conn.Close() }()
	if frame := readFrame(t, conn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为欢迎帧，实际为 %s", frame.Type)
	}
	waitStats(t, env.hub, 1)

	pushData := &cspb.PaySuccessPush{
		OrderId:  "order-1",
		PayId:    "com.xx.diamond",
		Channel:  "appstore",
		AwardTag: "iap_001",
		PaidAt:   1700000000000,
	}
	if result := env.hub.PushToPlayer(71001, &PushPayload{Data: pushData, BizID: "order-1"}); result.Delivered != 1 {
		t.Fatalf("推送结果异常: %+v", result)
	}

	frame := readFrame(t, conn)
	if frame.Type != cspb.WsMsgType_WsMsgTypePush {
		t.Fatalf("应收到推送帧，实际为 %s", frame.Type)
	}
	if frame.BizId != "order-1" {
		t.Fatalf("推送帧 bizId 应为 order-1，实际为 %q", frame.BizId)
	}
	if frame.MsgId != cspb.WsMsgId_WsMsgIdPaySuccessPush {
		t.Fatalf("推送帧 msgId 应为 WsMsgIdPaySuccessPush，实际为 %s", frame.MsgId)
	}
	got := &cspb.PaySuccessPush{}
	if err := proto.Unmarshal(frame.Data, got); err != nil {
		t.Fatalf("解析支付成功推送负载失败: %v", err)
	}
	if got.OrderId != pushData.OrderId || got.PayId != pushData.PayId ||
		got.Channel != pushData.Channel || got.AwardTag != pushData.AwardTag ||
		got.PaidAt != pushData.PaidAt {
		t.Fatalf("推送负载内容错误: %+v", got)
	}
}

// TestUpgradeRequiredForPlainRequest 校验非升级请求返回统一错误信封而非进入协议解析
func TestUpgradeRequiredForPlainRequest(t *testing.T) {
	env := startTestServer(t, newTestConfig(), "80001")
	// 直接以普通 TCP 发起 HTTP GET，不携带升级头
	addr := env.url[len("ws://") : len(env.url)-len("/ws?token=t&platform=test")]
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("建立 TCP 连接失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("GET /ws HTTP/1.1\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("发送普通 HTTP 请求失败: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("读取响应失败: %v", err)
	}
	// 业务错误统一以 HTTP 200 + 响应体 code 返回
	if got := string(buf[:n]); !contains(got, "200") {
		t.Fatalf("非升级请求应返回 200 错误信封，实际响应: %q", got)
	}
}

// contains 判断字符串包含关系（避免为单处断言引入额外依赖）
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestParsePlayerID 校验玩家ID解析：空串与非法值返回 0，交由调用方拒绝建连
func TestParsePlayerID(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"", 0},
		{"abc", 0},
		{"-1", -1},
		{"123", 123},
		{strconv.FormatInt(1<<62, 10), 1 << 62},
	}
	for _, c := range cases {
		if got := parsePlayerID(c.input); got != c.want {
			t.Fatalf("parsePlayerID(%q) = %d，期望 %d", c.input, got, c.want)
		}
	}
}

// TestNilFrameGuard 校验 nil 消息/负载的防御行为：
// 推送与投递路径遇到 nil 帧时静默拒绝（返回零送达），不会 panic，也不向客户端写入空消息。
func TestNilFrameGuard(t *testing.T) {
	// 拉长心跳间隔，避免服务端心跳探测帧干扰「不应收到任何下行数据」的断言
	cfg := newTestConfig()
	cfg.HeartbeatInterval = time.Second
	env := startTestServer(t, cfg, "77001")
	conn := dial(t, env)
	defer func() { _ = conn.Close() }()
	if frame := readFrame(t, conn); frame.Type != cspb.WsMsgType_WsMsgTypeHandshake {
		t.Fatalf("首帧应为欢迎帧，实际为 %s", frame.Type)
	}
	waitStats(t, env.hub, 1)

	// SendToPlayer 收到 nil 帧：返回零送达而非写入空消息
	if result := env.hub.SendToPlayer(77001, nil); result.Delivered != 0 {
		t.Fatalf("nil 帧不应送达任何连接，实际送达 %d", result.Delivered)
	}
	// PushToPlayer / PushToPlayers 收到 nil 负载：不 panic，返回零送达
	if result := env.hub.PushToPlayer(77001, nil); result.Delivered != 0 {
		t.Fatalf("nil 负载不应送达任何连接，实际送达 %d", result.Delivered)
	}
	batch := env.hub.PushToPlayers([]int64{77001}, nil)
	if batch.Delivered != 0 || batch.Offline != 0 {
		t.Fatalf("nil 负载批量推送不应计送达或离线，实际 %+v", batch)
	}

	// 以上调用期间服务端不得向客户端写入任何帧（含空帧）
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("nil 帧不应向客户端写入任何下行数据")
	}
}

// TestSendFrameNilGuard 校验 sendFrame 对 nil 帧与空帧的防御：
// 均返回 false 且不向发送缓冲写入任何字节（不产生对客户端的空消息）
func TestSendFrameNilGuard(t *testing.T) {
	s := &Session{
		cfg:  newTestConfig(),
		send: make(chan outboundMessage, 1),
		done: make(chan struct{}),
	}
	if s.SendMessage(nil) {
		t.Fatal("sendFrame 对 nil 帧应返回 false")
	}
	// 全零字段的可变消息序列化后为空字节，同样不应进入发送缓冲
	if s.SendMessage(&cspb.WsMessage{}) {
		t.Fatal("sendFrame 对空帧应返回 false")
	}
	select {
	case msg := <-s.send:
		t.Fatalf("nil/空帧不应进入发送缓冲，实际收到 %d 字节", len(msg.data))
	default:
	}
}
