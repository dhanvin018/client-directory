package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// ErrInvalidToken is what a TokenVerifier returns for a token that is
// unknown, expired or revoked. Any other error is treated as an
// infrastructure failure (e.g. the session store is down) and answered with
// 503 rather than 401, so an outage doesn't look like a forced logout.
var ErrInvalidToken = errors.New("invalid or expired session")

// TokenVerifier turns a session token into the peer it belongs to. The
// implementation (opaque sessions in a store, signed tokens, ...) is chosen
// elsewhere; the middleware only needs this contract.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (peerID uuid.UUID, err error)
}

type AuthConfig struct {
	Verifier TokenVerifier

	// CookieName, if set, lets the token also arrive as a cookie when there
	// is no Authorization header. Leave empty for bearer-header-only auth.
	// Cookie auth makes state-changing routes CSRF-relevant: use
	// SameSite cookies and keep CORS origins explicit.
	CookieName string

	Logger *slog.Logger // defaults to slog.Default()
}

// Authenticate requires a valid session on every request it wraps and stores
// the peer's ID in the request context (see PeerIDFromContext).
func Authenticate(cfg AuthConfig) func(http.Handler) http.Handler {
	if cfg.Verifier == nil {
		panic("middleware: AuthConfig.Verifier is required")
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := TokenFromRequest(r, cfg.CookieName)
			if token == "" {
				unauthorized(w, "missing credentials")
				return
			}

			peerID, err := cfg.Verifier.Verify(r.Context(), token)
			switch {
			case err == nil:
				next.ServeHTTP(w, r.WithContext(WithPeerID(r.Context(), peerID)))
			case errors.Is(err, ErrInvalidToken):
				unauthorized(w, "invalid or expired session")
			default:
				log.ErrorContext(r.Context(), "session verification failed",
					"error", err, "request_id", RequestIDFromContext(r.Context()))
				writeError(w, http.StatusServiceUnavailable, "authentication temporarily unavailable")
			}
		})
	}
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, msg)
}

// TokenFromRequest reads "Authorization: Bearer <token>", falling back to the
// cookie only when no Authorization header is present at all.
func TokenFromRequest(r *http.Request, cookieName string) string {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, tok, ok := strings.Cut(h, " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			return strings.TrimSpace(tok)
		}
		return "" // a malformed header is a failure, not a reason to try the cookie
	}
	if cookieName != "" {
		if c, err := r.Cookie(cookieName); err == nil {
			return c.Value
		}
	}
	return ""
}
