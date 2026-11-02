package httpcli

import (
	"context"
	"testing"
)

func TestNewService_DefaultName(t *testing.T) {
	svc := NewService(nil)
	if svc.String() != ServiceName {
		t.Errorf("String() = %q, want %q", svc.String(), ServiceName)
	}
}

func TestNewService_CustomName(t *testing.T) {
	svc := NewService(nil, "my-httpcli")
	if svc.String() != "my-httpcli" {
		t.Errorf("String() = %q, want %q", svc.String(), "my-httpcli")
	}
}

func TestNewService_EmptyNameFallsBackToDefault(t *testing.T) {
	svc := NewService(nil, "")
	if svc.String() != ServiceName {
		t.Errorf("String() = %q, want %q (empty name should fall back to default)", svc.String(), ServiceName)
	}
}

func TestNewService_MultipleNamesUsesFirst(t *testing.T) {
	svc := NewService(nil, "first", "second")
	if svc.String() != "first" {
		t.Errorf("String() = %q, want %q", svc.String(), "first")
	}
}

func TestService_Lifecycle(t *testing.T) {
	cfg := &Config{
		Timeout:   5000000000, // 5s
		UserAgent: "test-svc",
	}
	svc := NewService(cfg)
	ctx := context.Background()

	// Start 前：Client 应为 nil
	if svc.Client() != nil {
		t.Fatal("Client() should be nil before Start")
	}

	// Start 前：State 应为 disconnected
	state, err := svc.State(ctx)
	if err != nil {
		t.Fatalf("State before start failed: %v", err)
	}
	if state != "disconnected" {
		t.Errorf("State before start = %q, want %q", state, "disconnected")
	}

	// Start
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Start 后：Client 不应为 nil
	if svc.Client() == nil {
		t.Fatal("Client() should not be nil after Start")
	}

	// Start 后：State 应为 ready
	state, err = svc.State(ctx)
	if err != nil {
		t.Fatalf("State after start failed: %v", err)
	}
	if state != "ready" {
		t.Errorf("State after start = %q, want %q", state, "ready")
	}

	// 验证 Client 的配置正确
	client := svc.Client()
	if client.cfg.UserAgent != "test-svc" {
		t.Errorf("Client cfg.UserAgent = %q, want %q", client.cfg.UserAgent, "test-svc")
	}

	// Terminate
	if err := svc.Terminate(ctx); err != nil {
		t.Fatalf("Terminate failed: %v", err)
	}
}

func TestService_TerminateBeforeStart(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()

	// Terminate 前 Client 为 nil，不应报错
	if err := svc.Terminate(ctx); err != nil {
		t.Fatalf("Terminate before Start should not error: %v", err)
	}
}

func TestService_StartWithNilConfig(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start with nil config failed: %v", err)
	}
	defer func() { _ = svc.Terminate(ctx) }()

	if svc.Client() == nil {
		t.Fatal("Client() should not be nil after Start")
	}

	// 应使用默认配置
	if svc.Client().cfg.UserAgent != "httpcli" {
		t.Errorf("Client cfg.UserAgent = %q, want %q", svc.Client().cfg.UserAgent, "httpcli")
	}
}

func TestService_StartWithRetryConfig(t *testing.T) {
	retryCfg := DefaultRetryConfig()
	svc := NewService(&Config{
		Timeout: 10000000000, // 10s
		Retry:   retryCfg,
	})
	ctx := context.Background()

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start with retry config failed: %v", err)
	}
	defer func() { _ = svc.Terminate(ctx) }()

	if svc.Client().cfg.Retry == nil {
		t.Fatal("Client cfg.Retry should not be nil")
	}
	if svc.Client().cfg.Retry.MaxRetryCount != 3 {
		t.Errorf("Client cfg.Retry.MaxRetryCount = %d, want 3", svc.Client().cfg.Retry.MaxRetryCount)
	}
}
