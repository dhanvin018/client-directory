// Package router wires HTTP routes to handlers for the client-directory service.
//
// Requires Go 1.22+ (method-aware ServeMux patterns and Request.PathValue).
// Handlers read path parameters with r.PathValue("roomId"), r.PathValue("docId"), etc.
package router

import (
	"net/http"
	"strings"
)

const apiV1 = "/api/v1"

// Middleware wraps a handler. In a list, the first middleware is outermost.
type Middleware func(http.Handler) http.Handler

// Handlers groups the handler-layer implementations the router dispatches to.
type Handlers struct {
	Auth      AuthHandler
	Peers     PeerHandler
	Rooms     RoomHandler
	Documents DocumentHandler
	Members   MemberHandler
	Events    EventHandler
	Invites   InviteHandler
}

// Config is everything New needs.
type Config struct {
	Handlers Handlers

	// Authenticate rejects unauthenticated requests with 401 and puts the
	// calling peer into the request context. Applied to every route except
	// /healthz and the OAuth login/callback routes.
	Authenticate Middleware

	// Global middleware applied to every request, outermost first
	// (panic recovery, request logging, CORS, ...).
	Global []Middleware
}

// New builds the service's root http.Handler.
//
// Route registration panics on conflicting patterns, so a bad route table
// fails at startup rather than at request time.
func New(cfg Config) http.Handler {
	if cfg.Authenticate == nil {
		panic("router: Config.Authenticate is required")
	}

	mux := http.NewServeMux()

	public := group{mux: mux}
	authed := public.with(cfg.Authenticate)
	api := authed.under(apiV1)

	public.handle("GET /healthz", healthz)

	registerAuthRoutes(public, authed, cfg.Handlers.Auth)
	registerPeerRoutes(api, cfg.Handlers.Peers)
	registerRoomRoutes(api, cfg.Handlers.Rooms)
	registerDocumentRoutes(api, cfg.Handlers.Documents)
	registerMemberRoutes(api, cfg.Handlers.Members)
	registerEventRoutes(api, cfg.Handlers.Events)
	registerInviteRoutes(api, cfg.Handlers.Invites)

	return chain(mux, cfg.Global)
}

// group registers routes under a shared path prefix and middleware stack.
type group struct {
	mux    *http.ServeMux
	prefix string
	mw     []Middleware
}

// with returns a copy of the group with extra middleware appended (innermost).
func (g group) with(mw ...Middleware) group {
	g.mw = append(append([]Middleware(nil), g.mw...), mw...)
	return g
}

// under returns a copy of the group with prefix appended to its path prefix.
func (g group) under(prefix string) group {
	g.prefix += prefix
	return g
}

// handle registers a handler for a "METHOD /path" pattern.
func (g group) handle(pattern string, h http.HandlerFunc) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		panic(`router: pattern must be "METHOD /path": ` + pattern)
	}
	g.mux.Handle(method+" "+g.prefix+path, chain(h, g.mw))
}

// chain wraps h in mw, first middleware outermost.
func chain(h http.Handler, mw []Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
