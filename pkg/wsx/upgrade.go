package wsx

import (
	"context"
	"errors"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"

	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// UpgradeHandler 返回 WebSocket 升级处理器（fiber.Handler）。
// 使用前应已通过 AuthMiddleware 完成升级前的签名鉴权：
// 处理器先执行升级后的会话校验（auth.CheckSession，可为 nil），通过后读取上下文中的 userId，
// 解析为玩家ID并创建会话、注册到 Hub、下发 handshake 握手消息，随后进入阻塞式读写泵直至连接断开。
// 非升级请求（如普通 GET/curl）返回统一 JSON 错误信封，避免进入协议解析。
func UpgradeHandler(hub *Hub, cfg Config, auth *HandshakeAuth) fiber.Handler {
	upgrader := websocket.New(func(conn *websocket.Conn) {
		handleUpgradedConnection(hub, cfg, auth, conn)
	}, websocket.Config{
		// 握手与 IO 超时/缓冲参数与 wsx.Config 对齐
		HandshakeTimeout:  cfg.HandshakeTimeout,
		ReadBufferSize:    cfg.ReadBufferSize,
		WriteBufferSize:   cfg.WriteBufferSize,
		EnableCompression: cfg.EnableCompression,
		// Origin 校验：防御跨站 WebSocket 劫持；非浏览器客户端不带 Origin 头，
		// 故由 AllowEmptyOrigin 单独控制是否放行
		Origins:          cfg.Origins,
		AllowEmptyOrigin: cfg.AllowEmptyOrigin,
	})

	return func(c fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return httpx.Fail(c, httpx.ErrCodeBadRequest, "websocket upgrade required")
		}
		return upgrader(c)
	}
}

// Register 在 fiber 路由上注册 WebSocket 升级端点（一站式便捷方法），
// 等价于依次注册 AuthMiddleware（升级前签名校验）与 UpgradeHandler（升级后会话校验）。
// 参数：
//   - route: 目标路由（如 server.App() 或 httpx.RouterGroup.Raw()）
//   - path:  升级路径（如 /ws）
//   - hub:   会话管理器（进程内网关核心）
//   - cfg:   连接配置
//   - auth:  两段式握手鉴权配置；为 nil 时跳过全鉴权（如已由其他鉴权机制保证）
//
// 注意：服务若挂载了全局 JWT 中间件（如 httpx.JWTMiddlewareWithTokenStore），
// 需先将升级路径加入其 skipPaths，以免 Header 鉴权拦截无 Authorization 头的握手请求。
func Register(route fiber.Router, path string, hub *Hub, cfg Config, auth *HandshakeAuth) {
	if auth == nil {
		route.Get(path, UpgradeHandler(hub, cfg, nil))
		return
	}
	if auth.Validate != nil {
		route.Get(path, AuthMiddleware(auth.Validate), UpgradeHandler(hub, cfg, auth))
		return
	}
	route.Get(path, UpgradeHandler(hub, cfg, auth))
}

// handleUpgradedConnection 在升级成功后创建会话并启动其读写泵。
// 由 websocket.New 的升级回调调用，运行在独占 goroutine 中直至连接关闭。
func handleUpgradedConnection(hub *Hub, cfg Config, auth *HandshakeAuth, conn *websocket.Conn) {
	// Locals/Query 均在升级时由中间件从请求上下文拷贝进 conn，连接存活期间读取安全
	userId, _ := conn.Locals("userId").(string)
	requestID, _ := conn.Locals("requestId").(string)
	platform := conn.Query("platform")
	ip := conn.IP()
	token := conn.Query("token")

	// 升级后的会话校验：必须在创建会话、注册 Hub 之前完成。
	// 若放到 Hub.register 之后，被顶号的旧连接会占用该玩家的连接配额，
	// 并把合法的第 6 条连接当作「最早连接」踢掉（见 Hub.register/pickEvictTarget）。
	// 此处不创建 Session、不起协程，拒绝成本仅限一次升级 + 一个 Close 帧。
	//
	// 为什么只下发 Close 码、不再下发 error 帧：顶号踢线与本处的握手拒绝是**同一个事件**——
	// 客户端当时离线，收不到本该收到的 4002，重连时补发即可；关闭码 4002 的语义单一，
	// 客户端不需要再解析业务码就能提示「账号已在其它设备登录」。
	if auth != nil && auth.CheckSession != nil && userId != "" {
		if err := auth.CheckSession(context.Background(), requestID, userId, token); err != nil {
			closeCode := CloseCodeInvalidIdentity
			message := "invalid or expired token"
			if errors.Is(err, ErrSessionReplaced) {
				closeCode = CloseCodeConnReplaced
				message = "session replaced by another login"
			} else if errors.Is(err, ErrTokenRevoked) {
				message = "token has been revoked"
			}
			logx.Warn("WebSocket 握手会话校验失败，拒绝建连",
				zap.String("requestId", requestID),
				zap.String("userId", userId),
				zap.String("ip", ip),
				zap.Int("closeCode", closeCode),
				zap.Error(err),
			)
			rejectConnection(conn, cfg, closeCode, message)
			return
		}
	}

	playerID := parsePlayerID(userId)

	// 玩家ID 必须为正整数：若放行非法值，所有异常连接都会落进 playerID=0 的同一会话桶，
	// 彼此触发连接数超限踢除，并被 PushToPlayer(0) 误当作同一玩家广播，故直接拒绝建连
	if playerID <= 0 {
		logx.Warn("WebSocket 握手玩家ID非法，拒绝建连",
			zap.String("requestId", requestID),
			zap.String("rawUserId", userId),
			zap.String("ip", ip),
		)
		rejectConnection(conn, cfg, CloseCodeInvalidIdentity, "invalid player identity")
		return
	}

	session := newSession(conn, hub, cfg, playerID, platform, ip, requestID, TokenDigest(token))
	hub.register(session)

	logx.Info("WebSocket 连接建立",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.String("platform", platform),
		zap.String("ip", ip),
	)

	// 写协程就绪后再下发欢迎帧：告知客户端连接已建立
	// （负载为 WsHandshakeInfo，携带玩家ID与服务端时间）
	session.Run(func() {
		now := time.Now().UnixMilli()
		handshakeData, err := proto.Marshal(&cspb.WsHandshakeInfo{PlayerId: playerID, ServerTime: now})
		if err == nil {
			_ = session.SendMessage(&cspb.WsMessage{
				Type:      cspb.WsMsgType_WsMsgTypeHandshake,
				Data:      handshakeData,
				Timestamp: now,
			})
		}
	})
}

// rejectConnection 在会话尚未建立的阶段拒绝一条已升级成功的连接。
// 只下发 WebSocket Close 控制帧，再关闭底层连接：
//   - Close 控制帧承载关闭码与英文原因，客户端据此决定是否重连、如何提示用户；
//   - 这是协议层的正常关闭流程，比直接断开 TCP 更可靠（避免客户端把它当成网络异常重连）。
//
// 不再额外下发业务错误帧：握手阶段的失败结论已由关闭码完整表达（4002 顶号 / 4006 身份非法），
// 多写一帧只会增加拒绝成本，且客户端必须维护两套映射。
//
// 参数：
//   - conn:      已升级的连接
//   - cfg:       连接配置（提供写超时）
//   - closeCode: WebSocket 关闭状态码（见 CloseCodeXxx）
//   - message:   英文关闭原因（面向客户端，长度须小于 123 字节）
func rejectConnection(conn *websocket.Conn, cfg Config, closeCode int, message string) {
	deadline := time.Now().Add(cfg.WriteWait)
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, message), deadline)
	// 关闭前排空对端在途数据：接收缓冲残留未读数据时关闭 socket 会触发 RST，
	// 对端接收缓冲连同刚下发的 Close 帧一起被丢弃
	_ = conn.SetReadDeadline(time.Now().Add(closeDrainWait))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	_ = conn.Close()
}

// parsePlayerID 将玩家ID字符串解析为 int64；空串或非法值返回 0。
// 与 httpx 的玩家ID语义保持一致（userId 为数字字符串）。
func parsePlayerID(userId string) int64 {
	if userId == "" {
		return 0
	}
	pid, err := strconv.ParseInt(userId, 10, 64)
	if err != nil {
		return 0
	}
	return pid
}
