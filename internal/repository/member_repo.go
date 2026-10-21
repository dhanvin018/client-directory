package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

type memberRepo struct{ q dbtx }

// addMemberSQL inserts the member and its initial per-document roles in ONE
// statement, so the two can never be half-applied, even outside a
// transaction. $6 is a JSON object {"<docId>": "<role>", ...}; jsonb_each_text
// turns it into rows (and yields none for {}).
const addMemberSQL = `
WITH new_member AS (
    INSERT INTO members (room_id, peer_id, room_role, joined_at, invited_by)
    VALUES ($1, $2, $3, $4, $5)
    RETURNING room_id, peer_id
)
INSERT INTO member_doc_roles (room_id, peer_id, doc_id, doc_role)
SELECT m.room_id, m.peer_id, r.key::uuid, r.value
FROM new_member m, jsonb_each_text($6::jsonb) AS r`

// Add returns ErrConflict if the peer is already a member. A doc role that
// points at a document outside the room fails the composite foreign key and
// the whole statement (member included) is rejected.
func (r memberRepo) Add(ctx context.Context, m service.NewMember) error {
	roles := make(map[string]string, len(m.DocRoles))
	for docID, role := range m.DocRoles {
		roles[docID.String()] = string(role)
	}
	payload, err := json.Marshal(roles)
	if err != nil {
		return err
	}
	_, err = r.q.ExecContext(ctx, addMemberSQL,
		m.RoomID, m.PeerID, string(m.RoomRole), m.JoinedAt, nullUUID(m.InvitedBy), string(payload))
	return translate(err)
}

// membersSQL reads members together with their per-document roles in a single
// statement, so a member and its roles always come from one snapshot. $2 is an
// optional peer filter.
const membersSQL = `
SELECT m.peer_id, m.room_role, m.joined_at, m.invited_by, d.doc_id, d.doc_role
FROM members m
LEFT JOIN member_doc_roles d
       ON d.room_id = m.room_id AND d.peer_id = m.peer_id
WHERE m.room_id = $1
  AND ($2::uuid IS NULL OR m.peer_id = $2::uuid)
ORDER BY m.joined_at, m.peer_id, d.doc_id`

type memberAcc struct {
	peerID   uuid.UUID
	role     string
	joinedAt time.Time
	invited  uuid.NullUUID
	docRoles map[string]model.DocRoles
}

func loadMembers(ctx context.Context, q dbtx, roomID uuid.UUID, peerID *uuid.UUID) ([]model.Member, error) {
	rows, err := q.QueryContext(ctx, membersSQL, roomID, nullUUID(peerID))
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var order []*memberAcc
	index := map[uuid.UUID]*memberAcc{}
	for rows.Next() {
		var (
			id       uuid.UUID
			role     string
			joinedAt time.Time
			invited  uuid.NullUUID
			docID    uuid.NullUUID
			docRole  sql.NullString
		)
		if err := rows.Scan(&id, &role, &joinedAt, &invited, &docID, &docRole); err != nil {
			return nil, translate(err)
		}
		acc, ok := index[id]
		if !ok {
			acc = &memberAcc{
				peerID: id, role: role, joinedAt: joinedAt, invited: invited,
				docRoles: map[string]model.DocRoles{},
			}
			index[id] = acc
			order = append(order, acc)
		}
		if docID.Valid {
			acc.docRoles[docID.UUID.String()] = model.DocRoles(docRole.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	out := make([]model.Member, 0, len(order))
	for _, a := range order {
		out = append(out, newMember(a.peerID, a.role, a.joinedAt, a.invited, a.docRoles))
	}
	return out, nil
}

func (r memberRepo) Get(ctx context.Context, roomID, peerID uuid.UUID) (*model.Member, error) {
	members, err := loadMembers(ctx, r.q, roomID, &peerID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, service.ErrNotFound
	}
	return &members[0], nil
}

func (r memberRepo) List(ctx context.Context, roomID uuid.UUID) ([]model.Member, error) {
	return loadMembers(ctx, r.q, roomID, nil)
}

// GetRoomRole returns ErrNotFound if the peer is not a member of the room.
func (r memberRepo) GetRoomRole(ctx context.Context, roomID, peerID uuid.UUID) (model.RoomRoles, error) {
	var role string
	err := r.q.QueryRowContext(ctx,
		`SELECT room_role FROM members WHERE room_id = $1 AND peer_id = $2`,
		roomID, peerID).Scan(&role)
	if err != nil {
		return "", translate(err)
	}
	return model.RoomRoles(role), nil
}

func (r memberRepo) CountRoomOwners(ctx context.Context, roomID uuid.UUID) (int, error) {
	var n int
	err := r.q.QueryRowContext(ctx,
		`SELECT count(*) FROM members WHERE room_id = $1 AND room_role = $2`,
		roomID, string(model.RoleOwner)).Scan(&n)
	if err != nil {
		return 0, translate(err)
	}
	return n, nil
}

func (r memberRepo) SetRoomRole(ctx context.Context, roomID, peerID uuid.UUID, role model.RoomRoles) error {
	return affectedOne(r.q.ExecContext(ctx,
		`UPDATE members SET room_role = $3 WHERE room_id = $1 AND peer_id = $2`,
		roomID, peerID, string(role)))
}

// Remove deletes the member; their per-document roles go with it through
// ON DELETE CASCADE.
func (r memberRepo) Remove(ctx context.Context, roomID, peerID uuid.UUID) error {
	return affectedOne(r.q.ExecContext(ctx,
		`DELETE FROM members WHERE room_id = $1 AND peer_id = $2`,
		roomID, peerID))
}

// GetDocRole reports found=false when the member has no explicit role on the
// document (they then fall back to the room's default doc role).
func (r memberRepo) GetDocRole(ctx context.Context, roomID, docID, peerID uuid.UUID) (model.DocRoles, bool, error) {
	var role string
	err := r.q.QueryRowContext(ctx,
		`SELECT doc_role FROM member_doc_roles WHERE room_id = $1 AND doc_id = $2 AND peer_id = $3`,
		roomID, docID, peerID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, translate(err)
	}
	return model.DocRoles(role), true, nil
}

// SetDocRole inserts or replaces the member's role on one document. The
// composite foreign keys reject a member or document from another room.
func (r memberRepo) SetDocRole(ctx context.Context, roomID, docID, peerID uuid.UUID, role model.DocRoles) error {
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO member_doc_roles (room_id, peer_id, doc_id, doc_role)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (doc_id, peer_id) DO UPDATE SET doc_role = EXCLUDED.doc_role`,
		roomID, peerID, docID, string(role))
	return translate(err)
}
