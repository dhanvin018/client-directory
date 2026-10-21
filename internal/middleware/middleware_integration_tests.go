package middleware

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRequestIDMiddleware(t *testing.T) {
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Case 1: Client provides Request ID
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "custom-request-id-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "custom-request-id-123" {
		t.Errorf("expected X-Request-ID 'custom-request-id-123', got '%s'", rec.Header().Get("X-Request-ID"))
	}

	// Case 2: No Request ID provided -> Auto-generates UUID
	reqNoID := httptest.NewRequest(http.MethodGet, "/", nil)
	recNoID := httptest.NewRecorder()

	handler.ServeHTTP(recNoID, reqNoID)

	generatedID := recNoID.Header().Get("X-Request-ID")
	if generatedID == "" {
		t.Error("expected generated X-Request-ID header")
	}
	if _, err := uuid.Parse(generatedID); err != nil {
		t.Errorf("expected generated X-Request-ID to be a valid UUID, got error: %v", err)
	}
}

func TestCORSMiddleware(t *testing.T) {
	corsMw := CORS(CORSConfig{
		AllowedOrigins:   []string{"http://localhost:3000"},
		AllowCredentials: true,
	})

	handler := corsMw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Preflight Request
	req := httptest.NewRequest(http.MethodOptions, "/rooms", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for preflight, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("expected Allow-Origin header to match origin")
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("expected Allow-Credentials header to be true")
	}
}

func TestRecoverMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	recoverMw := Recover(logger)

	handler := recoverMw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went terribly wrong")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 Internal Server Error, got %d", rec.Code)
	}
}

type mockVerifier struct {
	validToken string
	peerID     uuid.UUID
}

func (m *mockVerifier) Verify(ctx context.Context, token string) (uuid.UUID, error) {
	if token == m.validToken {
		return m.peerID, nil
	}
	return uuid.Nil, errors.New("invalid token")
}

func TestAuthenticateMiddleware(t *testing.T) {
	expectedPeerID := uuid.New()
	verifier := &mockVerifier{
		validToken: "valid-bearer-token",
		peerID:     expectedPeerID,
	}

	authMw := Authenticate(AuthConfig{
		Verifier: verifier,
	})

	var extractedPeerID uuid.UUID
	handler := authMw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extractedPeerID, _ = PeerIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	// 1. Invalid Token
	reqInvalid := httptest.NewRequest(http.MethodGet, "/peers/me", nil)
	reqInvalid.Header.Set("Authorization", "Bearer invalid-token")
	recInvalid := httptest.NewRecorder()

	handler.ServeHTTP(recInvalid, reqInvalid)

	if recInvalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 Unauthorized, got %d", recInvalid.Code)
	}

	// 2. Valid Token
	reqValid := httptest.NewRequest(http.MethodGet, "/peers/me", nil)
	reqValid.Header.Set("Authorization", "Bearer valid-bearer-token")
	recValid := httptest.NewRecorder()

	handler.ServeHTTP(recValid, reqValid)

	if recValid.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", recValid.Code)
	}
	if extractedPeerID != expectedPeerID {
		t.Errorf("expected extracted peer ID %v, got %v", expectedPeerID, extractedPeerID)
	}
}
