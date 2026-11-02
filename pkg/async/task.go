package async

import "sync"

// Context 异步任务上下文，用于在提交事件时传递参数给处理函数
// data 由 RWMutex 保护，允许多个 handler goroutine 并发读取同一上下文
type Context struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewContext 创建空的任务上下文
func NewContext() *Context {
	return &Context{data: make(map[string]any)}
}

// Set 设置参数
func (c *Context) Set(key string, value any) *Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = value
	return c
}

// Get 获取参数，不存在时返回 nil
func (c *Context) Get(key string) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data[key]
}

// GetString 获取字符串参数
func (c *Context) GetString(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, _ := c.data[key].(string)
	return v
}

// GetInt 获取整型参数
func (c *Context) GetInt(key string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, _ := c.data[key].(int)
	return v
}

// GetInt64 获取 int64 参数
func (c *Context) GetInt64(key string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, _ := c.data[key].(int64)
	return v
}

// Task 异步任务接口
// 所有提交到异步队列的任务必须实现此接口
type Task interface {
	// Execute 执行任务逻辑
	// 任务执行失败应返回 error，由队列根据策略决定是否重试
	Execute() error

	// Name 返回任务名称，用于日志和监控
	Name() string
}

// TaskFunc 函数适配器，将普通函数包装为 Task
type TaskFunc struct {
	name string
	fn   func(ctx *Context) error
	ctx  *Context
}

// NewTaskFunc 创建函数类型任务
func NewTaskFunc(name string, fn func(ctx *Context) error, ctx *Context) *TaskFunc {
	if ctx == nil {
		ctx = NewContext()
	}
	return &TaskFunc{name: name, fn: fn, ctx: ctx}
}

// Execute 执行任务
func (t *TaskFunc) Execute() error {
	return t.fn(t.ctx)
}

// Name 返回任务名称
func (t *TaskFunc) Name() string {
	return t.name
}

// Handler 事件处理函数类型
// 通过 RegisterHandler 注册，通过 Emit 触发
type Handler func(ctx *Context) error
