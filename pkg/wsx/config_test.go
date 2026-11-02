package wsx

import (
	"testing"
	"time"
)

// TestDefaultConfigIsValid 校验默认配置本身合法，保证「不写任何配置」即可正常启动
func TestDefaultConfigIsValid(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("默认配置应合法，实际报错: %v", err)
	}
}

// TestConfigValidateRejectsBrokenCombinations 校验启动期能拦住会导致连接不可用的配置组合。
// 这些组合不会在启动时报错、却会让所有连接在运行期异常，必须在启动校验阶段暴露。
func TestConfigValidateRejectsBrokenCombinations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(cfg *Config)
	}{
		{
			name:   "读超时不大于心跳间隔会让所有连接在首次探测前就被判失联",
			mutate: func(cfg *Config) { cfg.HeartbeatInterval = 30 * time.Second; cfg.ReadWait = 30 * time.Second },
		},
		{
			name:   "读超时小于心跳间隔",
			mutate: func(cfg *Config) { cfg.HeartbeatInterval = 30 * time.Second; cfg.ReadWait = 29 * time.Second },
		},
		{
			name:   "读超时非正",
			mutate: func(cfg *Config) { cfg.ReadWait = 0 },
		},
		{
			name:   "握手超时非正",
			mutate: func(cfg *Config) { cfg.HandshakeTimeout = 0 },
		},
		{
			name:   "心跳间隔为负",
			mutate: func(cfg *Config) { cfg.HeartbeatInterval = -time.Second },
		},
		{
			name:   "写超时非正",
			mutate: func(cfg *Config) { cfg.WriteWait = 0 },
		},
		{
			name:   "下行缓冲容量非正",
			mutate: func(cfg *Config) { cfg.SendBufferSize = 0 },
		},
		{
			name:   "上行消息上限非正",
			mutate: func(cfg *Config) { cfg.MaxMessageSize = 0 },
		},
		{
			name:   "读缓冲非正",
			mutate: func(cfg *Config) { cfg.ReadBufferSize = 0 },
		},
		{
			name:   "写缓冲非正",
			mutate: func(cfg *Config) { cfg.WriteBufferSize = 0 },
		},
		{
			name: "收紧 Origin 却禁止空 Origin 会拒绝所有原生客户端",
			mutate: func(cfg *Config) {
				cfg.Origins = []string{"https://game.example.com"}
				cfg.AllowEmptyOrigin = false
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := DefaultConfig()
			c.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("非法配置组合应被启动校验拦截")
			}
		})
	}
}

// TestConfigAcceptsRestrictedOriginsWithEmptyOriginAllowed 校验收紧 Origin 白名单的正确用法可通过校验
func TestConfigAcceptsRestrictedOriginsWithEmptyOriginAllowed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Origins = []string{"https://game.example.com"}
	cfg.AllowEmptyOrigin = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("收紧 Origin 且放行空 Origin 应合法，实际报错: %v", err)
	}
}

// TestHasOriginWildcard 校验通配判定：底层升级中间件对通配白名单完全不校验 Origin 头，
// 即此时 CSWSH 防御处于关闭状态，服务启动需据此告警。
func TestHasOriginWildcard(t *testing.T) {
	cases := []struct {
		name    string
		origins []string
		want    bool
	}{
		{"缺省（空列表）等价于放行所有来源", nil, true},
		{"显式通配", []string{"*"}, true},
		{"通配与具体域名混用仍为放行所有来源", []string{"https://a.com", "*"}, true},
		{"仅具体域名", []string{"https://a.com", "https://b.com"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Origins = c.origins
			if got := cfg.HasOriginWildcard(); got != c.want {
				t.Fatalf("HasOriginWildcard(%v) = %v，期望 %v", c.origins, got, c.want)
			}
		})
	}
}

// TestInboundBurstFallback 校验令牌桶容量的缺省推导：未配置突发时退化为每秒上限
func TestInboundBurstFallback(t *testing.T) {
	cases := []struct {
		name  string
		burst int
		rate  int
		want  int
	}{
		{"显式配置突发", 100, 50, 100},
		{"未配置突发时取每秒上限", 0, 50, 50},
		{"突发为负时取每秒上限", -1, 50, 50},
		{"两者都未配置时至少为 1", 0, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{InboundBurst: c.burst, MaxMessagesPerSecond: c.rate}
			if got := inboundBurst(cfg); got != c.want {
				t.Fatalf("inboundBurst(burst=%d, rate=%d) = %d，期望 %d", c.burst, c.rate, got, c.want)
			}
		})
	}
}
