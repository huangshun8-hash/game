package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"kemiluo/pkg/wsx"
)

// writeConfigFile 在临时目录写入一份 gamelogic 配置文件，并把 CONFIG_DIR 指向它
func writeConfigFile(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gamelogic_local.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试配置文件失败: %v", err)
	}
	t.Setenv("CONFIG_DIR", dir)
}

// setRequiredEnv 设置 Load() 强制要求的环境变量，避免测试因缺失密钥而中断
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "local")
	t.Setenv("JWT_SIGNING_KEY", "test-jwt-signing-key-at-least-32-chars")
	t.Setenv("INTERNAL_SECRET", "test-internal-secret-at-least-32-chars")
}

// TestLoadAppliesWebSocketSectionFromFile 校验配置文件 websocket 段能真正落到 wsx.Config。
// 这是「不改代码即可调整长连接参数与 Origin 白名单」的前提：若 yaml 段没接通，
// Origin 白名单永远是默认通配，CSWSH 防御等于永久关闭。
func TestLoadAppliesWebSocketSectionFromFile(t *testing.T) {
	setRequiredEnv(t)
	writeConfigFile(t, `
websocket:
  handshake_timeout: "7s"
  heartbeat_interval: "15s"
  read_wait: "45s"
  write_wait: "3s"
  send_buffer_size: 128
  max_connections_per_player: 2
  max_message_size: 32768
  max_messages_per_second: 20
  inbound_burst: 40
  enable_compression: false
  origins:
    - "https://game.example.com"
    - "https://cdn.example.com"
  allow_empty_origin: true
  read_buffer_size: 2048
  write_buffer_size: 4096
`)

	cfg := Load()
	ws := cfg.WS

	if ws.HandshakeTimeout != 7*time.Second {
		t.Fatalf("handshakeTimeout 应为 7s，实际为 %s", ws.HandshakeTimeout)
	}
	if ws.HeartbeatInterval != 15*time.Second {
		t.Fatalf("heartbeatInterval 应为 15s，实际为 %s", ws.HeartbeatInterval)
	}
	if ws.ReadWait != 45*time.Second {
		t.Fatalf("readWait 应为 45s，实际为 %s", ws.ReadWait)
	}
	if ws.WriteWait != 3*time.Second {
		t.Fatalf("writeWait 应为 3s，实际为 %s", ws.WriteWait)
	}
	if ws.SendBufferSize != 128 {
		t.Fatalf("sendBufferSize 应为 128，实际为 %d", ws.SendBufferSize)
	}
	if ws.MaxConnectionsPerPlayer != 2 {
		t.Fatalf("maxConnectionsPerPlayer 应为 2，实际为 %d", ws.MaxConnectionsPerPlayer)
	}
	if ws.MaxMessageSize != 32768 {
		t.Fatalf("maxMessageSize 应为 32768，实际为 %d", ws.MaxMessageSize)
	}
	if ws.MaxMessagesPerSecond != 20 {
		t.Fatalf("maxMessagesPerSecond 应为 20，实际为 %d", ws.MaxMessagesPerSecond)
	}
	if ws.InboundBurst != 40 {
		t.Fatalf("inboundBurst 应为 40，实际为 %d", ws.InboundBurst)
	}
	if ws.EnableCompression {
		t.Fatal("enableCompression 应为 false")
	}
	if len(ws.Origins) != 2 || ws.Origins[0] != "https://game.example.com" || ws.Origins[1] != "https://cdn.example.com" {
		t.Fatalf("origins 应读出两个具体域名，实际为 %v", ws.Origins)
	}
	if ws.HasOriginWildcard() {
		t.Fatal("收紧后的 origins 不应再被判定为通配")
	}
	if !ws.AllowEmptyOrigin {
		t.Fatal("allowEmptyOrigin 应为 true")
	}
	if ws.ReadBufferSize != 2048 || ws.WriteBufferSize != 4096 {
		t.Fatalf("读/写缓冲应为 2048/4096，实际为 %d/%d", ws.ReadBufferSize, ws.WriteBufferSize)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("该配置组合应通过校验，实际报错: %v", err)
	}
}

// TestLoadFallsBackToWebSocketDefaults 校验未配置 websocket 段时沿用 wsx.DefaultConfig()，
// 保证「漏配某项」不会把参数清零成 0 值（0 值会让连接立刻失联或缓冲为 0）
func TestLoadFallsBackToWebSocketDefaults(t *testing.T) {
	setRequiredEnv(t)
	writeConfigFile(t, "log:\n  level: \"debug\"\n")

	got := Load().WS
	want := wsx.DefaultConfig()
	if got.HeartbeatInterval != want.HeartbeatInterval || got.ReadWait != want.ReadWait ||
		got.SendBufferSize != want.SendBufferSize || got.MaxMessageSize != want.MaxMessageSize ||
		got.MaxMessagesPerSecond != want.MaxMessagesPerSecond || got.InboundBurst != want.InboundBurst ||
		got.MaxConnectionsPerPlayer != want.MaxConnectionsPerPlayer {
		t.Fatalf("未配置 websocket 段时应沿用默认配置\n实际: %+v\n期望: %+v", got, want)
	}
	if !got.HasOriginWildcard() {
		t.Fatal("未配置 origins 时应为默认通配")
	}
}

// TestLoadWebSocketEnvOverridesFile 校验环境变量优先级高于配置文件，
// 且 origins 支持环境变量的逗号分隔写法（环境变量无法表达 YAML 列表结构）
func TestLoadWebSocketEnvOverridesFile(t *testing.T) {
	setRequiredEnv(t)
	writeConfigFile(t, `
websocket:
  max_connections_per_player: 2
  origins:
    - "*"
`)
	t.Setenv("WEBSOCKET_MAX_CONNECTIONS_PER_PLAYER", "9")
	t.Setenv("WEBSOCKET_ORIGINS", "https://a.example.com, https://b.example.com")

	ws := Load().WS
	if ws.MaxConnectionsPerPlayer != 9 {
		t.Fatalf("环境变量应覆盖配置文件，maxConnectionsPerPlayer 应为 9，实际为 %d", ws.MaxConnectionsPerPlayer)
	}
	if len(ws.Origins) != 2 || ws.Origins[0] != "https://a.example.com" || ws.Origins[1] != "https://b.example.com" {
		t.Fatalf("origins 应按逗号分隔解析并去除空格，实际为 %v", ws.Origins)
	}
}

// TestValidateRejectsBrokenWebSocketConfig 校验非法长连接配置能阻止服务启动，
// 而不是等到运行期表现为「所有连接建立后立刻断开」
func TestValidateRejectsBrokenWebSocketConfig(t *testing.T) {
	setRequiredEnv(t)
	// 读超时小于心跳间隔：连接会在首次心跳探测之前就被判定失联
	writeConfigFile(t, `
websocket:
  heartbeat_interval: "60s"
  read_wait: "10s"
`)
	if err := Load().Validate(); err == nil {
		t.Fatal("读超时小于心跳间隔的配置应被启动校验拦截")
	}
}

// TestLoadWSPath 校验 WebSocket 升级路径的三级取值为：默认值 → 配置文件 → 环境变量。
// ws_path 决定客户端建连 URL 的路径段，且必须与 JWT 跳过白名单保持一致。
func TestLoadWSPath(t *testing.T) {
	setRequiredEnv(t)

	t.Run("default", func(t *testing.T) {
		writeConfigFile(t, "log:\n  level: \"debug\"\n")
		if got := Load().WSPath; got != defaultWSPath {
			t.Fatalf("未配置 ws_path 时应回退默认路径 %q，实际为 %q", defaultWSPath, got)
		}
	})

	t.Run("fromFile", func(t *testing.T) {
		writeConfigFile(t, `
websocket:
  ws_path: "/ws/game"
`)
		if got := Load().WSPath; got != "/ws/game" {
			t.Fatalf("配置文件 ws_path 应生效为 /ws/game，实际为 %q", got)
		}
	})

	t.Run("envOverridesFile", func(t *testing.T) {
		writeConfigFile(t, `
websocket:
  ws_path: "/ws/game"
`)
		t.Setenv("WEBSOCKET_WS_PATH", "/ws/live")
		if got := Load().WSPath; got != "/ws/live" {
			t.Fatalf("环境变量应覆盖配置文件 ws_path，应为 /ws/live，实际为 %q", got)
		}
	})
}

// TestValidateRejectsBrokenWSPath 校验非法升级路径（不以 "/" 开头）能阻止服务启动，
// 避免生成无法命中路由的裸路径导致所有 WebSocket 握手失败
func TestValidateRejectsBrokenWSPath(t *testing.T) {
	setRequiredEnv(t)
	writeConfigFile(t, `
websocket:
  ws_path: "ws"
`)
	if err := Load().Validate(); err == nil {
		t.Fatal("不以 / 开头的 ws_path 应被启动校验拦截")
	}
}
