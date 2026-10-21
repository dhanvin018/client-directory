package repository

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model" // adjust to your module path
)

// This is the only file that builds or reads model struct fields. If the
// models change, this is the only file to update.
//
// Optional IDs: ClonedFrom is a pointer, but InvitedBy, TargetID and
// TargetDocID are plain uuid.UUID in the models, so "none" is stored as NULL
// in the database and read back as the zero UUID (uuid.Nil).
//
// Member.DocRoles is keyed by the document ID as a string.

const (
	peerCols  = `peer_id, handle, user_name, auth_provider, auth_subject`
	roomCols  = `room_id, name, created_at, visibility, default_doc_role`
	docCols   = `doc_id, title, position_rank, added_by, added_at, cloned_from_doc_id`
	eventCols = `room_id, event_id, actor_id, target_id, target_doc_id, event_type, details, occurred_at`
)

func scanPeer(s rowScanner) (*model.Peer, error) {
	var p model.Peer
	if err := s.Scan(&p.PeerID, &p.Handle, &p.UserName, &p.AuthProvider, &p.AuthSubject); err != nil {
		return nil, translate(err)
	}
	return &p, nil
}

func scanRoom(s rowScanner) (*model.Room, error) {
	var (
		r           model.Room
		visibility  string
		defaultRole string
	)
	if err := s.Scan(&r.RoomID, &r.Name, &r.CreatedAt, &visibility, &defaultRole); err != nil {
		return nil, translate(err)
	}
	r.CreatedAt = r.CreatedAt.UTC()
	r.Visibility = model.Visibility(visibility)
	r.DefaultDocRole = model.DocRoles(defaultRole)
	return &r, nil
}

func scanDocument(s rowScanner) (*model.DocumentRef, error) {
	var (
		d      model.DocumentRef
		cloned uuid.NullUUID
	)
	if err := s.Scan(&d.DocID, &d.Title, &d.PositionRank, &d.AddedByPeer, &d.AddedAt, &cloned); err != nil {
		return nil, translate(err)
	}
	d.AddedAt = d.AddedAt.UTC()
	d.ClonedFrom = nullPtr(cloned)
	return &d, nil
}

func scanEvent(s rowScanner) (*model.RoomEvent, error) {
	var (
		e         model.RoomEvent
		eventID   int64
		target    uuid.NullUUID
		targetDoc uuid.NullUUID
		eventType string
		details   []byte
		at        time.Time
	)
	if err := s.Scan(&e.RoomID, &eventID, &e.ActorID, &target, &targetDoc, &eventType, &details, &at); err != nil {
		return nil, translate(err)
	}
	e.EventID = uint64(eventID)
	e.TargetID = target.UUID       // uuid.Nil when NULL
	e.TargetDocID = targetDoc.UUID // uuid.Nil when NULL
	e.EventType = model.RoomEventType(eventType)
	if len(details) > 0 {
		if err := json.Unmarshal(details, &e.Details); err != nil {
			return nil, err
		}
	}
	e.Timestamp = at.UTC()
	return &e, nil
}

func scanRoomSummary(s rowScanner) (*model.RoomSummary, error) {
	var (
		r          model.RoomSummary
		visibility string
	)
	if err := s.Scan(&r.RoomID, &r.Name, &visibility, &r.MemberCount, &r.IsMember); err != nil {
		return nil, translate(err)
	}
	r.Visibility = model.Visibility(visibility)
	return &r, nil
}

// inviteSelect reads invites with the room name and both peers resolved, so
// the inbox can show "<inviter> invited you to <room>" without more requests.
const inviteSelect = `
SELECT i.invite_id, i.code, i.room_id, r.name,
       i.inviter_id, pi.handle, pi.user_name,
       i.invitee_id, pe.handle, pe.user_name,
       i.room_role, i.status, i.created_at, i.expires_at, i.responded_at
FROM %s i
JOIN rooms r  ON r.room_id  = i.room_id
JOIN peers pi ON pi.peer_id = i.inviter_id
JOIN peers pe ON pe.peer_id = i.invitee_id`

func scanInvite(s rowScanner) (*model.RoomInvite, error) {
	var (
		inv       model.RoomInvite
		role      string
		status    string
		responded sql.NullTime
	)
	err := s.Scan(&inv.InviteID, &inv.Code, &inv.RoomID, &inv.RoomName,
		&inv.Inviter.PeerID, &inv.Inviter.Handle, &inv.Inviter.UserName,
		&inv.Invitee.PeerID, &inv.Invitee.Handle, &inv.Invitee.UserName,
		&role, &status, &inv.CreatedAt, &inv.ExpiresAt, &responded)
	if err != nil {
		return nil, translate(err)
	}
	inv.RoomRole = model.RoomRoles(role)
	inv.Status = model.InviteStatus(status)
	inv.CreatedAt = inv.CreatedAt.UTC()
	inv.ExpiresAt = inv.ExpiresAt.UTC()
	if responded.Valid {
		t := responded.Time.UTC()
		inv.RespondedAt = &t
	}
	return &inv, nil
}

func scanCandidate(s rowScanner) (*model.InviteCandidate, error) {
	var (
		c      model.InviteCandidate
		status string
	)
	if err := s.Scan(&c.PeerID, &c.Handle, &c.UserName, &status); err != nil {
		return nil, translate(err)
	}
	c.Status = model.CandidateStatus(status)
	return &c, nil
}

func scanBan(s rowScanner) (*model.RoomBan, error) {
	var b model.RoomBan
	if err := s.Scan(&b.Peer.PeerID, &b.Peer.Handle, &b.Peer.UserName, &b.BannedBy, &b.BannedAt); err != nil {
		return nil, translate(err)
	}
	b.BannedAt = b.BannedAt.UTC()
	return &b, nil
}

// likeEscape declares the escape character prefixPattern uses.
const likeEscape = `ESCAPE '\'`

// prefixPattern turns a search prefix into a LIKE pattern, escaping the
// wildcards so "a_b" matches literally. Queries use likeEscape.
func prefixPattern(prefix string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
}

// newMember assembles a Member from its row plus its per-document roles.
func newMember(peerID uuid.UUID, roomRole string, joinedAt time.Time, invitedBy uuid.NullUUID, docRoles map[string]model.DocRoles) model.Member {
	if docRoles == nil {
		docRoles = map[string]model.DocRoles{} // encodes as {} rather than null
	}
	return model.Member{
		PeerID:    peerID,
		RoomRole:  model.RoomRoles(roomRole),
		DocRoles:  docRoles,
		JoinedAt:  joinedAt.UTC(),
		InvitedBy: invitedBy.UUID, // uuid.Nil when NULL
	}
}

// ---- NULL helpers ----

func nullPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id := n.UUID
	return &id
}

func nullUUID(p *uuid.UUID) uuid.NullUUID {
	if p == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *p, Valid: true}
}

// nullText turns an optional string-like value into a nullable column value.
func nullText[T ~string](p *T) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*p), Valid: true}
}
