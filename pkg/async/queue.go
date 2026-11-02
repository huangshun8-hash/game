package async

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"kemiluo/pkg/logx"

	"go.uber.org/zap"
)

const (
	// retryBaseBackoff 重试退避基准间隔
	retryBaseBackoff = 100 * time.Millisecond
	// retryMaxBackoff 重试退避上限，避免优雅关闭时排空阶段被长时间阻塞
	retryMaxBackoff = 2 * time.Second
)

// OverflowPolicy 队列满时的溢出策略
type OverflowPolicy int

const (
	// PolicyDrop 丢弃任务并告警
	PolicyDrop OverflowPolicy = iota
	// PolicyBlock 阻塞等待直到有空间
	PolicyBlock
	// PolicyDropSilent 静默丢弃（不告警，适用于统计类）
	PolicyDropSilent
)

// QueueConfig 队列配置
type QueueConfig struct {
	// Name 队列名称
	Name string
	// Workers Worker数量
	Workers int
	// BufferSize 缓冲区大小
	BufferSize int
	// Policy 队列满时的溢出策略
	Policy OverflowPolicy
	// MaxRetries 任务最大重试次数（0表示不重试）
	MaxRetries int
}

// Queue 异步任务队列
// 使用 channel + 固定数量 Worker 的模式处理任务
type Queue struct {
	cfg     QueueConfig
	ch      chan Task
	done    chan struct{} // 关闭信号，用于解除 PolicyBlock 的阻塞
	wg      sync.WaitGroup
	closed  atomic.Bool
	dropped atomic.Int64
}

// newQueue 创建队列实例（由 Center 调用）
func newQueue(cfg QueueConfig) *Queue {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.BufferSize < 1 {
		cfg.BufferSize = 256
	}

	return &Queue{
		cfg:  cfg,
		ch:   make(chan Task, cfg.BufferSize),
		done: make(chan struct{}),
	}
}

// start 启动所有 Worker
func (q *Queue) start() {
	for i := 0; i < q.cfg.Workers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
}

// worker 消费者协程
// 优先消费任务，收到关闭信号后排空剩余任务再退出
func (q *Queue) worker() {
	defer q.wg.Done()

	for {
		select {
		case <-q.done:
			// 收到关闭信号，排空 channel 中剩余任务后退出
			q.drain()
			return
		case task, ok := <-q.ch:
			if !ok {
				return
			}
			q.executeWithRetry(task)
		}
	}
}

// drain 排空 channel 中剩余的任务并执行
func (q *Queue) drain() {
	for {
		select {
		case task, ok := <-q.ch:
			if !ok {
				return
			}
			q.executeWithRetry(task)
		default:
			return
		}
	}
}

// executeWithRetry 带重试的任务执行
// 失败后按指数退避重试（有上限），每次失败均记录日志便于排障；panic 视为不可恢复，不再重试。
func (q *Queue) executeWithRetry(task Task) {
	maxAttempts := q.cfg.MaxRetries + 1
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err, isPanic := q.runOnce(task, attempt)

		// panic 视为不可恢复错误，不再重试
		if isPanic {
			return
		}
		// 执行成功，结束
		if err == nil {
			return
		}

		// 已达最大重试次数，记录最终失败
		if attempt >= maxAttempts {
			logx.Warn("异步任务执行失败（已达最大重试）",
				zap.String("queue", q.cfg.Name),
				zap.String("task", task.Name()),
				zap.Int("attempts", attempt),
				zap.Error(err),
			)
			return
		}

		// 中间失败也记录，便于排查偶发错误
		logx.Warn("异步任务执行失败，准备重试",
			zap.String("queue", q.cfg.Name),
			zap.String("task", task.Name()),
			zap.Int("attempt", attempt),
			zap.Error(err),
		)

		// 指数退避后重试（不监听关闭信号，保证排空阶段重试能完整执行）
		time.Sleep(backoffDuration(attempt))
	}
}

// runOnce 执行任务一次，捕获 panic。返回执行错误以及是否发生 panic。
func (q *Queue) runOnce(task Task, attempt int) (err error, isPanic bool) {
	defer func() {
		if r := recover(); r != nil {
			isPanic = true
			logx.Error("异步任务panic",
				zap.String("queue", q.cfg.Name),
				zap.String("task", task.Name()),
				zap.Int("attempt", attempt),
				zap.Any("panic", r),
			)
		}
	}()
	return task.Execute(), false
}

// backoffDuration 计算第 attempt 次重试前的退避时长（指数退避，带上限）
func backoffDuration(attempt int) time.Duration {
	backoff := retryBaseBackoff << (attempt - 1)
	if backoff > retryMaxBackoff || backoff <= 0 {
		backoff = retryMaxBackoff
	}
	return backoff
}

// submit 提交任务到队列
func (q *Queue) submit(task Task) bool {
	if q.closed.Load() {
		return false
	}

	switch q.cfg.Policy {
	case PolicyBlock:
		// 使用 select 同时监听 done 信号，避免关闭时永久阻塞或写入已关闭 channel
		select {
		case <-q.done:
			// 队列正在关闭，放弃提交
			logx.Warn("队列关闭中，阻塞提交被中断",
				zap.String("queue", q.cfg.Name),
				zap.String("task", task.Name()),
			)
			return false
		case q.ch <- task:
			return true
		}
	default:
		// 非阻塞策略：将关闭信号检查与 channel 写入拆为两步，
		// 避免单 select 中 done 与写入同时就绪时随机命中写入、导致关闭后任务落入无人消费的 channel
		select {
		case <-q.done:
			if q.cfg.Policy == PolicyDrop {
				logx.Warn("队列关闭中，任务提交被拒绝",
					zap.String("queue", q.cfg.Name),
					zap.String("task", task.Name()),
				)
			}
			return false
		default:
		}
		select {
		case q.ch <- task:
			return true
		default:
			q.dropped.Add(1)
			if q.cfg.Policy == PolicyDrop {
				logx.Warn("异步队列已满，任务被丢弃",
					zap.String("queue", q.cfg.Name),
					zap.String("task", task.Name()),
					zap.Int64("totalDropped", q.dropped.Load()),
				)
			}
			return false
		}
	}
}

// stop 关闭队列，等待所有任务处理完毕
func (q *Queue) stop() {
	q.closed.Store(true)
	close(q.done) // 发送关闭信号，解除阻塞的 submit 并通知 worker 退出
	q.wg.Wait()
}

// Len 返回队列中当前待处理的任务数量
func (q *Queue) Len() int {
	return len(q.ch)
}

// DroppedCount 返回被丢弃的任务总数
func (q *Queue) DroppedCount() int64 {
	return q.dropped.Load()
}

// Stats 返回队列统计信息
func (q *Queue) Stats() string {
	return fmt.Sprintf("queue=%s pending=%d dropped=%d", q.cfg.Name, q.Len(), q.DroppedCount())
}
