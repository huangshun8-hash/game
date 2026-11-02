// Package authdto 定义认证相关的跨实例广播消息与频道常量。
package authdto

// SessionKickedChannel 顶号跨服广播的 Redis Pub/Sub 频道名。
// 每个 GameLogic 实例的 wsx.Hub 只管理本进程内的连接，登录请求与玩家 WS 连接可能落在不同实例，
// 故登录实例把「顶号」事件发布到此频道，各实例订阅后仅对本实例上的旧连接下发通知并断开。
const SessionKickedChannel = "auth:sessionKicked"

// SessionKickedMessage 顶号跨服广播消息（频道：auth:sessionKicked）。
// 订阅方按 PlayerID 判断目标玩家是否连接在本实例上，在线则下发 cspb.KickedOfflinePush 并断开。
type SessionKickedMessage struct {
	// PlayerID 被顶号的玩家ID
	PlayerID int64 `json:"playerId"`
	// LoginAt 新登录发生时间（Unix 毫秒）
	LoginAt int64 `json:"loginAt"`
	// Platform 新登录端平台标识（如 ios / android）
	Platform string `json:"platform"`
	// DeviceName 新登录端设备名称（客户端可能未上报，可为空）
	DeviceName string `json:"deviceName"`
	// DeviceID 新登录端设备唯一标识（客户端可能未上报，可为空）
	DeviceID string `json:"deviceId"`
	// IP 新登录端的客户端 IP
	IP string `json:"ip"`
	// TokenDigest 新登录端 Token 的 SHA-256 摘要。
	// 订阅端据此跳过「已用新 Token 建立」的连接，避免延迟到达的广播误踢新登录端；
	// 摘要不是明文令牌，不可用于认证。
	TokenDigest string `json:"tokenDigest"`
	// OriginInstance 发起广播的实例标识，订阅端据此跳过自身消息
	OriginInstance string `json:"originInstance"`
	// RequestID 关联的登录请求追踪ID，便于跨实例串联日志
	RequestID string `json:"requestId"`
}
