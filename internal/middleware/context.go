// Package middleware holds the HTTP middleware for the client-directory service.
//
// Every constructor returns a plain func(http.Handler) http.Handler, which is
// assignable to router.Middleware, so this package does not import router.
package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey int

const (
	peerIDKey ctxKey = iota
	requestIDKey
)

// WithPeerID returns a context carrying the authenticated peer's ID.
func WithPeerID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, peerIDKey, id)
}

// PeerIDFromContext returns the authenticated peer's ID. ok is false on
// routes that did not pass through Authenticate.
func PeerIDFromContext(ctx context.Context) (id uuid.UUID, ok bool) {
	id, ok = ctx.Value(peerIDKey).(uuid.UUID)
	return id, ok
}

// RequestIDFromContext returns the request ID set by RequestID, or "".
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// writeError writes a JSON error body: {"error": "..."}.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
