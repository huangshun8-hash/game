package wsx

import (
	"fmt"
	"slices"
	"time"
)

// originWildcard Origin 白名单中的通配符，表示放行所有来源
const originWildcard = "*"

// Config WebSocket 长连接网关配置参数
type Config struct {
	// HandshakeTimeout WebSocket 升级握手超时时间
	HandshakeTimeout time.Duration
	// HeartbeatInterval 服务端主动心跳探测间隔；0（默认）表示不开启服务端主动心跳，
	// 连接活性由客户端主动心跳维持（读超时 ReadWait 兜底判定失联）。
	// 设置为正数后，服务端按该间隔主动下发 Heartbeat 帧探测客户端活性。
	HeartbeatInterval time.Duration
	// ReadWait 读超时：超过该时长未收到客户端任何消息（含心跳应答）则判定失联并断开
	ReadWait time.Duration
	// WriteWait 单次写入超时时间
	WriteWait time.Duration
	// SendBufferSize 下行发送缓冲容量（单位：条消息）
	SendBufferSize int
	// MaxConnectionsPerPlayer 单玩家允许的最大并发连接数（<=0 表示不限制）。
	// 超过上限时踢除该玩家最早建立的连接，防止异常客户端反复建连耗尽内存。
	MaxConnectionsPerPlayer int
	// MaxMessageSize 单条上行消息的最大字节数
	MaxMessageSize int64
	// MaxMessagesPerSecond 单连接每秒允许的上行消息数（<=0 表示不限流）。
	// 超限即断开连接，防止异常客户端高频刷帧放大服务端解析与日志开销。
	// 采用令牌桶实现，长期速率严格不超过该值（固定窗口会在窗口边界放行两倍突发）。
	MaxMessagesPerSecond int
	// InboundBurst 上行限流允许的瞬时突发条数（<=0 时取 MaxMessagesPerSecond）。
	// 令牌桶容量，用于容纳客户端正常的批量发送（如场景切换时的连续请求），
	// 同时把长期平均速率约束在 MaxMessagesPerSecond。
	InboundBurst int
	// EnableCompression 是否启用消息压缩（需客户端支持 permessage-deflate）
	EnableCompression bool
	// Origins 允许的 Origin 白名单，用于防御跨站 WebSocket 劫持（CSWSH）。
	// 缺省为 ["*"]，即放行所有来源；有 Web 客户端接入时应按环境收紧为具体域名。
	// 注意：一旦配置为具体域名，不发送 Origin 头的非浏览器客户端（如 Unity 原生客户端）
	// 会被拒绝，此时须同时开启 AllowEmptyOrigin。
	Origins []string
	// AllowEmptyOrigin 是否放行不携带 Origin 头的连接（非浏览器客户端）。
	// 仅在 Origins 收紧为具体域名时才有意义；Origins 为 ["*"] 时所有来源均放行。
	AllowEmptyOrigin bool
	// ReadBufferSize 底层扩读缓冲大小（字节）
	ReadBufferSize int
	// WriteBufferSize 底层写缓冲大小（字节）
	WriteBufferSize int
}

// DefaultConfig 返回 WebSocket 网关默认配置
func DefaultConfig() Config {
	return Config{
		HandshakeTimeout:        10 * time.Second,
		HeartbeatInterval:       0,
		ReadWait:                90 * time.Second,
		WriteWait:               10 * time.Second,
		SendBufferSize:          256,
		MaxConnectionsPerPlayer: 5,
		MaxMessageSize:          64 * 1024,
		MaxMessagesPerSecond:    50,
		InboundBurst:            100,
		EnableCompression:       true,
		Origins:                 []string{originWildcard},
		AllowEmptyOrigin:        true,
		ReadBufferSize:          1024,
		WriteBufferSize:         1024,
	}
}

// HasOriginWildcard 判断 Origin 白名单是否为通配（放行所有来源）。
// 底层升级中间件对 ["*"] 走「完全不校验 Origin 头」的快路径，
// 即此时 CSWSH 防御处于关闭状态，故服务启动时应据此给出提示。
//
// 返回：true 表示白名单含通配符或为空（等价于放行所有来源）
func (c Config) HasOriginWildcard() bool {
	return len(c.Origins) == 0 || slices.Contains(c.Origins, originWildcard)
}

// Validate 校验连接参数的自洽性，拦截会导致连接不可用的错误组合。
// 在服务启动阶段调用，把「所有连接建立后立刻超时断开」这类问题暴露在启动期而非运行期。
//
// 返回：配置非法时返回具体原因，合法时返回 nil
func (c Config) Validate() error {
	if c.HandshakeTimeout <= 0 {
		return fmt.Errorf("websocket.handshakeTimeout must be positive, got %s", c.HandshakeTimeout)
	}
	if c.HeartbeatInterval < 0 {
		return fmt.Errorf("websocket.heartbeatInterval must not be negative, got %s", c.HeartbeatInterval)
	}
	if c.WriteWait <= 0 {
		return fmt.Errorf("websocket.writeWait must be positive, got %s", c.WriteWait)
	}
	// 读超时必须为正，且显著大于心跳间隔（心跳开启时），否则服务端还没来得及探测就先把连接判成失联
	if c.ReadWait <= 0 {
		return fmt.Errorf("websocket.readWait must be positive, got %s", c.ReadWait)
	}
	if c.HeartbeatInterval > 0 && c.ReadWait <= c.HeartbeatInterval {
		return fmt.Errorf("websocket.readWait (%s) must be greater than heartbeatInterval (%s), otherwise every connection times out before the first heartbeat probe",
			c.ReadWait, c.HeartbeatInterval)
	}
	if c.SendBufferSize <= 0 {
		return fmt.Errorf("websocket.sendBufferSize must be positive, got %d", c.SendBufferSize)
	}
	if c.MaxMessageSize <= 0 {
		return fmt.Errorf("websocket.maxMessageSize must be positive, got %d", c.MaxMessageSize)
	}
	if c.ReadBufferSize <= 0 {
		return fmt.Errorf("websocket.readBufferSize must be positive, got %d", c.ReadBufferSize)
	}
	if c.WriteBufferSize <= 0 {
		return fmt.Errorf("websocket.writeBufferSize must be positive, got %d", c.WriteBufferSize)
	}
	// 收紧 Origin 白名单却禁止空 Origin，会把不发送 Origin 头的原生客户端（Unity 等）全部拒之门外
	if !c.HasOriginWildcard() && !c.AllowEmptyOrigin {
		return fmt.Errorf("websocket.allowEmptyOrigin must be true when websocket.origins is restricted to %v, otherwise native clients without an Origin header are rejected", c.Origins)
	}
	return nil
}
