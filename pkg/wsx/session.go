package wsx

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
)

// 服务端主动断开连接时下发的 WebSocket Close 状态码（RFC 6455 的 4000~4999 私有区间）。
// 若不告知原因，客户端无法区分「被封禁」与「网络抖动」，只会无脑自动重连：
// 对被封禁玩家会形成重连风暴，对限流场景则会立刻再次触发同样的问题。
// 这些状态码属于客户端协议契约的一部分，新增时须同步通知客户端。
//
// 唯一不告知原因的服务端断开是「下行缓冲积压」：缓冲被写满说明客户端没在消费下行数据，
// Close 帧本就送不到，详见 enqueue 的说明。
const (
	// CloseCodeKicked 被服务端踢下线（封禁 / GM 踢人）：客户端应停止重连并回到登录流程
	CloseCodeKicked = 4001
	// CloseCodeConnReplaced 被新设备顶号踢线（同一账号在其它设备登录）：
	// 已建连时先收到 KickedOfflinePush 通知帧再收到本码；客户端离线错过通知时，
	// 重连握手会补发本码。客户端应停止重连与自动重登，提示「账号已在其它设备登录」
	CloseCodeConnReplaced = 4002
	// CloseCodeRateLimited 上行消息频率超限：客户端应降低发送频率后退避重连
	CloseCodeRateLimited = 4003
	// CloseCodeHeartbeatTimeout 心跳超时判定失联：客户端可立即重连
	CloseCodeHeartbeatTimeout = 4005
	// CloseCodeInvalidIdentity 握手身份非法（含握手令牌已撤销/过期）：客户端应回到登录流程重新获取令牌
	CloseCodeInvalidIdentity = 4006
	// CloseCodeServerShutdown 服务端正在停机：客户端应退避后重连（通常会被调度到新实例）
	CloseCodeServerShutdown = 4007
	// CloseCodeConnLimit 单玩家并发连接数超限，最早建立的连接被新连接踢除：
	// 与 4002（被新设备顶号）区分——此处账号仍在本端，只是异常客户端反复建连；
	// 客户端不应重连，也不应提示「账号已在其它设备登录」
	CloseCodeConnLimit = 4008
)

// closeDrainWait 主动断开前排空对端在途数据的最长等待时间。
// 必须排空：若接收缓冲仍有未读数据就关闭 socket，内核会发送 RST 而非 FIN，
// 对端接收缓冲随之被丢弃——刚下发的 Close 帧也一起丢掉，客户端只会看到「连接被重置」。
// 上限保证个别持续刷帧、不理会 Close 帧的异常客户端无法把收尾流程拖长。
const closeDrainWait = 200 * time.Millisecond

// closeFrameWriteWait 下发 Close 控制帧的最长等待时间。
// 取值远小于 Config.WriteWait：Close 帧需要与写协程争抢连接的写锁，
// 而 close 可能被 Hub 的批量踢人/停机流程调用，必须避免个别拥塞连接拖慢整体收尾。
// 等不到写锁即放弃告知原因，退化为直接断开（与加入本机制之前的行为一致）。
const closeFrameWriteWait = 100 * time.Millisecond

// noticeFlushWait 「先通知后断开」场景下等待通知帧真正写入底层连接的最长时间。
// 必须等写入完成而不能只看入队：closeWith 会立刻关闭 done 让写协程退出，
// 缓冲中尚未写出的通知帧会被丢弃，客户端就只会看到 Close 帧而拿不到业务说明。
// 等待有上限：个别拥塞连接不得把登录等主流程拖慢，超时后仍会下发 Close 码兜底。
const noticeFlushWait = 100 * time.Millisecond

// outboundMessage 下行发送缓冲中的一项。
// data 非空表示一条待写入的已编码帧；ack 非 nil 表示「屏障」：
// 写协程处理到屏障时，说明排在它前面的帧都已 WriteMessage 返回，
// 随后关闭 ack 通知等待方。帧与屏障共用同一 FIFO 通道，保证屏障不会乱序抢先被处理。
type outboundMessage struct {
	// data 已编码的下行帧字节
	data []byte
	// ack 屏障完成信号（仅屏障项非 nil）
	ack chan struct{}
}

// closeReason 服务端主动断开连接的原因，用于填充 WebSocket Close 控制帧。
// code 为 0 表示无需告知客户端（如读协程因客户端主动断开而自然结束）。
type closeReason struct {
	// code WebSocket 关闭状态码（见 CloseCodeXxx 常量）
	code int
	// text 英文原因短语（面向客户端；控制帧负载上限 125 字节，须保持简短）
	text string
}

// Session 一条 WebSocket 长连接会话。
// 每个会话持有独立的下行发送缓冲通道，并由唯一的写协程串行写入底层连接，
// 保证同一时刻仅一个协程执行 WriteMessage，规避并发写导致的协议解析错乱。
// 上下行消息统一以 cspb.WsMessage 消息帧编码（protobuf 二进制帧）。
type Session struct {
	conn     *websocket.Conn
	hub      *Hub
	cfg      Config
	playerID int64
	platform string
	ip       string
	// tokenDigest 握手令牌的 SHA-256 摘要（不保存明文）。
	// 用于顶号踢线时区分「旧端连接」与「新登录建立的连接」：两者 Token 不同、摘要不同，
	// 跨实例广播据此跳过新连接，避免延迟到达的广播把刚建好的连接踢掉。
	// 用摘要而非 connectedAt 比较是因为 connectedAt 取各实例本地时钟，跨实例存在时钟偏差。
	tokenDigest string
	// connID 握手请求的 requestId，用于把长连接期间的日志与建连请求串联起来
	connID string
	// send 下行发送缓冲，帧与 flush 屏障共用同一 FIFO 通道（见 outboundMessage）
	send chan outboundMessage
	// done 会话关闭信号；关闭会话时只关闭该通道，绝不关闭 send
	done chan struct{}
	// writeDone 写协程退出信号，由写协程在返回前关闭
	writeDone chan struct{}
	isClosed  atomic.Bool
	// signalOnce 保证「发出关闭信号」只做一次（注销会话 + 关闭 done）
	signalOnce sync.Once
	// connOnce 保证底层连接只关闭一次。
	// 与 signalOnce 拆开是必需的：读协程主动断开时要先让写协程停下，
	// 再独占连接完成「下发 Close 帧 → 排空在途数据 → 关闭连接」；
	// 若「发信号」与「关连接」绑在同一个 Once 内就无法拆出这段独占窗口。
	connOnce   sync.Once
	lastActive atomic.Int64
	// connectedAt 建连时间（Unix 毫秒），仅用于日志与排查
	connectedAt int64
	// seq 建连顺序号，由 Hub 单调递增分配。
	// 踢除「最早连接」必须依据它而非 connectedAt：毫秒精度下同一刻建立的多条连接无法比较，
	// 会让「踢最早」退化成「随机踢一条」。
	seq uint64
	// inboundTokens/inboundLastRefill 上行限流令牌桶状态（仅读协程访问，无需同步）
	inboundTokens     float64
	inboundLastRefill time.Time
}

// newSession 创建会话实例（不自动注册，由升级处理器显式注册以控制时序）
func newSession(conn *websocket.Conn, hub *Hub, cfg Config, playerID int64, platform, ip, connID, tokenDigest string) *Session {
	now := time.Now()
	s := &Session{
		conn:        conn,
		hub:         hub,
		cfg:         cfg,
		playerID:    playerID,
		platform:    platform,
		ip:          ip,
		connID:      connID,
		tokenDigest: tokenDigest,
		send:        make(chan outboundMessage, cfg.SendBufferSize),
		done:        make(chan struct{}),
		writeDone:   make(chan struct{}),
		connectedAt: now.UnixMilli(),
		seq:         hub.nextSeq(),
		// 令牌桶初始为满桶，允许客户端建连后立即发送一轮请求
		inboundTokens:     float64(inboundBurst(cfg)),
		inboundLastRefill: now,
	}
	s.setLastActive()
	return s
}

// PlayerID 返回会话所属玩家ID
func (s *Session) PlayerID() int64 {
	return s.playerID
}

// Platform 返回客户端平台标识（升级时由 query 参数 platform 传入，可为空）
func (s *Session) Platform() string {
	return s.platform
}

// IP 返回客户端 IP 地址
func (s *Session) IP() string {
	return s.ip
}

// ConnID 返回连接追踪ID（取自握手请求的 requestId，可为空）
func (s *Session) ConnID() string {
	return s.connID
}

// Run 启动会话读写泵，onReady 在写协程就绪后、进入读循环前回调（可为 nil）。
// 通过 onReady 下发欢迎帧，确保写协程已启动，避免依赖发送缓冲的隐式容量前提。
// readPump 阻塞直至连接断开或读超时判定失联。
//
// 收尾时序至关重要，必须满足两点：
//  1. 用 defer 关闭会话：业务回调 panic 时读协程会向上 unwind，若按顺序执行则
//     会话将永久残留在 Hub 中（幽灵会话，持续被推送且不可回收）。
//  2. 返回前等待写协程彻底退出：本函数返回后，Fiber 的 websocket 中间件会把
//     *websocket.Conn 置空并归还对象池（releaseConn），此时若仍有写入在途，
//     将命中 nil 底层连接导致进程崩溃，或写入被复用后属于其他玩家的连接造成数据串号。
func (s *Session) Run(onReady func()) {
	defer func() {
		// 先关闭会话（关 done 并断开底层连接，令在途写入立即失败返回），
		// 再等待写协程退出，确保此后不会有任何协程再触碰底层连接。
		s.close()
		<-s.writeDone
	}()

	go s.writePump()
	if onReady != nil {
		onReady()
	}
	s.readPump()
}

// SendMessage 异步向该会话投递一条消息帧（非阻塞）。
// 返回 true 表示消息已进入发送缓冲；缓冲满或会话已关闭时返回 false。
func (s *Session) SendMessage(frame *cspb.WsMessage) bool {
	return s.sendFrame(frame)
}

// sendFrame 将消息帧编码为 protobuf 字节并投递到下行缓冲。
// 与 sendEncoded 语义保持一致：帧为 nil 或其编码结果为空字节时返回 false，
// 不向客户端写入空消息（空帧会被对端解析为不可辨识的非法帧）。
func (s *Session) sendFrame(frame *cspb.WsMessage) bool {
	if frame == nil || s.isClosed.Load() {
		return false
	}
	data, err := proto.Marshal(frame)
	if err != nil {
		logx.Warn("WebSocket 下行消息帧序列化失败，丢弃该消息",
			zap.String("requestId", s.connID),
			zap.Int64("userId", s.playerID),
			zap.String("messageType", frame.Type.String()),
			zap.Error(err),
		)
		return false
	}
	if len(data) == 0 {
		return false
	}
	return s.enqueue(data)
}

// sendEncoded 投递已编码完成的消息帧字节。
// 供 Hub 批量推送复用同一份编码结果，避免同一帧对每个会话重复序列化。
// 参数 data 在多个会话间共享，下行路径只读，调用方不得在投递后修改其内容。
func (s *Session) sendEncoded(data []byte) bool {
	if s.isClosed.Load() || len(data) == 0 {
		return false
	}
	return s.enqueue(data)
}

// sendError 下发错误帧（负载为 WsErrorInfo）
func (s *Session) sendError(code int32, message string) {
	data := buildErrorFrameBytes(code, message)
	if data == nil {
		return
	}
	_ = s.enqueue(data)
}

// sendHeartbeatAck 应答客户端心跳探测帧（负载为 WsHeartbeatResp）
func (s *Session) sendHeartbeatAck() {
	now := time.Now().UnixMilli()
	data, err := proto.Marshal(&cspb.WsHeartbeatResp{ServerTime: now})
	if err != nil {
		return
	}
	_ = s.sendFrame(&cspb.WsMessage{
		Type:      cspb.WsMsgType_WsMsgTypeHeartbeatAck,
		Data:      data,
		Timestamp: now,
	})
}

// isDone 判断会话是否已进入关闭流程（非阻塞）
func (s *Session) isDone() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// enqueue 将字节写入下行缓冲；缓冲满时判定客户端消费过慢产生背压，关闭连接以防内存膨胀。
// 通过监听 done 通道感知会话关闭，避免向已关闭会话的发送通道写入而 panic：
// 会话关闭时只关闭 done（永不关闭 send），故 select 命中 done 分支后直接返回 false，
// 保证发送端永远不会向已关闭的 channel 发送数据。
func (s *Session) enqueue(data []byte) bool {
	if s.isDone() {
		// 会话已关闭，丢弃该消息
		return false
	}
	select {
	case s.send <- outboundMessage{data: data}:
		return true
	case <-s.done:
		// 入队期间会话被关闭，丢弃该消息
		return false
	default:
		logx.Warn("WebSocket 下行缓冲已满，关闭连接以释放背压",
			zap.String("requestId", s.connID),
			zap.Int64("userId", s.playerID),
			zap.Int("bufferSize", cap(s.send)),
		)
		// 背压场景刻意不下发 Close 帧，直接断开：
		// 一是缓冲被写满恰恰说明客户端没在消费下行数据，Close 帧本就无法送达；
		// 二是此时写协程正卡在写入上、握着连接的写锁，下发 Close 帧必然等满
		// closeFrameWriteWait 才失败——而 enqueue 会被 Hub 的广播循环调用，
		// 每条积压连接都白等一次会把整轮广播拖慢。
		s.signalClose()
		s.closeConn()
		return false
	}
}

// writePump 下行写协程：串行消费待发送消息；当服务端主动心跳开启（HeartbeatInterval>0）时，
// 额外按心跳间隔探测客户端活性。退出前关闭 writeDone，通知 Run 可以安全返回（底层连接此后不再被写入）。
func (s *Session) writePump() {
	defer close(s.writeDone)

	// 服务端主动心跳默认不开启（HeartbeatInterval 为 0，见 DefaultConfig）：
	// 此时不创建 ticker，静默连接由读协程的读超时 ReadWait 兜底断开；
	// 心跳开启时才按配置间隔下发 Heartbeat 探测帧并周期性检查失联。
	var heartbeatC <-chan time.Time
	if s.cfg.HeartbeatInterval > 0 {
		ticker := time.NewTicker(s.cfg.HeartbeatInterval)
		defer ticker.Stop()
		heartbeatC = ticker.C
	}

	for {
		select {
		case <-s.done:
			// 会话已关闭，退出写协程
			return
		case msg := <-s.send:
			// done 与 send 同时就绪时 select 随机命中，故写入前再判一次关闭状态，
			// 尽量避免会话关闭后仍向底层连接发起写入
			if s.isDone() {
				return
			}
			if msg.ack != nil {
				// 屏障：此前的帧已全部写入底层连接，通知等待方（如「先通知后断开」）
				close(msg.ack)
				continue
			}
			if err := s.write(msg.data); err != nil {
				logx.Warn("WebSocket 下行写入失败，断开连接",
					zap.String("requestId", s.connID),
					zap.Int64("userId", s.playerID),
					zap.Error(err),
				)
				s.close()
				return
			}
		case <-heartbeatC:
			if s.isDone() {
				return
			}
			// 距最近一次收到客户端消息超过读超时阈值，判定失联
			if time.Since(time.UnixMilli(s.lastActive.Load())) > s.cfg.ReadWait {
				logx.Warn("WebSocket 心跳超时，断开连接",
					zap.String("requestId", s.connID),
					zap.Int64("userId", s.playerID),
					zap.Duration("readWait", s.cfg.ReadWait),
				)
				s.closeWith(closeReason{CloseCodeHeartbeatTimeout, "heartbeat timeout"})
				return
			}
			// 服务端主动心跳探测：构造 Heartbeat 帧（负载为 WsHeartbeatReq）
			frameBytes := s.buildHeartbeatFrame()
			if frameBytes == nil {
				// 心跳帧构造失败意味着无法再探测活性，关闭会话而非留下无写协程的连接
				s.closeWith(closeReason{websocket.CloseInternalServerErr, "heartbeat encode failed"})
				return
			}
			if err := s.write(frameBytes); err != nil {
				s.close()
				return
			}
		}
	}
}

// buildHeartbeatFrame 构造服务端心跳探测帧的编码字节
func (s *Session) buildHeartbeatFrame() []byte {
	now := time.Now().UnixMilli()
	reqData, err := proto.Marshal(&cspb.WsHeartbeatReq{SenderTime: now})
	if err != nil {
		return nil
	}
	frame, err := proto.Marshal(&cspb.WsMessage{
		Type:      cspb.WsMsgType_WsMsgTypeHeartbeat,
		Data:      reqData,
		Timestamp: now,
	})
	if err != nil {
		return nil
	}
	return frame
}

// isReadTimeout 判断读取错误是否源于读超时（底层连接在 ReadWait 内无任何数据到达）。
// 本包仅在读协程中通过 SetReadDeadline 设置读超时，故命中读超时即等价于
// 「客户端静默超过 ReadWait」，是判定失联的直接依据。
func isReadTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// readPump 上行读协程：持续读取客户端消息并处理心跳与业务消息。
// 会话读取到任何消息（含心跳应答）都会重置读超时并刷新活跃时间；
// 读超时（客户端静默超过 ReadWait）判定失联并以 4005 关闭码断开。
func (s *Session) readPump() {
	s.conn.SetReadLimit(s.cfg.MaxMessageSize)
	_ = s.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadWait))

	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			// 读超时判定失联：超过 ReadWait 未收到客户端任何消息（含心跳应答）。
			// 服务端主动心跳默认关闭，写协程的失联检查不运行，此处是失联断开的唯一出口——
			// 必须携带 4005 关闭码告知客户端并记录日志，否则静默断开会让客户端把它当成
			// 网络抖动无脑重连，运维也无从排查「连接为何整点消失」。读取超时后连接状态已
			// 不可用（库文档明确），直接进入关闭流程。
			if isReadTimeout(err) {
				logx.Warn("WebSocket 读超时判定失联，断开连接",
					zap.String("requestId", s.connID),
					zap.Int64("userId", s.playerID),
					zap.Duration("readWait", s.cfg.ReadWait),
				)
				s.closeWith(closeReason{CloseCodeHeartbeatTimeout, "heartbeat timeout"})
				return
			}
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				logx.Warn("WebSocket 读取出错，断开连接",
					zap.String("requestId", s.connID),
					zap.Int64("userId", s.playerID),
					zap.Error(err),
				)
			}
			return
		}
		s.refresh()

		// 上行限流：异常客户端可能高频刷帧放大服务端解析与日志开销，超限直接断开。
		// 必须告知原因，否则客户端会把断开当成网络抖动而立即重连、再次触发限流。
		if !s.allowInbound() {
			logx.Warn("WebSocket 上行消息频率超限，断开连接",
				zap.String("requestId", s.connID),
				zap.Int64("userId", s.playerID),
				zap.Int("maxMessagesPerSecond", s.cfg.MaxMessagesPerSecond),
				zap.Int("inboundBurst", inboundBurst(s.cfg)),
			)
			// 走排空路径：限流意味着客户端正在高频发送，直接关闭会因 RST 让 Close 帧丢失
			s.closeAfterDrain(closeReason{CloseCodeRateLimited, "inbound rate limit exceeded"})
			return
		}

		var frame cspb.WsMessage
		if err := proto.Unmarshal(data, &frame); err != nil || frame.Type == cspb.WsMsgType_WsMsgTypeNone {
			// 非法帧：下发错误通知后继续，不中断连接
			s.sendError(httpx.ErrCodeBadRequest, "invalid message frame")
			continue
		}

		switch frame.Type {
		case cspb.WsMsgType_WsMsgTypeHeartbeat:
			// 客户端心跳探测：应答心跳应答帧并携带服务端时间
			s.sendHeartbeatAck()
		case cspb.WsMsgType_WsMsgTypeHeartbeatAck:
			// 客户端对服务端探测的应答：活跃时间已在 refresh 中刷新，负载无需解析
		case cspb.WsMsgType_WsMsgTypeBusiness:
			// 业务帧：交由业务回调处理。
			// bizId 必须一并传入——分发已收窄到只有 business 一种帧类型，
			// msgType 恒为 business，bizId 是业务侧唯一可用的路由/幂等依据。
			s.hub.dispatchMessage(s.playerID, frame.Type, frame.BizId, frame.Data)
		default:
			// 其余帧类型（Welcome/Push/Error 等均为服务端下行专用），客户端不应上行；
			// 收敛攻击面：下发错误通知后继续，不中断连接
			s.sendError(httpx.ErrCodeBadRequest, "unsupported message type")
		}
	}
}

// inboundBurst 返回上行限流的令牌桶容量：未显式配置时退化为每秒上限。
// 参数：cfg 连接配置
// 返回：桶容量（条），至少为 1
func inboundBurst(cfg Config) int {
	if cfg.InboundBurst > 0 {
		return cfg.InboundBurst
	}
	if cfg.MaxMessagesPerSecond > 0 {
		return cfg.MaxMessagesPerSecond
	}
	return 1
}

// allowInbound 判定当前上行消息是否放行（令牌桶限流）。
// 相比固定窗口，令牌桶不会在窗口边界放行两倍突发：令牌按 MaxMessagesPerSecond 匀速补充，
// 桶容量 InboundBurst 决定允许的瞬时突发量，长期平均速率严格受限。
// 仅由读协程串行调用，故桶状态无需同步原语。
// 配置项 MaxMessagesPerSecond <= 0 时表示不限流。
//
// 返回：true 表示放行本条上行消息
func (s *Session) allowInbound() bool {
	rate := float64(s.cfg.MaxMessagesPerSecond)
	if rate <= 0 {
		return true
	}
	burst := float64(inboundBurst(s.cfg))

	now := time.Now()
	if elapsed := now.Sub(s.inboundLastRefill).Seconds(); elapsed > 0 {
		s.inboundTokens = min(burst, s.inboundTokens+elapsed*rate)
	}
	s.inboundLastRefill = now

	if s.inboundTokens < 1 {
		return false
	}
	s.inboundTokens--
	return true
}

// write 底层连接写入（携带写超时；消息帧为 protobuf 二进制，使用二进制消息类型）
func (s *Session) write(data []byte) error {
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteWait))
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

// refresh 刷新活跃时间与读超时
func (s *Session) refresh() {
	s.setLastActive()
	_ = s.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadWait))
}

// setLastActive 记录最近一次活跃时间（Unix 毫秒）
func (s *Session) setLastActive() {
	s.lastActive.Store(time.Now().UnixMilli())
}

// signalClose 发出会话关闭信号：注销会话并关闭 done，不触碰底层连接。
// 幂等（signalOnce 保证）。
// 注意：仅关闭 done、绝不关闭 send，从而杜绝发送端向已关闭 channel 写入导致的 panic。
func (s *Session) signalClose() {
	s.signalOnce.Do(func() {
		s.isClosed.Store(true)
		s.hub.unregister(s)
		close(s.done)
	})
}

// closeConn 关闭底层连接（幂等）。
// 关闭后在途的读写立即返回错误，从而快速释放读写协程。
func (s *Session) closeConn() {
	s.connOnce.Do(func() {
		_ = s.conn.Close()
	})
}

// sendCloseFrame 下发 WebSocket Close 控制帧告知断开原因；code 为 0 时不发送。
// WriteControl 内部会争抢连接写锁，因此允许跨协程并发调用（库文档明确保证），
// 无需绕经写协程转发；等锁时间受 closeFrameWriteWait 限制，
// 拥塞时放弃告知而非拖慢批量踢人/停机流程。
//
// 参数：reason 断开原因
func (s *Session) sendCloseFrame(reason closeReason) {
	if reason.code == 0 {
		return
	}
	deadline := time.Now().Add(min(closeFrameWriteWait, s.cfg.WriteWait))
	_ = s.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(reason.code, reason.text),
		deadline,
	)
}

// flush 向下行缓冲插入一个屏障并等待写协程处理完毕，返回是否在时限内完成。
// 屏障与帧共用同一 FIFO 通道，故 flush 返回 true 时，此前入队的帧都已写入底层连接。
// 参数：wait 最长等待时间
// 返回：true 表示屏障已被写协程处理（此前的帧均已写出）；false 表示会话已关闭或等待超时
func (s *Session) flush(wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	ack := make(chan struct{})
	enqueueTimer := time.NewTimer(wait)
	defer enqueueTimer.Stop()
	select {
	case s.send <- outboundMessage{ack: ack}:
	case <-s.done:
		return false
	case <-enqueueTimer.C:
		return false
	}
	flushTimer := time.NewTimer(time.Until(deadline))
	defer flushTimer.Stop()
	select {
	case <-ack:
		return true
	case <-s.done:
		return false
	case <-flushTimer.C:
		return false
	}
}

// notifyAndClose 先向客户端投递一条通知帧并等待其写出，再以 reason 断开连接。
// 用于「先告知客户端被顶号，再断开」：直接 closeWith 会先关闭 done 让写协程退出，
// 缓冲中尚未写出的通知帧会被丢弃，客户端只能拿到 Close 码而看不到业务说明。
// 通知帧入队失败或等待超时不影响断开，客户端仍可凭 Close 码兜底处理。
//
// 参数：
//   - data:   已编码的通知帧字节
//   - reason: 断开原因（code 为 0 时不下发 Close 帧）
func (s *Session) notifyAndClose(data []byte, reason closeReason) {
	if len(data) > 0 && s.enqueue(data) {
		_ = s.flush(noticeFlushWait)
	}
	s.closeWith(reason)
}

// close 关闭会话，不向客户端告知原因。
// 仅用于「客户端已主动断开」等无需通知的场景；服务端主动断开一律使用 closeWith。
func (s *Session) close() {
	s.closeWith(closeReason{})
}

// closeWith 关闭会话并告知客户端断开原因（幂等）。
// 依次发出关闭信号、下发 Close 控制帧、关闭底层连接；
// 写协程因 done 关闭而退出，读协程因连接关闭而返回。
//
// 可由任意协程调用（Hub 的踢人/停机流程、写协程、Run 的收尾 defer）。
//
// 参数：reason 断开原因；code 为 0 时不下发 Close 帧
func (s *Session) closeWith(reason closeReason) {
	s.signalClose()
	s.sendCloseFrame(reason)
	s.closeConn()
}

// closeAfterDrain 由读协程发起的主动断开，保证客户端能可靠收到断开原因。
// 与 closeWith 的差别是多了两步不可省略的处理：
//
//  1. 先让写协程停下并等它退出。写协程与本函数操作同一条连接，而下发 Close 帧后
//     底层库会把连接标记为「已发送关闭」，写协程随后的任何写入都会失败，
//     进而走「写失败即断开」分支直接关掉连接——排空尚未开始就被打断。
//  2. 再排空对端在途数据。读协程主动断开的典型场景是上行限流，此时客户端正在高频发送，
//     接收缓冲必然有未读数据；此时关闭 socket 内核会发出 RST 而非 FIN，
//     对端接收缓冲连同刚收到的 Close 帧一起被丢弃，客户端只会看到「连接被重置」。
//
// 仅允许在读协程内调用（内部使用 SetReadDeadline / ReadMessage，这两者要求单读者）。
//
// 参数：reason 断开原因（code 为 0 时不下发 Close 帧，仅排空后关闭）
func (s *Session) closeAfterDrain(reason closeReason) {
	// 1. 停写协程并等待其退出，此后本协程独占连接。
	//    等待有上限：写协程可能正卡在被 TCP 背压阻塞的写入上（最长 WriteWait），
	//    超时即放弃优雅收尾直接断开，避免为一条异常连接长时间占用协程与连接对象。
	s.signalClose()
	select {
	case <-s.writeDone:
	case <-time.After(min(closeFrameWriteWait, s.cfg.WriteWait)):
		s.closeConn()
		return
	}

	// 2. 下发 Close 控制帧
	s.sendCloseFrame(reason)

	// 3. 排空在途数据：读到错误（含对端 Close 回应）或超过 closeDrainWait 即停止
	_ = s.conn.SetReadDeadline(time.Now().Add(closeDrainWait))
	for {
		if _, _, err := s.conn.ReadMessage(); err != nil {
			break
		}
	}

	// 4. 关闭底层连接（此时接收缓冲已排空，正常 FIN 而非 RST）
	s.closeConn()
}
