package mongox

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// ServiceName is the default service name registered with Fiber's state management.
const ServiceName = "mongodb"

// Service implements the fiber.Service interface for MongoDB lifecycle management.
// Fiber will call Start on application startup and Terminate on shutdown automatically.
type Service struct {
	cfg    *Config
	client *Client
	name   string
}

// NewService creates a new MongoDB service with the given configuration.
// The optional name parameter overrides the default service name.
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

// Start initializes the MongoDB connection.
// It implements the fiber.Service interface.
func (s *Service) Start(ctx context.Context) error {
	client, err := NewClient(ctx, s.cfg)
	if err != nil {
		return fmt.Errorf("mongox service: start failed: %w", err)
	}
	s.client = client
	return nil
}

// String returns a human-readable name of the service for the startup message.
// It implements the fiber.Service interface.
func (s *Service) String() string {
	return s.name
}

// State returns the current state of the MongoDB service.
// It implements the fiber.Service interface.
func (s *Service) State(ctx context.Context) (string, error) {
	if s.client == nil {
		return "disconnected", nil
	}
	if err := s.client.raw.Ping(ctx, readpref.Primary()); err != nil {
		return "unhealthy", nil
	}
	return "connected", nil
}

// Terminate gracefully disconnects the MongoDB client.
// It implements the fiber.Service interface.
func (s *Service) Terminate(ctx context.Context) error {
	if s.client == nil {
		return nil
	}
	return s.client.Close(ctx)
}

// Client returns the underlying mongox.Client after the service has started.
// Returns nil if the service has not been started yet.
func (s *Service) Client() *Client {
	return s.client
}
