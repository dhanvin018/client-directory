package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

type roomRepo struct{ q dbtx }

func (r roomRepo) Create(ctx context.Context, n service.NewRoom) (*model.Room, error) {
	return scanRoom(r.q.QueryRowContext(ctx,
		`INSERT INTO rooms (room_id, name, created_at, visibility, default_doc_role)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+roomCols,
		n.RoomID, n.Name, n.CreatedAt, string(n.Visibility), string(n.DefaultDocRole)))
}

func (r roomRepo) Get(ctx context.Context, roomID uuid.UUID) (*model.Room, error) {
	return scanRoom(r.q.QueryRowContext(ctx,
		`SELECT `+roomCols+` FROM rooms WHERE room_id = $1`, roomID))
}

// LockForUpdate row-locks the room until the surrounding transaction ends.
// Called outside a transaction the lock is released immediately, so the
// service layer only uses it inside WithTx.
//
// FOR NO KEY UPDATE is enough because the room's key is never changed. It
// still conflicts with another writer's lock and with DELETE, but unlike
// FOR UPDATE it does not block the foreign-key checks other transactions run
// when they reference the room.
func (r roomRepo) LockForUpdate(ctx context.Context, roomID uuid.UUID) (*model.Room, error) {
	return scanRoom(r.q.QueryRowContext(ctx,
		`SELECT `+roomCols+` FROM rooms WHERE room_id = $1 FOR NO KEY UPDATE`, roomID))
}

// ListForPeer returns the rooms a peer belongs to, newest first.
func (r roomRepo) ListForPeer(ctx context.Context, peerID uuid.UUID) ([]model.Room, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT r.room_id, r.name, r.created_at, r.visibility, r.default_doc_role
		 FROM rooms r
		 JOIN members m ON m.room_id = r.room_id
		 WHERE m.peer_id = $1
		 ORDER BY r.created_at DESC, r.room_id`, peerID)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanRoom)
}

// Update changes only the non-nil fields of the patch.
func (r roomRepo) Update(ctx context.Context, roomID uuid.UUID, p service.RoomPatch) (*model.Room, error) {
	return scanRoom(r.q.QueryRowContext(ctx,
		`UPDATE rooms
		 SET name             = COALESCE($2::text, name),
		     visibility       = COALESCE($3::text, visibility),
		     default_doc_role = COALESCE($4::text, default_doc_role)
		 WHERE room_id = $1
		 RETURNING `+roomCols,
		roomID, nullText(p.Name), nullText(p.Visibility), nullText(p.DefaultDocRole)))
}

// roomSummarySelect computes the member count and whether $2 is a member.
const roomSummarySelect = `
SELECT r.room_id, r.name, r.visibility,
       (SELECT count(*) FROM members m WHERE m.room_id = r.room_id),
       EXISTS (SELECT 1 FROM members m WHERE m.room_id = r.room_id AND m.peer_id = $2)
FROM rooms r`

// SearchPublic matches lower(name) by prefix; the partial index
// idx_rooms_public_name_prefix serves it.
func (r roomRepo) SearchPublic(ctx context.Context, prefix string, peerID uuid.UUID, limit int) ([]model.RoomSummary, error) {
	rows, err := r.q.QueryContext(ctx,
		roomSummarySelect+`
		 WHERE r.visibility = 'PUBLIC' AND lower(r.name) LIKE $1 `+likeEscape+`
		 ORDER BY lower(r.name), r.room_id
		 LIMIT $3`,
		prefixPattern(prefix), peerID, limit)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanRoomSummary)
}

func (r roomRepo) Summary(ctx context.Context, roomID, peerID uuid.UUID) (*model.RoomSummary, error) {
	return scanRoomSummary(r.q.QueryRowContext(ctx,
		roomSummarySelect+` WHERE r.room_id = $1`, roomID, peerID))
}

// Delete removes the room; documents, members, doc roles, events, invites
// and bans go with it through ON DELETE CASCADE.
func (r roomRepo) Delete(ctx context.Context, roomID uuid.UUID) error {
	return affectedOne(r.q.ExecContext(ctx,
		`DELETE FROM rooms WHERE room_id = $1`, roomID))
}
