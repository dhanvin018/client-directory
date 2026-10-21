package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

type banRepo struct{ q dbtx }

// Add returns ErrConflict if the peer is already banned from the room.
func (r banRepo) Add(ctx context.Context, b service.NewBan) error {
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO room_bans (room_id, peer_id, banned_by, banned_at) VALUES ($1, $2, $3, $4)`,
		b.RoomID, b.PeerID, b.BannedBy, b.BannedAt)
	return translate(err)
}

func (r banRepo) Remove(ctx context.Context, roomID, peerID uuid.UUID) error {
	return affectedOne(r.q.ExecContext(ctx,
		`DELETE FROM room_bans WHERE room_id = $1 AND peer_id = $2`, roomID, peerID))
}

func (r banRepo) IsBanned(ctx context.Context, roomID, peerID uuid.UUID) (bool, error) {
	var banned bool
	err := r.q.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM room_bans WHERE room_id = $1 AND peer_id = $2)`,
		roomID, peerID).Scan(&banned)
	return banned, translate(err)
}

// List returns the room's bans, most recent first.
func (r banRepo) List(ctx context.Context, roomID uuid.UUID) ([]model.RoomBan, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT p.peer_id, p.handle, p.user_name, b.banned_by, b.banned_at
		 FROM room_bans b
		 JOIN peers p ON p.peer_id = b.peer_id
		 WHERE b.room_id = $1
		 ORDER BY b.banned_at DESC, p.peer_id`, roomID)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanBan)
}
