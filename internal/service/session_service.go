package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SessionService keeps sessions in the database: one per signed-in device.
//
// Access tokens are short-lived JWTs that name their session and are checked
// without a database lookup (see package auth). A session's refresh token
// gets new access tokens. Ending a session deletes its row, so after logout
// the refresh token is dead everywhere, restarts included, and the access
// tokens already handed out stop working within their short lifetime.
//
// Refresh tokens rotate: each refresh returns a new one and spends the old.
// Presenting an already spent token means two parties hold it, so the
// session is ended (refresh token reuse detection, RFC 9700 section 4.14).
type SessionService struct{ base }

const defaultSessionTTL = 30 * 24 * time.Hour

var errBadRefreshToken = fmt.Errorf("%w: invalid, expired or reused refresh token", ErrUnauthenticated)

// NewSession is a started or refreshed session with its new refresh token.
type NewSession struct {
	SessionID    uuid.UUID
	PeerID       uuid.UUID
	RefreshToken string
	ExpiresAt    time.Time // the refresh token's expiry
}

// Start creates a session for a peer who just signed in.
func (s *SessionService) Start(ctx context.Context, peerID uuid.UUID) (*NewSession, error) {
	token, err := randomURLToken(32)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if err := s.store.Sessions().DeleteExpired(ctx, now); err != nil {
		return nil, err
	}
	sess := Session{
		SessionID:   s.newID(),
		PeerID:      peerID,
		RefreshHash: hashCode(token),
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.sessionTTL),
	}
	if err := s.store.Sessions().Create(ctx, sess); err != nil {
		return nil, err
	}
	return &NewSession{SessionID: sess.SessionID, PeerID: peerID, RefreshToken: token, ExpiresAt: sess.ExpiresAt}, nil
}

// Refresh spends a refresh token and returns its replacement. Every failure
// is ErrUnauthenticated: the client has to sign in again.
func (s *SessionService) Refresh(ctx context.Context, refreshToken string) (*NewSession, error) {
	if refreshToken == "" {
		return nil, errBadRefreshToken
	}
	next, err := randomURLToken(32)
	if err != nil {
		return nil, err
	}
	now := s.now()
	old := hashCode(refreshToken)
	sess, err := s.store.Sessions().Rotate(ctx, old, hashCode(next), now.Add(s.sessionTTL), now)
	if errors.Is(err, ErrNotFound) {
		// Unknown, expired, or already spent. If it was spent, someone else
		// has (had) it too: end the session for everyone who holds it.
		if err := s.store.Sessions().DeleteByHash(ctx, old); err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, errBadRefreshToken
	}
	if err != nil {
		return nil, err
	}
	return &NewSession{SessionID: sess.SessionID, PeerID: sess.PeerID, RefreshToken: next, ExpiresAt: sess.ExpiresAt}, nil
}

// End ends the session a refresh token belongs to. An unknown token is not
// an error: logging out twice is fine.
func (s *SessionService) End(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return ignoreNotFound(s.store.Sessions().DeleteByHash(ctx, hashCode(refreshToken)))
}

// EndByID ends a session by its ID (taken from an access token).
func (s *SessionService) EndByID(ctx context.Context, sessionID uuid.UUID) error {
	return ignoreNotFound(s.store.Sessions().Delete(ctx, sessionID))
}

// EndAll ends every session of a peer ("sign out everywhere").
func (s *SessionService) EndAll(ctx context.Context, peerID uuid.UUID) error {
	return s.store.Sessions().DeleteForPeer(ctx, peerID)
}

func ignoreNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
