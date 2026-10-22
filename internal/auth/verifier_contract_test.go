package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/middleware"
)

// The session manager is the TokenVerifier behind middleware.Authenticate, so
// every way a token can be bad must come back as 401, never as 503.
func TestBadTokensAreUnauthorized(t *testing.T) {
	const secret = "test-secret-key-min-32-bytes-long-change-me"
	ctx := context.Background()
	m := NewJWTSessionManager(secret, time.Minute)

	valid, _, err := m.Issue(ctx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	revoked, _, err := m.Issue(ctx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Revoke(ctx, revoked); err != nil {
		t.Fatal(err)
	}
	expired, _, err := NewJWTSessionManager(secret, -time.Minute).Issue(ctx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := NewJWTSessionManager("a-different-secret-also-32-bytes-long!", time.Minute).Issue(ctx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	noneAlg, err := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{PeerID: uuid.New()}).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}

	protected := middleware.Authenticate(middleware.AuthConfig{Verifier: m})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	tests := []struct {
		name  string
		token string
		want  int
	}{
		{"valid", valid, http.StatusOK},
		{"revoked", revoked, http.StatusUnauthorized},
		{"expired", expired, http.StatusUnauthorized},
		{"signed with another key", otherKey, http.StatusUnauthorized},
		{"unsigned (alg none)", noneAlg, http.StatusUnauthorized},
		{"garbage", "not.a.jwt", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.want != http.StatusOK {
				_, err := m.Verify(ctx, tt.token)
				if !errors.Is(err, middleware.ErrInvalidToken) {
					t.Errorf("Verify error %v does not wrap middleware.ErrInvalidToken", err)
				}
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+tt.token)
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}

	// The package's own sentinels still match, so existing callers keep working.
	if _, err := m.Verify(ctx, revoked); !errors.Is(err, ErrTokenRevoked) {
		t.Errorf("revoked token error %v is not ErrTokenRevoked", err)
	}
}
