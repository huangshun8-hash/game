package wsx

import (
	"context"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/logx"
)

// MessageHandler 业务消息回调，在会话读取到业务帧（WsMsgTypeBusiness）时触发。
// 参数：
//   - playerID: 会话所属玩家ID
//   - msgType:  帧类型（当前恒为 cspb.WsMsgType_WsMsgTypeBusiness，保留参数以便后续扩展可分发的帧类型）
//   - bizID:    业务消息ID（取自帧的 bizId 字段），业务侧据此做路由与幂等去重
//   - data:     帧负载（原始字节，按 bizID 对应的协议解析）
type MessageHandler func(playerID int64, msgType cspb.WsMsgType, bizID string, data []byte)

// closeWorkers 批量关闭连接时的最大并发度。
// 下发 Close 控制帧需要争抢连接写锁（受 closeFrameWriteWait 约束），
// 串行关闭会让个别拥塞连接把停机/批量踢人流程拖成 N×closeFrameWriteWait；
// 并发上限同时避免万人级停机时瞬间创建同等数量的协程。
const closeWorkers = 64

// Hub 长连接会话管理器（进程内网关核心）。
// 以 playerID 为维度维护玩家会话集合，提供注册/注销、定向与批量推送、在线统计能力，
// 并实现 fiber.Service 接口以随应用生命周期优雅启停、关闭所有连接。
type Hub struct {
	mu       sync.RWMutex
	sessions map[int64]map[*Session]struct{}
	// messageHandler 业务消息回调。以 atomic.Pointer 存储而非复用 mu 保护，
	// 使上行消息分发无需与连接注册/注销争抢同一把锁（上行是最热路径）。
	messageHandler atomic.Pointer[MessageHandler]
	// connSeq 建连顺序号发号器，为每条连接分配严格单调递增的序号。
	// 连接数超限时据此选出「最早建立」的连接：毫秒级时间戳无法区分同一刻建立的多条连接。
	connSeq atomic.Uint64
}

// NewHub 创建会话管理器
func NewHub() *Hub {
	return &Hub{sessions: make(map[int64]map[*Session]struct{})}
}

// nextSeq 分配下一个建连顺序号（严格单调递增，并发安全）
func (h *Hub) nextSeq() uint64 {
	return h.connSeq.Add(1)
}

// String 返回服务的可读名称（fiber.Service 接口）
func (h *Hub) String() string {
	return "wsx-hub"
}

// State 返回服务的当前状态（fiber.Service 接口）
func (h *Hub) State(ctx context.Context) (string, error) {
	return "ready", nil
}

// Start 实现 fiber.Service 接口，无需额外启动动作
func (h *Hub) Start(ctx context.Context) error {
	return nil
}

// Terminate 实现 fiber.Service 接口，关闭所有活跃 WebSocket 连接
func (h *Hub) Terminate(ctx context.Context) error {
	h.CloseAll()
	return nil
}

// SetMessageHandler 设置业务消息回调。
// 并发安全；作为进程级唯一处理器，由 handler 层在服务启动阶段注册一次。
// handler 为 nil 时表示清空回调。
func (h *Hub) SetMessageHandler(handler MessageHandler) {
	if handler == nil {
		h.messageHandler.Store(nil)
		return
	}
	h.messageHandler.Store(&handler)
}

// dispatchMessage 触发业务消息回调（若已注册）。
// 回调在会话读协程内同步执行，故必须拦截 panic：
// 一是 panic 会击穿到 goroutine 顶层直接终止进程，
// 二是读协程异常 unwind 会绕过正常的会话清理路径。
func (h *Hub) dispatchMessage(playerID int64, msgType cspb.WsMsgType, bizID string, data []byte) {
	ptr := h.messageHandler.Load()
	if ptr == nil || *ptr == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logx.Error("WebSocket 业务消息回调 panic，已拦截并保持连接",
				zap.Int64("userId", playerID),
				zap.String("msgType", msgType.String()),
				zap.String("bizId", bizID),
				zap.Any("panic", r),
				zap.String("stack", string(debug.Stack())),
			)
		}
	}()
	(*ptr)(playerID, msgType, bizID, data)
}

// register 注册新会话（连接上线）。
// 当单玩家在线连接数超过配置上限时，踢除其最早建立的连接以释放资源。
func (h *Hub) register(s *Session) {
	var evict *Session

	h.mu.Lock()
	set := h.sessions[s.playerID]
	if set == nil {
		set = make(map[*Session]struct{})
		h.sessions[s.playerID] = set
	}
	set[s] = struct{}{}
	// 在锁内选取踢除对象、锁外关闭，避免与 unregister 的锁重入
	evict = pickEvictTarget(set, s, s.cfg.MaxConnectionsPerPlayer)
	h.mu.Unlock()

	if evict != nil {
		logx.Warn("WebSocket 单玩家连接数超限，踢除最早连接",
			zap.Int64("userId", s.playerID),
			zap.Int("limit", s.cfg.MaxConnectionsPerPlayer),
			zap.String("evictedRequestId", evict.connID),
		)
		evict.closeWith(closeReason{CloseCodeConnLimit, "too many connections"})
	}
}

// pickEvictTarget 在玩家的会话集合中选出应被踢除的「最早建立」连接。
// 排序依据是 Hub 分配的单调递增 seq，而非毫秒精度的 connectedAt：
// 同一毫秒内建立的多条连接用时间戳无法比较，会让「踢最早」退化成「随机踢一条」。
// 调用方须持有 Hub 的写锁。
//
// 参数：
//   - set:   该玩家当前的全部会话（已包含 self）
//   - self:  刚注册的新会话，不参与踢除
//   - limit: 单玩家最大并发连接数，<=0 表示不限制
//
// 返回：需踢除的会话；未超限或无可踢对象时返回 nil
func pickEvictTarget(set map[*Session]struct{}, self *Session, limit int) *Session {
	if limit <= 0 || len(set) <= limit {
		return nil
	}
	var evict *Session
	for candidate := range set {
		if candidate == self {
			continue
		}
		if evict == nil || candidate.seq < evict.seq {
			evict = candidate
		}
	}
	return evict
}

// unregister 注销会话（连接下线）
func (h *Hub) unregister(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.sessions[s.playerID]
	if set == nil {
		return
	}
	delete(set, s)
	if len(set) == 0 {
		delete(h.sessions, s.playerID)
	}
}

// SendToPlayer 向目标玩家的所有在线会话投递自定义消息帧。
// 帧仅序列化一次，多条连接复用同一份只读字节，避免重复编码开销。
// 帧为 nil 时视为无效消息：不向任何连接投递，返回零送达。
// 返回送达连接数与推送前目标玩家在线连接数。
func (h *Hub) SendToPlayer(playerID int64, frame *cspb.WsMessage) *PushResult {
	targets := h.snapshot(playerID)
	result := &PushResult{Online: len(targets)}
	if len(targets) == 0 {
		return result
	}
	if frame == nil {
		logx.Warn("WebSocket 下行消息帧为空，丢弃该消息", zap.Int64("userId", playerID))
		return result
	}
	data, err := proto.Marshal(frame)
	if err != nil {
		logx.Warn("WebSocket 下行消息帧序列化失败，丢弃该消息",
			zap.Int64("userId", playerID),
			zap.String("messageType", frame.Type.String()),
			zap.Error(err),
		)
		return result
	}
	for _, s := range targets {
		if s.sendEncoded(data) {
			result.Delivered++
		}
	}
	return result
}

// PushToPlayer 向目标玩家定向推送业务消息（构建 push 类型的消息帧）。
// 负载为空时视为无效调用：记录告警并返回零送达。
func (h *Hub) PushToPlayer(playerID int64, payload *PushPayload) *PushResult {
	frame := buildPushFrame(payload)
	if frame == nil {
		logx.Warn("WebSocket 定向推送负载为空，跳过本次推送", zap.Int64("userId", playerID))
		return &PushResult{}
	}
	return h.SendToPlayer(playerID, frame)
}

// PushToPlayers 向多个玩家批量推送业务消息。
// 负载编码与帧序列化在全流程中只做一次，跨玩家、跨连接复用同一份字节，
// 避免万人级广播时产生 N×M 次重复序列化；
// 会话快照也只加一次读锁批量取出，避免 N 个目标玩家产生 N 次加锁与 N 次切片分配。
// 返回批量推送结果：Total 目标数、Delivered 送达连接数、Offline 未送达玩家数
func (h *Hub) PushToPlayers(playerIDs []int64, payload *PushPayload) *BatchPushResult {
	result := &BatchPushResult{Total: len(playerIDs)}
	if len(playerIDs) == 0 {
		return result
	}
	frame := buildPushFrame(payload)
	if frame == nil {
		logx.Warn("WebSocket 批量推送负载为空，跳过本次推送",
			zap.Int("targetCount", len(playerIDs)),
		)
		return result
	}
	data, err := proto.Marshal(frame)
	if err != nil {
		logx.Warn("WebSocket 批量推送消息帧序列化失败，丢弃本次推送",
			zap.String("messageType", frame.Type.String()),
			zap.Int("targetCount", len(playerIDs)),
			zap.Error(err),
		)
		result.Offline = len(playerIDs)
		return result
	}
	targets, counts := h.snapshotMany(playerIDs)
	// 锁外执行投递：targets 按 playerIDs 顺序平铺，counts[i] 为第 i 个玩家的连接数
	offset := 0
	for _, count := range counts {
		delivered := 0
		for _, s := range targets[offset : offset+count] {
			if s.sendEncoded(data) {
				delivered++
			}
		}
		if delivered == 0 {
			result.Offline++
		}
		result.Delivered += delivered
		offset += count
	}
	return result
}

// KickPlayer 关闭指定玩家的全部在线连接，返回被关闭的连接数。
// 用于封禁/踢人后立即断开长连接：握手鉴权只校验 JWT 签名、不查 Token 存储，
// 因此仅撤销 Redis Token 无法影响已建立的连接，必须由此显式断开。
//
// 参数：playerID 目标玩家ID
// 返回：本次实际关闭的连接数（玩家不在线时为 0）
func (h *Hub) KickPlayer(playerID int64) int {
	targets := h.snapshot(playerID)
	// 携带 Close 状态码断开：客户端据此区分「被踢下线需重新登录」与网络抖动，
	// 否则自动重连逻辑会立刻重连（握手校验器接入 TokenStore 后会被拒，但仍是无谓的重连风暴）
	closeSessions(targets, closeReason{CloseCodeKicked, "kicked by server"})
	if len(targets) > 0 {
		logx.Info("WebSocket 连接已按业务要求踢除",
			zap.Int64("userId", playerID),
			zap.Int("count", len(targets)),
		)
	}
	return len(targets)
}

// KickPlayerWithNotice 先向指定玩家的全部在线连接推送一条通知，再以 4002 关闭。
// 用于同一账号在其它设备登录（顶号）时告知旧端并下线：
// 4002 的客户端契约是「不应重连」，配合业务通知帧可让客户端明确展示「账号已在其它设备登录」，
// 而不是把断开当成网络抖动或令牌过期。
// 通知帧序列化失败时仍会断开（顶号状态已成立，不能让旧连接继续在线）。
//
// 参数：
//   - playerID: 目标玩家ID
//   - payload:  通知帧负载（通常为 cspb.KickedOfflinePush）
//
// 返回：本次处理的连接数（玩家不在线时为 0）
func (h *Hub) KickPlayerWithNotice(playerID int64, payload *PushPayload) int {
	return h.KickPlayerWithNoticeExcept(playerID, payload, "")
}

// KickPlayerWithNoticeExcept 与 KickPlayerWithNotice 相同，但跳过令牌摘要为 exceptTokenDigest 的会话。
// 顶号场景必须使用本方法：跨实例广播可能晚于新登录端建连到达，
// 若不加过滤会把新连接一并踢掉（客户端会看到「账号已在其它设备登录」并被断开）。
// 旧端持旧 Token、新端持新 Token，两者摘要不同，据此可精确区分；
// 相比 connectedAt 时间比较，不受跨实例时钟偏差影响。
//
// 参数：
//   - playerID:          目标玩家ID
//   - payload:           通知帧负载
//   - exceptTokenDigest: 需要保留的会话令牌摘要（传空串表示不跳过任何会话）
//
// 返回：本次实际关闭的连接数
func (h *Hub) KickPlayerWithNoticeExcept(playerID int64, payload *PushPayload, exceptTokenDigest string) int {
	all := h.snapshot(playerID)
	if len(all) == 0 {
		return 0
	}
	targets := all
	if exceptTokenDigest != "" {
		targets = make([]*Session, 0, len(all))
		for _, s := range all {
			// 摘要一致的会话属于本次新登录，必须保留
			if s.tokenDigest == exceptTokenDigest {
				continue
			}
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return 0
	}

	reason := closeReason{CloseCodeConnReplaced, "kicked off by another login"}
	frame := buildPushFrame(payload)
	if frame == nil {
		closeSessions(targets, reason)
		return len(targets)
	}
	data, err := proto.Marshal(frame)
	if err != nil {
		logx.Warn("WebSocket 顶号通知帧序列化失败，仅断开连接",
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		closeSessions(targets, reason)
		return len(targets)
	}
	closeSessionsWithNotice(targets, data, reason)
	logx.Info("WebSocket 已向被顶号连接下发通知并断开",
		zap.Int64("userId", playerID),
		zap.Int("count", len(targets)),
		zap.Int("online", len(all)),
	)
	return len(targets)
}

// Stats 返回当前在线统计
func (h *Hub) Stats() *Stats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := &Stats{}
	for _, set := range h.sessions {
		result.TotalConnections += len(set)
	}
	result.OnlinePlayers = len(h.sessions)
	return result
}

// CloseAll 关闭所有活跃连接（服务退出或定向关闭时调用）
func (h *Hub) CloseAll() {
	h.mu.RLock()
	total := 0
	for _, set := range h.sessions {
		total += len(set)
	}
	all := make([]*Session, 0, total)
	for _, set := range h.sessions {
		for s := range set {
			all = append(all, s)
		}
	}
	h.mu.RUnlock()
	closeSessions(all, closeReason{CloseCodeServerShutdown, "server shutting down"})
	if len(all) > 0 {
		logx.Info("WebSocket 连接全部关闭", zap.Int("count", len(all)))
	}
}

// closeSessions 批量关闭会话并告知断开原因，并发度受 closeWorkers 限制。
// 单条连接的关闭最多等待 closeFrameWriteWait 才能拿到写锁下发 Close 帧，
// 串行关闭会把停机流程拖成 N×closeFrameWriteWait，故按固定并发度分摊。
//
// 参数：targets 待关闭的会话（允许为空）；reason 断开原因
func closeSessions(targets []*Session, reason closeReason) {
	if len(targets) == 0 {
		return
	}
	// 数量少时直接串行，避免为个别连接付出协程调度成本
	if len(targets) == 1 {
		targets[0].closeWith(reason)
		return
	}
	workers := min(closeWorkers, len(targets))
	queue := make(chan *Session)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for s := range queue {
				s.closeWith(reason)
			}
		}()
	}
	for _, s := range targets {
		queue <- s
	}
	close(queue)
	wg.Wait()
}

// closeSessionsWithNotice 在关闭前先向每条会话投递同一条已编码通知帧，
// 并发度与 closeSessions 一致：单条连接等待通知写出有上限（noticeFlushWait），
// 串行处理会把批量顶号拖成 N×noticeFlushWait。
//
// 参数：
//   - targets: 待关闭的会话
//   - data:    已编码的通知帧字节（多条连接共享只读字节）
//   - reason:  断开原因
func closeSessionsWithNotice(targets []*Session, data []byte, reason closeReason) {
	if len(targets) == 0 {
		return
	}
	if len(targets) == 1 {
		targets[0].notifyAndClose(data, reason)
		return
	}
	workers := min(closeWorkers, len(targets))
	queue := make(chan *Session)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for s := range queue {
				s.notifyAndClose(data, reason)
			}
		}()
	}
	for _, s := range targets {
		queue <- s
	}
	close(queue)
	wg.Wait()
}

// snapshot 返回某玩家的在线会话快照。
// 在锁外执行推送，避免持锁期间进行网络 IO 导致阻塞其他会话的注册/注销。
func (h *Hub) snapshot(playerID int64) []*Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	set := h.sessions[playerID]
	if len(set) == 0 {
		return nil
	}
	targets := make([]*Session, 0, len(set))
	for s := range set {
		targets = append(targets, s)
	}
	return targets
}

// snapshotMany 一次加锁批量取出多个玩家的在线会话快照，供批量推送使用。
// 相比逐个玩家调用 snapshot，把 N 次读锁与 N 次切片分配压缩为 1 次，
// 显著降低万人级广播对注册/注销路径的锁争抢。
//
// 参数：playerIDs 目标玩家ID列表（允许含重复项，重复项按各自出现次数分别统计）
// 返回：
//   - targets: 按 playerIDs 顺序平铺的会话列表
//   - counts:  与 playerIDs 一一对应的连接数，用于在 targets 上切分每个玩家的区间
func (h *Hub) snapshotMany(playerIDs []int64) (targets []*Session, counts []int) {
	counts = make([]int, len(playerIDs))
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := 0
	for i, pid := range playerIDs {
		counts[i] = len(h.sessions[pid])
		total += counts[i]
	}
	if total == 0 {
		return nil, counts
	}
	targets = make([]*Session, 0, total)
	for _, pid := range playerIDs {
		for s := range h.sessions[pid] {
			targets = append(targets, s)
		}
	}
	return targets, counts
}
