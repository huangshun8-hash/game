package httpx

import (
	"context"
	"fmt"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

// ServerConfig holds the configuration for the HTTP server.
type ServerConfig struct {
	Name            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	BodyLimit       int
	EnableCORS      bool
	CORSConfig      *cors.Config
	// LogBodySkipPaths 指定不记录请求/响应体的路径（如登录、刷新、存档等含敏感令牌或大报文的接口）。
	// 这些路径仍会记录方法、状态码、耗时等元信息，仅跳过 reqBody/respBody，避免泄漏令牌与日志膨胀。
	LogBodySkipPaths []string
	// Services holds Fiber v3 services that are started/stopped with the app lifecycle.
	Services []fiber.Service
	// TrustedProxies 信任的反代 IP 列表（支持 CIDR，如 "127.0.0.1", "10.0.0.0/8"）。
	// 配置后 Fiber 将从 X-Forwarded-For 读取真实客户端 IP，供限流中间件使用。
	// 未配置时 c.IP() 返回直连 TCP 对端地址（适合无反代的直连部署）。
	TrustedProxies []string
	// ProxyHeader 反代透传真实 IP 所用的 Header 名称（默认 "X-Forwarded-For"）。
	// 仅在 TrustedProxies 不为空时生效。
	ProxyHeader string
	// StaticDir 前端 SPA（如 Vue）打包后的静态文件目录。为空表示不启用静态托管。
	// 该中间件在全局中间件之后、业务路由（含 JWT）之前注册，命中静态文件/SPA 回退时直接响应。
	StaticDir string
	// StaticProxyTarget 可选：后台 API 反向代理目标地址（如 http://localhost:8082）。
	// 为空时 /api 请求交由同端口路由处理（同源集成）；非空时静态中间件将 /api 请求反向代理到该目标。
	StaticProxyTarget string
}

// DefaultServerConfig returns a ServerConfig with sensible defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Name:            "Default",
		Port:            8080,
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    10 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 10 * time.Second,
		BodyLimit:       4 * 1024 * 1024, // 4MB
		EnableCORS:      true,
	}
}

// Server wraps a Fiber app and provides lifecycle management.
type Server struct {
	app  *fiber.App
	cfg  ServerConfig
	port int
}

// NewServer creates a new HTTP server with the given configuration.
// It uses Fiber v3's NewWithCustomCtx to register a custom context factory.
func NewServer(cfg ServerConfig) *Server {
	fiberCfg := fiber.Config{
		AppName:      cfg.Name,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
		BodyLimit:    cfg.BodyLimit,
		ErrorHandler: DefaultErrorHandler,
		JSONEncoder:  sonic.Marshal,
		JSONDecoder:  sonic.Unmarshal,
	}
	if len(cfg.Services) > 0 {
		fiberCfg.Services = cfg.Services
	}
	if len(cfg.TrustedProxies) > 0 {
		proxyHeader := cfg.ProxyHeader
		if proxyHeader == "" {
			proxyHeader = "X-Forwarded-For"
		}
		fiberCfg.TrustProxy = true
		fiberCfg.TrustProxyConfig = fiber.TrustProxyConfig{
			Proxies: cfg.TrustedProxies,
		}
		fiberCfg.ProxyHeader = proxyHeader
	}

	app := fiber.NewWithCustomCtx(func(app *fiber.App) fiber.CustomCtx {
		return NewCtx(app)
	}, fiberCfg)

	s := &Server{
		app:  app,
		cfg:  cfg,
		port: cfg.Port,
	}
	app.Use(recover.New())
	app.Use(requestid.New())
	app.Use(func(c fiber.Ctx) error {
		c.Locals("requestId", requestid.FromContext(c))
		return c.Next()
	})
	app.Use(ServerTimeMiddleware())
	app.Use(LogMiddleware(cfg.LogBodySkipPaths...))
	if cfg.EnableCORS {
		if cfg.CORSConfig != nil {
			app.Use(cors.New(*cfg.CORSConfig))
		} else {
			app.Use(cors.New())
		}
	}
	// Health check 与 Version 内置端点：必须在静态托管中间件之前注册。
	// 否则启用 StaticDir 时，非 /api、非静态文件的请求（如 curl 默认 Accept: */*）
	// 会命中 SPA history 回退，被 index.html 吞掉，导致 /health 返回前端页面。
	app.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"time":   time.Now().UnixMilli(),
		})
	})
	// Version endpoint 返回编译期注入的版本号（ldflags -X），供发布/回滚后核对运行版本
	app.Get("/version", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"service": cfg.Name,
			"version": Version,
			"time":    time.Now().UnixMilli(),
		})
	})
	// 静态托管（Vue 等 SPA 前端页面）中间件：在全局中间件之后、业务路由/JWT 之前注册。
	// 命中的静态文件或 SPA 回退直接响应，不进入后续 JWT 鉴权；/api 交由后续路由或反向代理处理。
	if cfg.StaticDir != "" {
		staticServer := NewStaticServer(cfg.StaticDir, cfg.StaticProxyTarget)
		app.Use(staticServer.Middleware())
	}
	app.Hooks().OnPreStartupMessage(func(p *fiber.PreStartupMessageData) error {
		routes := s.app.GetRoutes(true)
		var b strings.Builder
		fmt.Fprintf(&b, "Server %s Start\n\nRegistered routes:\n", cfg.Name)
		for _, r := range routes {
			if r.Method == fiber.MethodHead {
				continue
			}
			b.WriteString("  ")
			b.WriteString(r.Method)
			b.WriteString(" ")
			b.WriteString(r.Path)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
		p.BannerHeader = b.String()
		return nil
	})

	return s
}

// App returns the underlying Fiber app for route registration.
func (s *Server) App() *fiber.App {
	return s.app
}

// Start listens on the configured port with graceful shutdown support.
// Fiber v3 internally handles SIGINT/SIGTERM via GracefulContext,
// no need to manually set up signal listeners.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return s.app.Listen(addr, fiber.ListenConfig{
		GracefulContext: ctx,
		ShutdownTimeout: s.cfg.ShutdownTimeout,
	})
}
