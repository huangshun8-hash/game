package mongox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// Config holds the configuration for a MongoDB connection.
type Config struct {
	// URI is the MongoDB connection string (e.g., "mongodb://localhost:27017").
	URI string
	// Database is the default database name.
	Database string
	// ConnectTimeout is the timeout for establishing a connection. Default: 10s.
	ConnectTimeout time.Duration
	// PingTimeout is the timeout for the initial ping health check. Default: 5s.
	PingTimeout time.Duration
	// MaxPoolSize is the maximum number of connections in the pool. Default: 100.
	MaxPoolSize uint64
	// MinPoolSize is the minimum number of connections in the pool. Default: 10.
	MinPoolSize uint64
	// MaxIdleTime is the max idle time for a pooled connection. Default: 60s.
	MaxIdleTime time.Duration
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		URI:            "mongodb://localhost:27017",
		Database:       "test",
		ConnectTimeout: 10 * time.Second,
		PingTimeout:    5 * time.Second,
		MaxPoolSize:    100,
		MinPoolSize:    10,
		MaxIdleTime:    60 * time.Second,
	}
}

// Client wraps the official mongo.Client and provides convenient access to databases and collections.
type Client struct {
	raw *mongo.Client
	db  *mongo.Database
	cfg *Config
	mu  sync.RWMutex
}

// NewClient creates a new MongoDB client, establishes the connection, and pings the server.
// Returns an error if the connection or ping fails.
func NewClient(ctx context.Context, cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	opts := options.Client().
		ApplyURI(cfg.URI).
		SetConnectTimeout(cfg.ConnectTimeout).
		SetMaxPoolSize(cfg.MaxPoolSize).
		SetMinPoolSize(cfg.MinPoolSize).
		SetMaxConnIdleTime(cfg.MaxIdleTime)

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("mongox: connect failed: %w", err)
	}

	// Ping to verify the connection is alive
	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()

	if err = client.Ping(pingCtx, readpref.Primary()); err != nil {
		// Ping 失败时主动断开连接，释放后台拓扑监控 goroutine 与连接资源，避免泄漏
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongox: ping failed: %w", err)
	}

	return &Client{
		raw: client,
		db:  client.Database(cfg.Database),
		cfg: cfg,
	}, nil
}

// Raw returns the underlying official mongo.Client.
func (c *Client) Raw() *mongo.Client {
	return c.raw
}

// Database returns the default database instance configured during initialization.
func (c *Client) Database() *mongo.Database {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.db
}

// DB returns a database instance by name. If name is empty, returns the default database.
func (c *Client) DB(name string) *mongo.Database {
	if name == "" {
		return c.Database()
	}
	return c.raw.Database(name)
}

// Collection returns a collection from the default database.
func (c *Client) Collection(name string) *mongo.Collection {
	return c.db.Collection(name)
}

// Close gracefully disconnects the MongoDB client.
func (c *Client) Close(ctx context.Context) error {
	if err := c.raw.Disconnect(ctx); err != nil {
		return fmt.Errorf("mongox: disconnect failed: %w", err)
	}
	return nil
}

// Ping checks the connection to the primary node.
func (c *Client) Ping(ctx context.Context) error {
	return c.raw.Ping(ctx, readpref.Primary())
}
