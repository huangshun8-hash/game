package httpcli

import (
	"context"
)

// ServiceName 是注册到 Fiber 状态管理时的默认服务名称。
const ServiceName = "httpcli"

// Service 实现了 fiber.Service 接口，用于 HTTP 客户端的生命周期管理。
// Fiber 会在应用启动时自动调用 Start，在关闭时调用 Terminate。
type Service struct {
	cfg    *Config
	client *Client
	name   string
}

// NewService 使用指定配置创建一个新的 HTTP 客户端服务。
// 可选的 name 参数用于覆盖默认的服务名称。
func NewService(cfg *Config, name ...string) *Service {
	svcName := ServiceName
	if len(name) > 0 && name[0] != "" {
		svcName = name[0]
	}
	return &Service{
		cfg:  cfg,
		name: svcName,
	}
}

// Start 初始化 HTTP 客户端。
// 它实现了 fiber.Service 接口。
func (s *Service) Start(_ context.Context) error {
	s.client = NewClient(s.cfg)
	return nil
}

// String 返回服务的可读名称，用于启动日志输出。
// 它实现了 fiber.Service 接口。
func (s *Service) String() string {
	return s.name
}

// State 返回 HTTP 客户端服务的当前状态。
// 它实现了 fiber.Service 接口。
func (s *Service) State(_ context.Context) (string, error) {
	if s.client == nil {
		return "disconnected", nil
	}
	return "ready", nil
}

// Terminate 优雅地关闭 HTTP 客户端，释放底层连接资源。
// 它实现了 fiber.Service 接口。
func (s *Service) Terminate(_ context.Context) error {
	if s.client == nil {
		return nil
	}
	s.client.Close()
	return nil
}

// Client 返回服务启动后的底层 httpcli.Client。
// 若服务尚未启动，返回 nil。
func (s *Service) Client() *Client {
	return s.client
}

// Secret 返回服务配置中的内部验签密钥。
// 用于内部服务间接口调用的 MD5 签名验证。
func (s *Service) Secret() string {
	if s.cfg == nil {
		return ""
	}
	return s.cfg.Secret
}
