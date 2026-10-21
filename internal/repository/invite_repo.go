package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

type inviteRepo struct{ q dbtx }

var (
	invitesFrom = fmt.Sprintf(inviteSelect, "room_invites")
	// invitesFromCTE reads rows produced by a CTE named "changed" (an INSERT
	// or UPDATE ... RETURNING), so writes return the joined shape in one statement.
	invitesFromCTE = fmt.Sprintf(inviteSelect, "changed")
)

// Create returns ErrConflict if the room already has a PENDING invite for
// the invitee (uq_room_invites_pending) or, vanishingly unlikely, the code
// is taken.
func (r inviteRepo) Create(ctx context.Context, in service.NewInvite) (*model.RoomInvite, error) {
	return scanInvite(r.q.QueryRowContext(ctx,
		`WITH changed AS (
		     INSERT INTO room_invites
		         (invite_id, code, room_id, inviter_id, invitee_id, room_role, status, created_at, expires_at)
		     VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', $7, $8)
		     RETURNING *
		 )`+invitesFromCTE,
		in.InviteID, in.Code, in.RoomID, in.InviterID, in.InviteeID, string(in.RoomRole), in.CreatedAt, in.ExpiresAt))
}

func (r inviteRepo) GetByID(ctx context.Context, roomID, inviteID uuid.UUID) (*model.RoomInvite, error) {
	return scanInvite(r.q.QueryRowContext(ctx,
		invitesFrom+` WHERE i.room_id = $1 AND i.invite_id = $2`, roomID, inviteID))
}

func (r inviteRepo) GetByCode(ctx context.Context, code string) (*model.RoomInvite, error) {
	return scanInvite(r.q.QueryRowContext(ctx,
		invitesFrom+` WHERE i.code = $1`, code))
}

func (r inviteRepo) GetPending(ctx context.Context, roomID, inviteeID uuid.UUID) (*model.RoomInvite, error) {
	return scanInvite(r.q.QueryRowContext(ctx,
		invitesFrom+` WHERE i.room_id = $1 AND i.invitee_id = $2 AND i.status = 'PENDING'`,
		roomID, inviteeID))
}

func (r inviteRepo) ListPendingForRoom(ctx context.Context, roomID uuid.UUID) ([]model.RoomInvite, error) {
	rows, err := r.q.QueryContext(ctx,
		invitesFrom+` WHERE i.room_id = $1 AND i.status = 'PENDING'
		 ORDER BY i.created_at DESC, i.invite_id`, roomID)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanInvite)
}

func (r inviteRepo) ListPendingForInvitee(ctx context.Context, inviteeID uuid.UUID) ([]model.RoomInvite, error) {
	rows, err := r.q.QueryContext(ctx,
		invitesFrom+` WHERE i.invitee_id = $1 AND i.status = 'PENDING'
		 ORDER BY i.created_at DESC, i.invite_id`, inviteeID)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanInvite)
}

// Resolve only touches a PENDING row, so a second resolver gets ErrNotFound.
func (r inviteRepo) Resolve(ctx context.Context, inviteID uuid.UUID, status model.InviteStatus, at time.Time) error {
	return affectedOne(r.q.ExecContext(ctx,
		`UPDATE room_invites SET status = $2, responded_at = $3
		 WHERE invite_id = $1 AND status = 'PENDING'`,
		inviteID, string(status), at))
}

func (r inviteRepo) RevokePending(ctx context.Context, roomID uuid.UUID, inviteeID *uuid.UUID, at time.Time) ([]model.RoomInvite, error) {
	rows, err := r.q.QueryContext(ctx,
		`WITH changed AS (
		     UPDATE room_invites SET status = 'REVOKED', responded_at = $3
		     WHERE room_id = $1 AND status = 'PENDING'
		       AND ($2::uuid IS NULL OR invitee_id = $2::uuid)
		     RETURNING *
		 )`+invitesFromCTE+`
		 ORDER BY i.created_at, i.invite_id`,
		roomID, nullUUID(inviteeID), at)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanInvite)
}

func (r inviteRepo) CountSentSince(ctx context.Context, inviterID uuid.UUID, since time.Time) (int, error) {
	var n int
	err := r.q.QueryRowContext(ctx,
		`SELECT count(*) FROM room_invites WHERE inviter_id = $1 AND created_at >= $2`,
		inviterID, since).Scan(&n)
	return n, translate(err)
}

// Candidates matches handle or lower(user_name) by prefix (both indexed in
// 0006_peer_handles.sql). Handle matches sort first.
func (r inviteRepo) Candidates(ctx context.Context, roomID uuid.UUID, prefix string, now time.Time, limit int) ([]model.InviteCandidate, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT p.peer_id, p.handle, p.user_name,
		        CASE
		            WHEN EXISTS (SELECT 1 FROM members m
		                         WHERE m.room_id = $1 AND m.peer_id = p.peer_id) THEN 'MEMBER'
		            WHEN EXISTS (SELECT 1 FROM room_bans b
		                         WHERE b.room_id = $1 AND b.peer_id = p.peer_id) THEN 'BANNED'
		            WHEN EXISTS (SELECT 1 FROM room_invites i
		                         WHERE i.room_id = $1 AND i.invitee_id = p.peer_id
		                           AND i.status = 'PENDING' AND i.expires_at > $3) THEN 'INVITED'
		            ELSE 'AVAILABLE'
		        END
		 FROM peers p
		 WHERE p.handle LIKE $2 `+likeEscape+` OR lower(p.user_name) LIKE $2 `+likeEscape+`
		 ORDER BY (p.handle LIKE $2 `+likeEscape+`) DESC, p.handle
		 LIMIT $4`,
		roomID, prefixPattern(prefix), now, limit)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanCandidate)
}
