package async

import (
	"context"
	"fmt"
	"sync"

	"kemiluo/pkg/logx"

	"go.uber.org/zap"
)

// ServiceName 是注册到 Fiber 状态管理时的默认服务名称
const ServiceName = "async"

// DefaultQueueConfig 返回单个队列的默认配置
func DefaultQueueConfig(name string) QueueConfig {
	return QueueConfig{
		Name:       name,
		Workers:    4,
		BufferSize: 1024,
		Policy:     PolicyDrop,
		MaxRetries: 0,
	}
}

// handlerEntry 注册的处理函数条目
type handlerEntry struct {
	queue   string
	handler Handler
}

// Service 异步任务中心，实现 fiber.Service 接口
// 管理多个独立队列，由 Fiber 负责生命周期（Start/Terminate）
// 支持两种使用模式：
//   - 直接提交：Submit / SubmitFunc 提交具体任务
//   - 事件模式：RegisterHandler 注册处理函数，Emit 触发事件
type Service struct {
	queues   map[string]*Queue
	handlers map[string][]handlerEntry
	mu       sync.RWMutex
	name     string
}

// NewService 创建异步任务服务
func NewService(name ...string) *Service {
	svcName := ServiceName
	if len(name) > 0 && name[0] != "" {
		svcName = name[0]
	}

	return &Service{
		queues:   make(map[string]*Queue),
		handlers: make(map[string][]handlerEntry),
		name:     svcName,
	}
}

// Register 注册一个队列配置，必须在 Start 之前调用
func (s *Service) Register(cfg QueueConfig) *Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queues[cfg.Name] = newQueue(cfg)
	return s
}

// RegisterHandler 注册事件处理函数
// event: 事件名称（如 "user.login"、"order.paid"）
// queue: 目标队列名称，处理函数将在该队列的 Worker 中执行
// handler: 处理函数，接收 Context 参数
func (s *Service) RegisterHandler(event string, queue string, handler Handler) *Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[event] = append(s.handlers[event], handlerEntry{
		queue:   queue,
		handler: handler,
	})
	return s
}

// Emit 触发事件，将事件分发到所有已注册的处理函数
// 每个处理函数作为独立任务提交到对应队列异步执行
// 返回成功提交的处理函数数量
func (s *Service) Emit(event string, ctx *Context) int {
	if ctx == nil {
		ctx = NewContext()
	}

	s.mu.RLock()
	entries, ok := s.handlers[event]
	s.mu.RUnlock()

	if !ok || len(entries) == 0 {
		return 0
	}

	submitted := 0
	for _, entry := range entries {
		// 闭包捕获当前 entry
		e := entry
		task := NewTaskFunc(event, e.handler, ctx)
		if s.Submit(e.queue, task) {
			submitted++
		}
	}
	return submitted
}

// Start 启动所有队列的 Worker
// 实现 fiber.Service 接口
func (s *Service) Start(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, q := range s.queues {
		q.start()
		logx.Info("异步队列启动",
			zap.String("queue", name),
			zap.Int("workers", q.cfg.Workers),
			zap.Int("buffer", q.cfg.BufferSize),
		)
	}
	return nil
}

// String 返回服务名称
// 实现 fiber.Service 接口
func (s *Service) String() string {
	return s.name
}

// State 返回服务状态
// 实现 fiber.Service 接口
func (s *Service) State(_ context.Context) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.queues) == 0 {
		return "no queues", nil
	}
	return "running", nil
}

// Terminate 优雅关闭所有队列，等待任务处理完毕
// 实现 fiber.Service 接口
func (s *Service) Terminate(_ context.Context) error {
	// 先加锁收集队列引用到局部切片，释放锁后再逐个停止，
	// 避免在持锁期间调用 q.stop()（内部等待 worker）时，任务 handler 回调 Submit/Emit 等需要读锁的方法造成死锁
	s.mu.Lock()
	queues := make([]*Queue, 0, len(s.queues))
	for _, q := range s.queues {
		queues = append(queues, q)
	}
	s.mu.Unlock()

	for _, q := range queues {
		logx.Info("异步队列关闭中", zap.String("queue", q.cfg.Name), zap.Int("pending", q.Len()))
		q.stop()
		logx.Info("异步队列已关闭", zap.String("queue", q.cfg.Name))
	}
	return nil
}

// Submit 提交任务到指定队列
// queueName: 目标队列名称
// task: 需要执行的任务
// 返回 false 表示任务被丢弃或队列不存在
func (s *Service) Submit(queueName string, task Task) bool {
	s.mu.RLock()
	q, ok := s.queues[queueName]
	s.mu.RUnlock()

	if !ok {
		logx.Warn("提交任务到不存在的队列",
			zap.String("queue", queueName),
			zap.String("task", task.Name()),
		)
		return false
	}

	return q.submit(task)
}

// SubmitFunc 提交函数类型任务到指定队列（便捷方法）
// ctx 为可选参数，传递给处理函数
func (s *Service) SubmitFunc(queueName string, taskName string, fn func(ctx *Context) error, ctx ...*Context) bool {
	var c *Context
	if len(ctx) > 0 {
		c = ctx[0]
	}
	return s.Submit(queueName, NewTaskFunc(taskName, fn, c))
}

// QueueStats 返回指定队列的统计信息
func (s *Service) QueueStats(queueName string) string {
	s.mu.RLock()
	q, ok := s.queues[queueName]
	s.mu.RUnlock()

	if !ok {
		return fmt.Sprintf("queue=%s not found", queueName)
	}
	return q.Stats()
}
