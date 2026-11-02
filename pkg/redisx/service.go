package redisx

import (
	"context"
	"fmt"
)

// ServiceName 是注册到 Fiber 状态管理时的默认服务名称。
const ServiceName = "redis"

// Service 实现了 fiber.Service 接口，用于 Redis 连接的生命周期管理。
// Fiber 会在应用启动时自动调用 Start，在关闭时调用 Terminate。
type Service struct {
	cfg    *Config
	client *Client
	name   string
}

// NewService 使用指定配置创建一个新的 Redis 服务。
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

// Start 初始化 Redis 连接。
// 它实现了 fiber.Service 接口。
func (s *Service) Start(ctx context.Context) error {
	client, err := NewClient(ctx, s.cfg)
	if err != nil {
		return fmt.Errorf("redisx service: start failed: %w", err)
	}
	s.client = client
	return nil
}

// String 返回服务的可读名称，用于启动日志输出。
// 它实现了 fiber.Service 接口。
func (s *Service) String() string {
	return s.name
}

// State 返回 Redis 服务的当前状态。
// 它实现了 fiber.Service 接口。
func (s *Service) State(ctx context.Context) (string, error) {
	if s.client == nil {
		return "disconnected", nil
	}
	if err := s.client.raw.Ping(ctx).Err(); err != nil {
		return "unhealthy", nil
	}
	return "connected", nil
}

// Terminate 优雅地关闭 Redis 客户端连接。
// 它实现了 fiber.Service 接口。
func (s *Service) Terminate(_ context.Context) error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// Client 返回服务启动后的底层 redisx.Client。
// 若服务尚未启动，返回 nil。
func (s *Service) Client() *Client {
	return s.client
}
