package schedx

import (
	"context"
	"sync"
	"time"

	"kemiluo/pkg/logx"

	"go.uber.org/zap"
)

// ServiceName 是注册到 Fiber 生命周期管理时的默认服务名称
const ServiceName = "scheduler"

// JobFunc 周期任务函数。
// 入参 ctx 在调度器 Terminate 时被取消，任务应据此尽快退出；
// 返回的 error 仅用于日志记录，不影响后续周期触发。
type JobFunc func(ctx context.Context) error

// job 单个周期任务的注册信息
type job struct {
	// name 任务名称（用于日志）
	name string
	// interval 触发间隔
	interval time.Duration
	// fn 任务执行函数
	fn JobFunc
}

// Scheduler 轻量周期任务调度器，实现 fiber.Service 接口，随应用生命周期启停。
// 每个任务由独立 goroutine + time.Ticker 驱动；单次执行的 panic 会被捕获，
// 不影响该任务的后续触发，也不影响其他任务与调度器本身。
type Scheduler struct {
	name   string
	jobs   []job
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler 创建调度器实例，name 可选（默认 "scheduler"）
func NewScheduler(name ...string) *Scheduler {
	svcName := ServiceName
	if len(name) > 0 && name[0] != "" {
		svcName = name[0]
	}
	return &Scheduler{name: svcName}
}

// AddJob 注册一个周期任务，必须在 Start 之前调用。
// name: 任务名称；interval: 触发间隔；fn: 任务函数。
// 首次执行发生在启动后的第一个 interval，而非立即执行（避免依赖尚未就绪）。
func (s *Scheduler) AddJob(name string, interval time.Duration, fn JobFunc) *Scheduler {
	if interval <= 0 {
		// interval 非法会导致 time.NewTicker panic，此处拒绝注册并记录错误
		logx.Error("周期任务注册失败：interval 必须大于 0",
			zap.String("job", name),
			zap.Duration("interval", interval),
		)
		return s
	}
	s.jobs = append(s.jobs, job{name: name, interval: interval, fn: fn})
	return s
}

// Start 实现 fiber.Service：为每个已注册任务启动独立 goroutine。
// 该方法立即返回，不阻塞；任务的实际生命周期由内部 context 控制，Terminate 时统一取消。
func (s *Scheduler) Start(_ context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for _, j := range s.jobs {
		s.wg.Add(1)
		go s.run(ctx, j)
		logx.Info("周期任务已启动",
			zap.String("job", j.name),
			zap.Duration("interval", j.interval),
		)
	}
	return nil
}

// run 单个任务的调度循环
func (s *Scheduler) run(ctx context.Context, j job) {
	defer s.wg.Done()
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.exec(ctx, j)
		}
	}
}

// exec 执行单次任务，捕获 panic 并记录耗时与错误
func (s *Scheduler) exec(ctx context.Context, j job) {
	defer func() {
		if r := recover(); r != nil {
			logx.Error("周期任务执行 panic",
				zap.String("job", j.name),
				zap.Any("recover", r),
			)
		}
	}()
	if err := j.fn(ctx); err != nil {
		logx.Warn("周期任务执行返回错误",
			zap.String("job", j.name),
			zap.Error(err),
		)
	}
}

// Terminate 实现 fiber.Service：取消所有任务上下文并等待全部 goroutine 退出。
// 尊重 Fiber 传入的关闭 context：在其 deadline 内等待任务收尾，超时则放弃等待，
// 避免个别卡住的任务（如迟迟不返回的 HTTP 补通知）阻塞整个进程的优雅关闭。
func (s *Scheduler) Terminate(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logx.Info("周期任务调度器已关闭", zap.String("scheduler", s.name))
	case <-ctx.Done():
		logx.Warn("周期任务调度器关闭超时，仍有任务未结束",
			zap.String("scheduler", s.name),
			zap.Error(ctx.Err()),
		)
	}
	return nil
}

// String 实现 fiber.Service，返回服务名称
func (s *Scheduler) String() string {
	return s.name
}

// State 实现 fiber.Service，返回服务状态
func (s *Scheduler) State(_ context.Context) (string, error) {
	if len(s.jobs) == 0 {
		return "no jobs", nil
	}
	return "running", nil
}
