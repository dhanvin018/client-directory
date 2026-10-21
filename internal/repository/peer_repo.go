package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

type peerRepo struct{ q dbtx }

func (r peerRepo) Get(ctx context.Context, id uuid.UUID) (*model.Peer, error) {
	return scanPeer(r.q.QueryRowContext(ctx,
		`SELECT `+peerCols+` FROM peers WHERE peer_id = $1`, id))
}

func (r peerRepo) GetByAuth(ctx context.Context, provider, subject string) (*model.Peer, error) {
	return scanPeer(r.q.QueryRowContext(ctx,
		`SELECT `+peerCols+` FROM peers WHERE auth_provider = $1 AND auth_subject = $2`,
		provider, subject))
}

// Create returns ErrConflict if the (provider, subject) identity already has
// a peer or the handle is taken.
func (r peerRepo) Create(ctx context.Context, p service.NewPeer) (*model.Peer, error) {
	return scanPeer(r.q.QueryRowContext(ctx,
		`INSERT INTO peers (peer_id, handle, user_name, auth_provider, auth_subject)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+peerCols,
		p.PeerID, p.Handle, p.UserName, p.AuthProvider, p.AuthSubject))
}

// UpdateHandle returns ErrConflict if another peer has the handle.
func (r peerRepo) UpdateHandle(ctx context.Context, id uuid.UUID, handle string) (*model.Peer, error) {
	return scanPeer(r.q.QueryRowContext(ctx,
		`UPDATE peers SET handle = $2 WHERE peer_id = $1 RETURNING `+peerCols,
		id, handle))
}

func (r peerRepo) UpdateUserName(ctx context.Context, id uuid.UUID, userName string) (*model.Peer, error) {
	return scanPeer(r.q.QueryRowContext(ctx,
		`UPDATE peers SET user_name = $2 WHERE peer_id = $1 RETURNING `+peerCols,
		id, userName))
}
