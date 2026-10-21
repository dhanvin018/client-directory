package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJWTSessionManager_Lifecycle(t *testing.T) {
	secret := "test-secret-key-min-32-bytes-long-change-me"
	manager := NewJWTSessionManager(secret, time.Minute)
	ctx := context.Background()
	peerID := uuid.New()

	// 1. Issue a token
	token, expiresAt, err := manager.Issue(ctx, peerID)
	if err != nil {
		t.Fatalf("failed to issue token: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
	if time.Now().After(expiresAt) {
		t.Error("expected expiration time in the future")
	}

	// 2. Verify the token successfully
	verifiedPeerID, err := manager.Verify(ctx, token)
	if err != nil {
		t.Fatalf("failed to verify valid token: %v", err)
	}
	if verifiedPeerID != peerID {
		t.Errorf("expected peer ID %v, got %v", peerID, verifiedPeerID)
	}

	// 3. Revoke the token
	err = manager.Revoke(ctx, token)
	if err != nil {
		t.Fatalf("failed to revoke token: %v", err)
	}

	// 4. Verify the revoked token fails
	_, err = manager.Verify(ctx, token)
	if !errors.Is(err, ErrTokenRevoked) {
		t.Errorf("expected ErrTokenRevoked, got %v", err)
	}
}

func TestJWTSessionManager_InvalidToken(t *testing.T) {
	secret := "test-secret-key-min-32-bytes-long-change-me"
	manager := NewJWTSessionManager(secret, time.Minute)
	ctx := context.Background()

	_, err := manager.Verify(ctx, "invalid.jwt.token.string")
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}
