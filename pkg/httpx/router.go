package httpx

import "github.com/gofiber/fiber/v3"

// Router is the interface that each handler group should implement.
// It registers its own routes onto the given RouterGroup.
type Router interface {
	Register(r RouterGroup)
}

// RouterFunc is an adapter to allow the use of ordinary functions as Router.
type RouterFunc func(r RouterGroup)

func (f RouterFunc) Register(r RouterGroup) {
	f(r)
}

// RouterGroup wraps fiber.Router and provides route registration
// methods that accept httpx.Handler directly, eliminating manual conversion.
type RouterGroup struct {
	raw fiber.Router
}

// NewRouterGroup creates a RouterGroup from a fiber.Router.
func NewRouterGroup(r fiber.Router) RouterGroup {
	return RouterGroup{raw: r}
}

// Group creates a sub-group with the given prefix.
func (g RouterGroup) Group(prefix string) RouterGroup {
	return RouterGroup{raw: g.raw.Group(prefix)}
}

// Use registers middleware handlers.
func (g RouterGroup) Use(handlers ...fiber.Handler) {
	for _, h := range handlers {
		g.raw.Use(h)
	}
}

// Get registers a GET route with an httpx.Handler.
func (g RouterGroup) Get(path string, handler Handler) {
	g.raw.Get(path, ToFiberHandler(handler))
}

// Post registers a POST route with an httpx.Handler.
func (g RouterGroup) Post(path string, handler Handler) {
	g.raw.Post(path, ToFiberHandler(handler))
}

// Put registers a PUT route with an httpx.Handler.
func (g RouterGroup) Put(path string, handler Handler) {
	g.raw.Put(path, ToFiberHandler(handler))
}

// Delete registers a DELETE route with an httpx.Handler.
func (g RouterGroup) Delete(path string, handler Handler) {
	g.raw.Delete(path, ToFiberHandler(handler))
}

// Patch registers a PATCH route with an httpx.Handler.
func (g RouterGroup) Patch(path string, handler Handler) {
	g.raw.Patch(path, ToFiberHandler(handler))
}

// Raw returns the underlying fiber.Router for cases needing direct access.
func (g RouterGroup) Raw() fiber.Router {
	return g.raw
}

// RegisterRouters registers multiple Router implementations onto the server's app.
// Optionally provide a prefix to group them under a common path.
func (s *Server) RegisterRouters(prefix string, routers ...Router) {
	var r fiber.Router
	if prefix != "" {
		r = s.app.Group(prefix)
	} else {
		r = s.app
	}

	g := NewRouterGroup(r)
	for _, router := range routers {
		router.Register(g)
	}
}
