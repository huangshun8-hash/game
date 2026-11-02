package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config 保存 Redis 连接配置。
type Config struct {
	// Addr 是 Redis 服务器地址，格式为 "host:port"（例如 "localhost:6379"）。
	Addr string
	// Password 是 Redis 服务器密码，为空表示无密码。
	Password string
	// DB 是使用的数据库编号，默认为 0。
	DB int
	// PoolSize 是连接池最大连接数，默认为 10。
	PoolSize int
	// MinIdleConns 是连接池中最小空闲连接数，默认为 5。
	MinIdleConns int
	// DialTimeout 是建立连接的超时时间，默认为 5s。
	DialTimeout time.Duration
	// ReadTimeout 是读取操作的超时时间，默认为 3s。
	ReadTimeout time.Duration
	// WriteTimeout 是写入操作的超时时间，默认为 3s。
	WriteTimeout time.Duration
	// PingTimeout 是初始 Ping 健康检查的超时时间，默认为 5s。
	PingTimeout time.Duration
	// MaxRetries 是命令执行失败时的最大重试次数，默认为 3。
	MaxRetries int
}

// DefaultConfig 返回带有合理默认值的 Config。
func DefaultConfig() *Config {
	return &Config{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           0,
		PoolSize:     10,
		MinIdleConns: 5,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PingTimeout:  5 * time.Second,
		MaxRetries:   3,
	}
}

// Client 封装了官方 go-redis 客户端，提供便捷的 Redis 操作方法。
type Client struct {
	raw *redis.Client
	cfg *Config
}

// NewClient 创建一个新的 Redis 客户端，建立连接并通过 Ping 验证连接可用性。
// 若连接或 Ping 失败，将返回错误。
func NewClient(ctx context.Context, cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	opts := &redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		MaxRetries:   cfg.MaxRetries,
	}

	raw := redis.NewClient(opts)

	// 通过 Ping 验证连接是否正常
	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()

	if err := raw.Ping(pingCtx).Err(); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("redisx: ping failed: %w", err)
	}

	return &Client{
		raw: raw,
		cfg: cfg,
	}, nil
}

// Raw 返回底层的官方 redis.Client。
func (c *Client) Raw() *redis.Client {
	return c.raw
}

// Get 获取指定 key 对应的字符串值。
// 若 key 不存在，返回 redis.Nil 错误。
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	val, err := c.raw.Get(ctx, key).Result()
	if err != nil {
		return "", fmt.Errorf("redisx: get key %q failed: %w", key, err)
	}
	return val, nil
}

// MGet 批量获取多个 key 的值。
// 返回结果与传入的 keys 顺序一一对应，key 不存在时对应位置为 nil。
// 用于批量读取场景，避免逐 key 多次往返。
func (c *Client) MGet(ctx context.Context, keys ...string) ([]interface{}, error) {
	vals, err := c.raw.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("redisx: mget %d keys failed: %w", len(keys), err)
	}
	return vals, nil
}

// Set 设置指定 key 的值，并可选设置过期时间。
// expiration 为 0 表示不设置过期时间。
func (c *Client) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	if err := c.raw.Set(ctx, key, value, expiration).Err(); err != nil {
		return fmt.Errorf("redisx: set key %q failed: %w", key, err)
	}
	return nil
}

// SetNX 仅当 key 不存在时设置其值，并设置过期时间，返回是否设置成功。
// 常用于一次性令牌（nonce）去重、分布式锁等"首次写入"语义的场景：
// 返回 true 表示本次为首次写入（key 之前不存在）；返回 false 表示 key 已存在（重复）。
func (c *Client) SetNX(ctx context.Context, key string, value any, expiration time.Duration) (bool, error) {
	ok, err := c.raw.SetNX(ctx, key, value, expiration).Result()
	if err != nil {
		return false, fmt.Errorf("redisx: setnx key %q failed: %w", key, err)
	}
	return ok, nil
}

// Del 删除指定 key，若 key 不存在不会返回错误。
func (c *Client) Del(ctx context.Context, key string) error {
	if err := c.raw.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redisx: del key %q failed: %w", key, err)
	}
	return nil
}

// Eval 执行 Lua 脚本，用于需要原子性的复合操作（如"比较版本再写入"）。
// keys 为脚本操作的 key 列表，args 为附加参数；返回脚本执行结果。
// 相比多次往返 + 客户端判断，Lua 在服务端原子执行，可避免并发下的竞态。
func (c *Client) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	res, err := c.raw.Eval(ctx, script, keys, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("redisx: eval script failed: %w", err)
	}
	return res, nil
}

// Ping 检查与 Redis 服务器的连接是否正常。
func (c *Client) Ping(ctx context.Context) error {
	return c.raw.Ping(ctx).Err()
}

// Close 关闭 Redis 客户端，释放连接资源。
func (c *Client) Close() error {
	if err := c.raw.Close(); err != nil {
		return fmt.Errorf("redisx: close failed: %w", err)
	}
	return nil
}
