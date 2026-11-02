package httpcli

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3/addon/retry"
	"github.com/gofiber/fiber/v3/client"
)

// RetryConfig 是重试配置的别名，底层使用 Fiber v3 的带抖动指数退避重试策略。
type RetryConfig = retry.Config

// Config 保存 HTTP 客户端的连接和请求配置。
type Config struct {
	// BaseURL 是所有请求的基础 URL 前缀，例如 "https://api.example.com"。
	BaseURL string
	// Timeout 是单个请求的超时时间，为 0 表示不设置超时。
	Timeout time.Duration
	// UserAgent 是请求的 User-Agent 头。
	UserAgent string
	// Headers 是应用到所有请求的默认请求头。
	Headers map[string]string
	// Retry 是可选的重试配置。若不为 nil，则所有请求将启用带抖动的指数退避重试。
	Retry *RetryConfig
	// Secret 内部服务间验签密钥（可选，用于 MD5 签名验证）
	Secret string
}

// DefaultConfig 返回带有合理默认值的 Config。
func DefaultConfig() *Config {
	return &Config{
		Timeout:   30 * time.Second,
		UserAgent: "httpcli",
	}
}

// DefaultRetryConfig 返回带有合理默认值的重试配置。
// 默认采用指数退避策略：初始间隔 1s，最大退避 32s，乘数 2.0，最大重试 3 次。
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		InitialInterval: 1 * time.Second,
		MaxBackoffTime:  32 * time.Second,
		Multiplier:      2.0,
		MaxRetryCount:   3,
	}
}

// Client 封装了 Fiber v3 的 HTTP 客户端，提供便捷的请求方法。
// 支持普通 GET/POST 请求，以及通过 Config.Retry 配置的可重试请求。
type Client struct {
	raw *client.Client
	cfg *Config
}

// NewClient 使用指定配置创建一个新的 HTTP 客户端。
// 若 cfg 为 nil，则使用默认配置。
func NewClient(cfg *Config) *Client {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	c := client.New()
	c.SetJSONMarshal(sonic.Marshal)
	c.SetJSONUnmarshal(sonic.Unmarshal)

	if cfg.BaseURL != "" {
		c.SetBaseURL(cfg.BaseURL)
	}
	if cfg.Timeout > 0 {
		c.SetTimeout(cfg.Timeout)
	}
	if cfg.UserAgent != "" {
		c.SetUserAgent(cfg.UserAgent)
	}
	for k, v := range cfg.Headers {
		c.SetHeader(k, v)
	}
	if cfg.Retry != nil {
		c.SetRetryConfig(cfg.Retry)
	}

	return &Client{
		raw: c,
		cfg: cfg,
	}
}

// Raw 返回底层的 Fiber 客户端实例，用于需要直接操作底层客户端的场景。
func (c *Client) Raw() *client.Client {
	return c.raw
}

// Get 发送 GET 请求到指定 URL。
// opts 用于设置请求级别的请求头、查询参数、超时等选项。
// 返回的 Response 需在使用后调用 Close 释放资源。
func (c *Client) Get(ctx context.Context, url string, opts ...Option) (*Response, error) {
	cfg := applyOptions(opts...)
	cfg.Ctx = ctx
	resp, err := c.raw.Get(url, cfg)
	if err != nil {
		return nil, fmt.Errorf("httpcli: GET %s failed: %w", url, err)
	}
	return &Response{raw: resp}, nil
}

// Post 发送 POST 请求到指定 URL。
// body 会根据类型自动处理：[]byte 和 string 作为原始请求体，其他类型序列化为 JSON。
// opts 用于设置请求级别的请求头、查询参数、超时等选项。
// 返回的 Response 需在使用后调用 Close 释放资源。
func (c *Client) Post(ctx context.Context, url string, body any, opts ...Option) (*Response, error) {
	cfg := applyOptions(opts...)
	cfg.Ctx = ctx
	if body != nil {
		cfg.Body = body
	}
	resp, err := c.raw.Post(url, cfg)
	if err != nil {
		return nil, fmt.Errorf("httpcli: POST %s failed: %w", url, err)
	}
	return &Response{raw: resp}, nil
}

// Put 发送 PUT 请求到指定 URL。
// body 会根据类型自动处理：[]byte 和 string 作为原始请求体，其他类型序列化为 JSON。
// 返回的 Response 需在使用后调用 Close 释放资源。
func (c *Client) Put(ctx context.Context, url string, body any, opts ...Option) (*Response, error) {
	cfg := applyOptions(opts...)
	cfg.Ctx = ctx
	if body != nil {
		cfg.Body = body
	}
	resp, err := c.raw.Put(url, cfg)
	if err != nil {
		return nil, fmt.Errorf("httpcli: PUT %s failed: %w", url, err)
	}
	return &Response{raw: resp}, nil
}

// Delete 发送 DELETE 请求到指定 URL。
// 返回的 Response 需在使用后调用 Close 释放资源。
func (c *Client) Delete(ctx context.Context, url string, opts ...Option) (*Response, error) {
	cfg := applyOptions(opts...)
	cfg.Ctx = ctx
	resp, err := c.raw.Delete(url, cfg)
	if err != nil {
		return nil, fmt.Errorf("httpcli: DELETE %s failed: %w", url, err)
	}
	return &Response{raw: resp}, nil
}

// Patch 发送 PATCH 请求到指定 URL。
// body 会根据类型自动处理：[]byte 和 string 作为原始请求体，其他类型序列化为 JSON。
// 返回的 Response 需在使用后调用 Close 释放资源。
func (c *Client) Patch(ctx context.Context, url string, body any, opts ...Option) (*Response, error) {
	cfg := applyOptions(opts...)
	cfg.Ctx = ctx
	if body != nil {
		cfg.Body = body
	}
	resp, err := c.raw.Patch(url, cfg)
	if err != nil {
		return nil, fmt.Errorf("httpcli: PATCH %s failed: %w", url, err)
	}
	return &Response{raw: resp}, nil
}

// Close 关闭客户端，释放底层空闲连接资源。
func (c *Client) Close() {
	c.raw.CloseIdleConnections()
}

// Option 用于配置单个请求的参数，如请求头、查询参数、超时等。
type Option func(*client.Config)

// WithHeader 设置单个请求头。
func WithHeader(key, val string) Option {
	return func(c *client.Config) {
		if c.Header == nil {
			c.Header = make(map[string]string)
		}
		c.Header[key] = val
	}
}

// WithHeaders 批量设置请求头。
func WithHeaders(headers map[string]string) Option {
	return func(c *client.Config) {
		if c.Header == nil {
			c.Header = make(map[string]string)
		}
		for k, v := range headers {
			c.Header[k] = v
		}
	}
}

// WithParam 设置单个查询参数。
func WithParam(key, val string) Option {
	return func(c *client.Config) {
		if c.Param == nil {
			c.Param = make(map[string]string)
		}
		c.Param[key] = val
	}
}

// WithParams 批量设置查询参数。
func WithParams(params map[string]string) Option {
	return func(c *client.Config) {
		if c.Param == nil {
			c.Param = make(map[string]string)
		}
		for k, v := range params {
			c.Param[k] = v
		}
	}
}

// WithCookie 设置单个 Cookie。
func WithCookie(key, val string) Option {
	return func(c *client.Config) {
		if c.Cookie == nil {
			c.Cookie = make(map[string]string)
		}
		c.Cookie[key] = val
	}
}

// WithCookies 批量设置 Cookie。
func WithCookies(cookies map[string]string) Option {
	return func(c *client.Config) {
		if c.Cookie == nil {
			c.Cookie = make(map[string]string)
		}
		for k, v := range cookies {
			c.Cookie[k] = v
		}
	}
}

// WithTimeout 设置单个请求的超时时间，覆盖客户端级别的超时配置。
func WithTimeout(timeout time.Duration) Option {
	return func(c *client.Config) {
		c.Timeout = timeout
	}
}

// WithUserAgent 设置单个请求的 User-Agent 头。
func WithUserAgent(ua string) Option {
	return func(c *client.Config) {
		c.UserAgent = ua
	}
}

// WithMaxRedirects 设置最大重定向次数。
func WithMaxRedirects(max int) Option {
	return func(c *client.Config) {
		c.MaxRedirects = max
	}
}

// applyOptions 将多个 Option 应用到一个新的请求配置上并返回。
func applyOptions(opts ...Option) client.Config {
	cfg := client.Config{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}
