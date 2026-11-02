package async

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Task 接口测试 ───

func TestTaskFunc_Execute(t *testing.T) {
	var called bool
	ctx := NewContext().Set("key", "value")
	task := NewTaskFunc("test_task", func(c *Context) error {
		called = true
		if c.GetString("key") != "value" {
			t.Fatal("context value not passed")
		}
		return nil
	}, ctx)

	if task.Name() != "test_task" {
		t.Fatalf("expected name 'test_task', got %q", task.Name())
	}

	if err := task.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !called {
		t.Fatal("task function was not called")
	}
}

func TestTaskFunc_ExecuteError(t *testing.T) {
	expectedErr := errors.New("something went wrong")
	task := NewTaskFunc("fail_task", func(c *Context) error {
		return expectedErr
	}, nil)

	err := task.Execute()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

// ─── Context 测试 ───

func TestContext_SetGet(t *testing.T) {
	ctx := NewContext().
		Set("name", "test").
		Set("count", 42).
		Set("id", int64(100))

	if ctx.GetString("name") != "test" {
		t.Fatal("GetString failed")
	}
	if ctx.GetInt("count") != 42 {
		t.Fatal("GetInt failed")
	}
	if ctx.GetInt64("id") != 100 {
		t.Fatal("GetInt64 failed")
	}
	if ctx.Get("missing") != nil {
		t.Fatal("Get on missing key should return nil")
	}
}

// ─── Queue 基础测试 ───

func TestQueue_SubmitAndProcess(t *testing.T) {
	var counter atomic.Int64

	q := newQueue(QueueConfig{
		Name:       "test",
		Workers:    2,
		BufferSize: 256,
		Policy:     PolicyBlock,
		MaxRetries: 0,
	})
	q.start()

	taskCount := 100
	for i := 0; i < taskCount; i++ {
		q.submit(NewTaskFunc("inc", func(c *Context) error {
			counter.Add(1)
			return nil
		}, nil))
	}

	q.stop()

	if got := counter.Load(); got != int64(taskCount) {
		t.Fatalf("expected %d tasks executed, got %d", taskCount, got)
	}
}

func TestQueue_PolicyDrop(t *testing.T) {
	q := newQueue(QueueConfig{
		Name:       "drop_test",
		Workers:    1,
		BufferSize: 2,
		Policy:     PolicyDrop,
	})

	q.submit(NewTaskFunc("t1", func(c *Context) error { return nil }, nil))
	q.submit(NewTaskFunc("t2", func(c *Context) error { return nil }, nil))

	ok := q.submit(NewTaskFunc("t3", func(c *Context) error { return nil }, nil))
	if ok {
		t.Fatal("expected task to be dropped, but it was accepted")
	}

	if q.DroppedCount() != 1 {
		t.Fatalf("expected 1 dropped, got %d", q.DroppedCount())
	}

	q.start()
	q.stop()
}

func TestQueue_PolicyDropSilent(t *testing.T) {
	q := newQueue(QueueConfig{
		Name:       "silent_test",
		Workers:    1,
		BufferSize: 1,
		Policy:     PolicyDropSilent,
	})

	q.submit(NewTaskFunc("t1", func(c *Context) error { return nil }, nil))
	ok := q.submit(NewTaskFunc("t2", func(c *Context) error { return nil }, nil))
	if ok {
		t.Fatal("expected task to be silently dropped")
	}

	if q.DroppedCount() != 1 {
		t.Fatalf("expected 1 dropped, got %d", q.DroppedCount())
	}

	q.start()
	q.stop()
}

func TestQueue_PolicyBlock(t *testing.T) {
	var processed atomic.Int64

	q := newQueue(QueueConfig{
		Name:       "block_test",
		Workers:    1,
		BufferSize: 1,
		Policy:     PolicyBlock,
	})
	q.start()

	taskCount := 10
	for i := 0; i < taskCount; i++ {
		q.submit(NewTaskFunc("block", func(c *Context) error {
			processed.Add(1)
			return nil
		}, nil))
	}

	q.stop()

	if got := processed.Load(); got != int64(taskCount) {
		t.Fatalf("expected %d processed, got %d", taskCount, got)
	}
}

func TestQueue_Retry(t *testing.T) {
	var attempts atomic.Int64

	q := newQueue(QueueConfig{
		Name:       "retry_test",
		Workers:    1,
		BufferSize: 16,
		Policy:     PolicyDrop,
		MaxRetries: 2,
	})
	q.start()

	q.submit(NewTaskFunc("retry", func(c *Context) error {
		attempts.Add(1)
		return errors.New("always fail")
	}, nil))

	q.stop()

	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestQueue_PanicRecovery(t *testing.T) {
	var afterPanic atomic.Bool

	q := newQueue(QueueConfig{
		Name:       "panic_test",
		Workers:    1,
		BufferSize: 16,
		Policy:     PolicyDrop,
		MaxRetries: 0,
	})
	q.start()

	q.submit(NewTaskFunc("panic", func(c *Context) error {
		panic("test panic")
	}, nil))

	q.submit(NewTaskFunc("after", func(c *Context) error {
		afterPanic.Store(true)
		return nil
	}, nil))

	q.stop()

	if !afterPanic.Load() {
		t.Fatal("task after panic was not executed, worker crashed")
	}
}

func TestQueue_ClosedReject(t *testing.T) {
	q := newQueue(QueueConfig{
		Name:       "closed_test",
		Workers:    1,
		BufferSize: 16,
		Policy:     PolicyDrop,
	})
	q.start()
	q.stop()

	ok := q.submit(NewTaskFunc("late", func(c *Context) error { return nil }, nil))
	if ok {
		t.Fatal("expected submit to fail on closed queue")
	}
}

// ─── Service 集成测试 ───

func TestService_Lifecycle(t *testing.T) {
	svc := NewService()
	svc.Register(QueueConfig{Name: "q1", Workers: 2, BufferSize: 64, Policy: PolicyDrop})
	svc.Register(QueueConfig{Name: "q2", Workers: 1, BufferSize: 32, Policy: PolicyBlock, MaxRetries: 1})

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	var count atomic.Int64
	for i := 0; i < 50; i++ {
		svc.SubmitFunc("q1", "inc", func(c *Context) error {
			count.Add(1)
			return nil
		})
	}
	for i := 0; i < 20; i++ {
		svc.SubmitFunc("q2", "inc", func(c *Context) error {
			count.Add(1)
			return nil
		})
	}

	if err := svc.Terminate(context.Background()); err != nil {
		t.Fatalf("terminate failed: %v", err)
	}

	if got := count.Load(); got != 70 {
		t.Fatalf("expected 70 tasks processed, got %d", got)
	}
}

func TestService_SubmitFuncWithContext(t *testing.T) {
	svc := NewService()
	svc.Register(QueueConfig{Name: "q", Workers: 1, BufferSize: 16, Policy: PolicyBlock})
	_ = svc.Start(context.Background())

	var got string
	ctx := NewContext().Set("userId", "U123")
	svc.SubmitFunc("q", "withCtx", func(c *Context) error {
		got = c.GetString("userId")
		return nil
	}, ctx)

	_ = svc.Terminate(context.Background())

	if got != "U123" {
		t.Fatalf("expected 'U123', got %q", got)
	}
}

func TestService_SubmitToNonExistentQueue(t *testing.T) {
	svc := NewService()
	svc.Register(QueueConfig{Name: "exists", Workers: 1, BufferSize: 16, Policy: PolicyDrop})
	_ = svc.Start(context.Background())

	ok := svc.Submit("not_exists", NewTaskFunc("t", func(c *Context) error { return nil }, nil))
	if ok {
		t.Fatal("expected submit to non-existent queue to return false")
	}

	_ = svc.Terminate(context.Background())
}

func TestService_String(t *testing.T) {
	svc := NewService("my_async")
	if svc.String() != "my_async" {
		t.Fatalf("expected name 'my_async', got %q", svc.String())
	}
}

func TestService_State(t *testing.T) {
	svc := NewService()
	svc.Register(QueueConfig{Name: "q", Workers: 1, BufferSize: 8, Policy: PolicyDrop})

	state, err := svc.State(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "running" {
		t.Fatalf("expected 'running', got %q", state)
	}

	emptySvc := NewService()
	state, _ = emptySvc.State(context.Background())
	if state != "no queues" {
		t.Fatalf("expected 'no queues', got %q", state)
	}
}

func TestService_QueueStats(t *testing.T) {
	svc := NewService()
	svc.Register(QueueConfig{Name: "stat_q", Workers: 1, BufferSize: 16, Policy: PolicyDrop})

	stats := svc.QueueStats("stat_q")
	if stats == "" {
		t.Fatal("expected non-empty stats")
	}

	notFound := svc.QueueStats("missing")
	if notFound == "" {
		t.Fatal("expected non-empty not-found message")
	}
}

// ─── 事件模式测试 ───

func TestService_RegisterHandlerAndEmit(t *testing.T) {
	svc := NewService()
	svc.Register(QueueConfig{Name: "log", Workers: 2, BufferSize: 64, Policy: PolicyBlock})
	_ = svc.Start(context.Background())

	var userID string
	var ip string

	svc.RegisterHandler("user.login", "log", func(ctx *Context) error {
		userID = ctx.GetString("userId")
		ip = ctx.GetString("ip")
		return nil
	})

	eventCtx := NewContext().Set("userId", "U456").Set("ip", "192.168.1.1")
	count := svc.Emit("user.login", eventCtx)

	_ = svc.Terminate(context.Background())

	if count != 1 {
		t.Fatalf("expected 1 handler submitted, got %d", count)
	}
	if userID != "U456" {
		t.Fatalf("expected user_id 'U456', got %q", userID)
	}
	if ip != "192.168.1.1" {
		t.Fatalf("expected ip '192.168.1.1', got %q", ip)
	}
}

func TestService_EmitMultipleHandlers(t *testing.T) {
	svc := NewService()
	svc.Register(QueueConfig{Name: "log", Workers: 2, BufferSize: 64, Policy: PolicyBlock})
	svc.Register(QueueConfig{Name: "stat", Workers: 1, BufferSize: 32, Policy: PolicyBlock})
	_ = svc.Start(context.Background())

	var logCalled, statCalled atomic.Bool

	svc.RegisterHandler("user.login", "log", func(ctx *Context) error {
		logCalled.Store(true)
		return nil
	})
	svc.RegisterHandler("user.login", "stat", func(ctx *Context) error {
		statCalled.Store(true)
		return nil
	})

	count := svc.Emit("user.login", nil)
	_ = svc.Terminate(context.Background())

	if count != 2 {
		t.Fatalf("expected 2 handlers submitted, got %d", count)
	}
	if !logCalled.Load() {
		t.Fatal("log handler was not called")
	}
	if !statCalled.Load() {
		t.Fatal("stat handler was not called")
	}
}

func TestService_EmitNoHandlers(t *testing.T) {
	svc := NewService()
	svc.Register(QueueConfig{Name: "q", Workers: 1, BufferSize: 8, Policy: PolicyDrop})

	count := svc.Emit("unknown.event", nil)
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}
}

// ─── DefaultQueueConfig 测试 ───

func TestDefaultQueueConfig(t *testing.T) {
	cfg := DefaultQueueConfig("test_queue")

	if cfg.Name != "test_queue" {
		t.Fatalf("expected name 'test_queue', got %q", cfg.Name)
	}
	if cfg.Workers != 4 {
		t.Fatalf("expected 4 workers, got %d", cfg.Workers)
	}
	if cfg.BufferSize != 1024 {
		t.Fatalf("expected buffer 1024, got %d", cfg.BufferSize)
	}
	if cfg.Policy != PolicyDrop {
		t.Fatalf("expected PolicyDrop, got %d", cfg.Policy)
	}
	if cfg.MaxRetries != 0 {
		t.Fatalf("expected 0 retries, got %d", cfg.MaxRetries)
	}
}

// ─── 并发安全测试 ───

func TestQueue_ConcurrentSubmit(t *testing.T) {
	var processed atomic.Int64

	q := newQueue(QueueConfig{
		Name:       "concurrent",
		Workers:    4,
		BufferSize: 256,
		Policy:     PolicyDrop,
	})
	q.start()

	done := make(chan struct{})
	for g := 0; g < 10; g++ {
		go func() {
			for i := 0; i < 100; i++ {
				q.submit(NewTaskFunc("c", func(c *Context) error {
					processed.Add(1)
					return nil
				}, nil))
			}
			done <- struct{}{}
		}()
	}

	for g := 0; g < 10; g++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for goroutines")
		}
	}

	q.stop()

	got := processed.Load()
	if got < 900 {
		t.Fatalf("expected at least 900 processed (got %d), too many dropped", got)
	}
}
