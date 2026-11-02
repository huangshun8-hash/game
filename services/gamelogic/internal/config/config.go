package config

import (
	"fmt"
	"strings"

	"kemiluo/pkg/confx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/wsx"
)

// Config 逻辑服配置，嵌入公共基础配置
type Config struct {
	confx.Base
	// ConfigMongo 游戏配置专用 MongoDB 连接配置（gconfig_db），与 GMT 共享同一实例，仅库名不同
	ConfigMongo *mongox.Config
	// PlatformURL 平台服务地址（用于内部调用账号验证接口）
	PlatformURL string
	// InternalSecret 内部服务间验签密钥（与 platform 共享）
	InternalSecret string
	// TrustedProxies 信任的反代 IP 列表，逗号分隔（如 "127.0.0.1,10.0.0.0/8"）。
	// 配置后服务从 X-Forwarded-For 读取真实客户端 IP，用于限流等场景。
	// 不配置时直接使用 TCP 对端 IP（适合无反代的直连部署）。
	TrustedProxies []string
	// WSPath WebSocket 升级路径（配置文件 websocket 段键 ws_path，默认 "/ws"；
	// 同时支持环境变量 WEBSOCKET_WS_PATH 覆盖）。修改后需同步调整客户端接入 URL。
	WSPath string
	// WS WebSocket 长连接网关配置（配置文件 websocket 段，未配置项沿用 wsx.DefaultConfig()）
	WS wsx.Config
}

// defaultWSPath WebSocket 升级路径默认值
const defaultWSPath = "/ws"

// Load 从环境变量加载逻辑服完整配置
func Load() *Config {
	base, src := confx.LoadBaseWithSource("GameLogic", 8081, "logic_db")

	// ConfigMongo 复用主 Mongo 连接参数，仅将默认库名替换为 gconfig_db
	// 可通过环境变量 CONFIG_MONGO_DATABASE 单独覆盖库名
	configMongoCfg := *base.Mongo
	configMongoCfg.Database = confx.GetEnv("CONFIG_MONGO_DATABASE", "gconfig_db")

	// TRUSTED_PROXIES 逗号分隔的反代 IP/CIDR 列表，如 "127.0.0.1,10.0.0.0/8"
	var trustedProxies []string
	if raw := confx.GetEnv("TRUSTED_PROXIES", ""); raw != "" {
		for p := range strings.SplitSeq(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				trustedProxies = append(trustedProxies, p)
			}
		}
	}

	return &Config{
		Base:           base,
		ConfigMongo:    &configMongoCfg,
		PlatformURL:    confx.GetEnv("PLATFORM_URL", "http://localhost:8080"),
		InternalSecret: confx.MustGetEnvStrict("INTERNAL_SECRET"),
		TrustedProxies: trustedProxies,
		WSPath:         src.String("websocket.ws_path", defaultWSPath),
		WS:             loadWSConfig(src),
	}
}

// loadWSConfig 加载 WebSocket 长连接网关配置。
// 以 wsx.DefaultConfig() 为基线，逐项允许配置文件 websocket 段或同名环境变量覆盖
// （如 websocket.origins ↔ WEBSOCKET_ORIGINS），避免调整连接参数/安全策略必须改代码重编译。
//
// 参数：src 配置源视图
// 返回：合并后的连接配置
func loadWSConfig(src *confx.Source) wsx.Config {
	cfg := wsx.DefaultConfig()
	cfg.HandshakeTimeout = src.Duration("websocket.handshake_timeout", cfg.HandshakeTimeout)
	cfg.HeartbeatInterval = src.Duration("websocket.heartbeat_interval", cfg.HeartbeatInterval)
	cfg.ReadWait = src.Duration("websocket.read_wait", cfg.ReadWait)
	cfg.WriteWait = src.Duration("websocket.write_wait", cfg.WriteWait)
	cfg.SendBufferSize = src.Int("websocket.send_buffer_size", cfg.SendBufferSize)
	cfg.MaxConnectionsPerPlayer = src.Int("websocket.max_connections_per_player", cfg.MaxConnectionsPerPlayer)
	cfg.MaxMessageSize = src.Int64("websocket.max_message_size", cfg.MaxMessageSize)
	cfg.MaxMessagesPerSecond = src.Int("websocket.max_messages_per_second", cfg.MaxMessagesPerSecond)
	cfg.InboundBurst = src.Int("websocket.inbound_burst", cfg.InboundBurst)
	cfg.EnableCompression = src.Bool("websocket.enable_compression", cfg.EnableCompression)
	cfg.Origins = src.StringSlice("websocket.origins", cfg.Origins)
	cfg.AllowEmptyOrigin = src.Bool("websocket.allow_empty_origin", cfg.AllowEmptyOrigin)
	cfg.ReadBufferSize = src.Int("websocket.read_buffer_size", cfg.ReadBufferSize)
	cfg.WriteBufferSize = src.Int("websocket.write_buffer_size", cfg.WriteBufferSize)
	return cfg
}

// Validate 校验逻辑服配置中的敏感项与关键依赖。
func (c *Config) Validate() error {
	if err := confx.ValidateBase(c.Base); err != nil {
		return err
	}
	if c.ConfigMongo == nil || strings.TrimSpace(c.ConfigMongo.Database) == "" {
		return fmt.Errorf("configMongo.database is required")
	}
	if err := confx.ValidateSecret("internalSecret", c.InternalSecret, 32); err != nil {
		return err
	}
	if err := confx.ValidateURL("platformUrl", c.PlatformURL, c.IsLocal()); err != nil {
		return err
	}
	// WebSocket 升级路径必须以 "/" 开头，避免路由拼接错乱（如 "ws" 这类裸路径）
	if !strings.HasPrefix(c.WSPath, "/") {
		return fmt.Errorf("wsPath must start with '/', got %q", c.WSPath)
	}
	// 长连接参数在启动期校验：把「读超时非正」「读超时小于心跳间隔（服务端心跳开启时）」这类会让所有连接建立即断的组合挡在启动前
	if err := c.WS.Validate(); err != nil {
		return err
	}
	return nil
}
