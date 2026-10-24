package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testSecret = "test-secret-key-min-32-bytes-long-change-me"

func TestAccessTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewAccessTokens(testSecret, time.Minute)
	peerID, sessionID := uuid.New(), uuid.New()

	token, expiresAt, err := m.Issue(ctx, peerID, sessionID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if d := time.Until(expiresAt); d <= 0 || d > time.Minute {
		t.Errorf("expires in %v, want about a minute", d)
	}

	got, err := m.Verify(ctx, token)
	if err != nil || got != peerID {
		t.Fatalf("Verify = %v, %v; want %v", got, err, peerID)
	}
	sid, err := m.SessionID(ctx, token)
	if err != nil || sid != sessionID {
		t.Fatalf("SessionID = %v, %v; want %v", sid, err, sessionID)
	}
}

// After expiry the token no longer authenticates, but it still names its
// session, so the client can log out with it.
func TestExpiredTokenStillNamesItsSession(t *testing.T) {
	ctx := context.Background()
	m := NewAccessTokens(testSecret, time.Minute)
	sessionID := uuid.New()
	token, _, err := m.Issue(ctx, uuid.New(), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	m.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := m.Verify(ctx, token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Verify(expired) error = %v, want ErrInvalidToken", err)
	}
	if sid, err := m.SessionID(ctx, token); err != nil || sid != sessionID {
		t.Errorf("SessionID(expired) = %v, %v; want %v", sid, err, sessionID)
	}

	// The signature is still checked.
	other := NewAccessTokens("a-different-secret-also-32-bytes-long!", time.Minute)
	if _, err := other.SessionID(ctx, token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("SessionID with the wrong key: error = %v, want ErrInvalidToken", err)
	}
}

func TestInvalidToken(t *testing.T) {
	m := NewAccessTokens(testSecret, time.Minute)
	if _, err := m.Verify(context.Background(), "invalid.jwt.token.string"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}
