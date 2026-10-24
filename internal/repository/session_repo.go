package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/service"
)

type sessionRepo struct{ q dbtx }

func (r sessionRepo) Create(ctx context.Context, s service.Session) error {
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO sessions (session_id, peer_id, refresh_hash, created_at, refreshed_at, expires_at)
		 VALUES ($1, $2, $3, $4, $4, $5)`,
		s.SessionID, s.PeerID, s.RefreshHash, s.CreatedAt, s.ExpiresAt)
	return translate(err)
}

// Rotate is a single UPDATE: the row lock it takes makes a second rotation
// with the same old hash find nothing, so a refresh token is used once.
func (r sessionRepo) Rotate(ctx context.Context, oldHash, newHash []byte, expiresAt, now time.Time) (*service.Session, error) {
	var s service.Session
	err := r.q.QueryRowContext(ctx,
		`UPDATE sessions
		    SET previous_refresh_hash = refresh_hash, refresh_hash = $2, refreshed_at = $4, expires_at = $3
		  WHERE refresh_hash = $1 AND expires_at > $4
		 RETURNING session_id, peer_id, refresh_hash, created_at, expires_at`,
		oldHash, newHash, expiresAt, now).Scan(&s.SessionID, &s.PeerID, &s.RefreshHash, &s.CreatedAt, &s.ExpiresAt)
	if err != nil {
		return nil, translate(err)
	}
	s.CreatedAt, s.ExpiresAt = s.CreatedAt.UTC(), s.ExpiresAt.UTC()
	return &s, nil
}

func (r sessionRepo) DeleteByHash(ctx context.Context, hash []byte) error {
	return affectedOne(r.q.ExecContext(ctx,
		`DELETE FROM sessions WHERE refresh_hash = $1 OR previous_refresh_hash = $1`, hash))
}

func (r sessionRepo) Delete(ctx context.Context, sessionID uuid.UUID) error {
	return affectedOne(r.q.ExecContext(ctx, `DELETE FROM sessions WHERE session_id = $1`, sessionID))
}

func (r sessionRepo) DeleteForPeer(ctx context.Context, peerID uuid.UUID) error {
	_, err := r.q.ExecContext(ctx, `DELETE FROM sessions WHERE peer_id = $1`, peerID)
	return translate(err)
}

func (r sessionRepo) DeleteExpired(ctx context.Context, now time.Time) error {
	_, err := r.q.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, now)
	return translate(err)
}
