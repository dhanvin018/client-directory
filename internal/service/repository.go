package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

// Contract for every repository implementation:
//   - A missing row returns ErrNotFound; a unique/primary-key violation
//     returns ErrConflict. Wrap the driver error so errors.Is works.
//   - Inside WithTx, every call uses the same transaction.
//
// Repositories take IDs and small input structs and return model types, so
// the service layer never builds model.Member / model.DocumentRef itself and
// stays independent of how the models embed peers.

// Repos is a set of repositories bound to one connection or transaction.
type Repos interface {
	Peers() PeerRepository
	Rooms() RoomRepository
	Documents() DocumentRepository
	Members() MemberRepository
	Events() EventRepository
	Invites() InviteRepository
	Bans() BanRepository
	DesktopLogins() DesktopLoginRepository
	Sessions() SessionRepository
}

// Store is Repos plus transaction support. Calls on the Store itself run
// outside a transaction (fine for single reads).
type Store interface {
	Repos

	// WithTx runs fn with repositories bound to one transaction. It commits
	// if fn returns nil and rolls back if fn returns an error or panics.
	WithTx(ctx context.Context, fn func(Repos) error) error
}

// ---- Inputs ----

type NewPeer struct {
	PeerID       uuid.UUID
	Handle       string // already normalized and validated
	UserName     string
	AuthProvider string
	AuthSubject  string
}

type NewRoom struct {
	RoomID         uuid.UUID
	Name           string
	CreatedAt      time.Time
	Visibility     model.Visibility
	DefaultDocRole model.DocRoles
}

// RoomPatch updates only the non-nil fields.
type RoomPatch struct {
	Name           *string
	Visibility     *model.Visibility
	DefaultDocRole *model.DocRoles
}

type NewDocument struct {
	DocID        uuid.UUID
	RoomID       uuid.UUID
	Title        string
	PositionRank string
	AddedBy      uuid.UUID
	AddedAt      time.Time
	ClonedFrom   *uuid.UUID // nil = created fresh
}

// DocumentPatch updates only the non-nil fields.
type DocumentPatch struct {
	Title        *string
	PositionRank *string
}

type NewMember struct {
	RoomID    uuid.UUID
	PeerID    uuid.UUID
	RoomRole  model.RoomRoles
	InvitedBy *uuid.UUID // who added them; nil for the room creator
	JoinedAt  time.Time
	DocRoles  map[uuid.UUID]model.DocRoles // optional initial per-document roles
}

type NewInvite struct {
	InviteID  uuid.UUID
	Code      string
	RoomID    uuid.UUID
	InviterID uuid.UUID
	InviteeID uuid.UUID
	RoomRole  model.RoomRoles
	CreatedAt time.Time
	ExpiresAt time.Time
}

type NewBan struct {
	RoomID   uuid.UUID
	PeerID   uuid.UUID
	BannedBy uuid.UUID
	BannedAt time.Time
}

type NewEvent struct {
	RoomID      uuid.UUID
	ActorID     uuid.UUID
	TargetID    *uuid.UUID
	TargetDocID *uuid.UUID
	Type        model.RoomEventType
	Details     map[string]any
	At          time.Time
}

// ---- Repositories ----

type PeerRepository interface {
	Get(ctx context.Context, id uuid.UUID) (*model.Peer, error)
	GetByAuth(ctx context.Context, provider, subject string) (*model.Peer, error)
	Create(ctx context.Context, p NewPeer) (*model.Peer, error) // ErrConflict on a duplicate auth identity or handle
	UpdateUserName(ctx context.Context, id uuid.UUID, userName string) (*model.Peer, error)
	UpdateHandle(ctx context.Context, id uuid.UUID, handle string) (*model.Peer, error) // ErrConflict if taken
}

type RoomRepository interface {
	Create(ctx context.Context, r NewRoom) (*model.Room, error)
	Get(ctx context.Context, roomID uuid.UUID) (*model.Room, error)

	// LockForUpdate loads the room and row-locks it (SELECT ... FOR UPDATE)
	// until the transaction ends. Every room-scoped write starts with it, which
	// serializes writes per room: the last-owner check cannot race, and event
	// IDs are assigned in order.
	LockForUpdate(ctx context.Context, roomID uuid.UUID) (*model.Room, error)

	ListForPeer(ctx context.Context, peerID uuid.UUID) ([]model.Room, error)
	Update(ctx context.Context, roomID uuid.UUID, p RoomPatch) (*model.Room, error)
	Delete(ctx context.Context, roomID uuid.UUID) error // cascades documents, members, events, invites and bans

	// SearchPublic returns PUBLIC rooms whose lowercased name starts with
	// prefix, ordered by name. IsMember is relative to peerID.
	SearchPublic(ctx context.Context, prefix string, peerID uuid.UUID, limit int) ([]model.RoomSummary, error)
	// Summary returns any room's summary (the service decides who may see it).
	Summary(ctx context.Context, roomID, peerID uuid.UUID) (*model.RoomSummary, error)
}

type DocumentRepository interface {
	Insert(ctx context.Context, d NewDocument) (*model.DocumentRef, error)
	Get(ctx context.Context, roomID, docID uuid.UUID) (*model.DocumentRef, error) // ErrNotFound if it is not in this room
	List(ctx context.Context, roomID uuid.UUID) ([]model.DocumentRef, error)      // ordered by position_rank
	Update(ctx context.Context, roomID, docID uuid.UUID, p DocumentPatch) (*model.DocumentRef, error)
	Delete(ctx context.Context, roomID, docID uuid.UUID) error
}

type MemberRepository interface {
	Add(ctx context.Context, m NewMember) error // ErrConflict if already a member; also writes initial doc roles
	Get(ctx context.Context, roomID, peerID uuid.UUID) (*model.Member, error)
	List(ctx context.Context, roomID uuid.UUID) ([]model.Member, error)

	GetRoomRole(ctx context.Context, roomID, peerID uuid.UUID) (model.RoomRoles, error) // ErrNotFound if not a member
	CountRoomOwners(ctx context.Context, roomID uuid.UUID) (int, error)
	SetRoomRole(ctx context.Context, roomID, peerID uuid.UUID, role model.RoomRoles) error
	Remove(ctx context.Context, roomID, peerID uuid.UUID) error // cascades the member's doc roles

	// GetDocRole reports found=false when the member has no explicit role on
	// the document (they then fall back to the room's default doc role).
	GetDocRole(ctx context.Context, roomID, docID, peerID uuid.UUID) (role model.DocRoles, found bool, err error)
	SetDocRole(ctx context.Context, roomID, docID, peerID uuid.UUID, role model.DocRoles) error // upsert
}

type EventRepository interface {
	// Append assigns the next per-room event ID by incrementing
	// rooms.last_event_id and inserts the event, in the caller's transaction.
	Append(ctx context.Context, e NewEvent) error
	List(ctx context.Context, roomID uuid.UUID, after uint64, limit int) ([]model.RoomEvent, error)
}

// InviteRepository stores invites as written; the service decides when a
// PENDING invite counts as expired.
type InviteRepository interface {
	Create(ctx context.Context, in NewInvite) (*model.RoomInvite, error) // ErrConflict if a PENDING invite exists for the room and invitee
	GetByID(ctx context.Context, roomID, inviteID uuid.UUID) (*model.RoomInvite, error)
	GetByCode(ctx context.Context, code string) (*model.RoomInvite, error)
	GetPending(ctx context.Context, roomID, inviteeID uuid.UUID) (*model.RoomInvite, error) // ErrNotFound if none
	ListPendingForRoom(ctx context.Context, roomID uuid.UUID) ([]model.RoomInvite, error)
	ListPendingForInvitee(ctx context.Context, inviteeID uuid.UUID) ([]model.RoomInvite, error)

	// Resolve moves a PENDING invite to status. ErrNotFound if it is not
	// pending any more (someone else resolved it first).
	Resolve(ctx context.Context, inviteID uuid.UUID, status model.InviteStatus, at time.Time) error
	// RevokePending revokes the room's pending invites (only inviteeID's when
	// it is non-nil) and returns them as they were before revoking.
	RevokePending(ctx context.Context, roomID uuid.UUID, inviteeID *uuid.UUID, at time.Time) ([]model.RoomInvite, error)

	// CountSentSince counts invites inviterID created at or after since.
	CountSentSince(ctx context.Context, inviterID uuid.UUID, since time.Time) (int, error)
	// Candidates finds peers whose handle or lowercased display name starts
	// with prefix, marked by their standing in the room. A pending invite past
	// its expiry at now does not count as INVITED.
	Candidates(ctx context.Context, roomID uuid.UUID, prefix string, now time.Time, limit int) ([]model.InviteCandidate, error)
}

type BanRepository interface {
	Add(ctx context.Context, b NewBan) error                    // ErrConflict if already banned
	Remove(ctx context.Context, roomID, peerID uuid.UUID) error // ErrNotFound if not banned
	IsBanned(ctx context.Context, roomID, peerID uuid.UUID) (bool, error)
	List(ctx context.Context, roomID uuid.UUID) ([]model.RoomBan, error)
}

// DesktopLogin is a desktop sign-in waiting for the user to finish at the
// OAuth provider, keyed by the state sent to the provider.
type DesktopLogin struct {
	State         string
	RedirectURI   string
	CodeChallenge string
	AppState      string
	ExpiresAt     time.Time
}

// DesktopAuthCode is a one-time code handed to a desktop app after sign-in.
// Only the SHA-256 of the code is stored.
type DesktopAuthCode struct {
	CodeHash      []byte
	PeerID        uuid.UUID
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

// DesktopLoginRepository stores both kinds of short-lived, single-use rows.
// The Take methods delete the row they return, so each row is used at most
// once even under concurrent requests.
type DesktopLoginRepository interface {
	SaveLogin(ctx context.Context, l DesktopLogin) error
	// TakeLogin returns and deletes the login; ErrNotFound if there is none
	// or it expired before now.
	TakeLogin(ctx context.Context, state string, now time.Time) (*DesktopLogin, error)
	SaveCode(ctx context.Context, c DesktopAuthCode) error
	// TakeCode returns and deletes the code; ErrNotFound if there is none or
	// it expired before now.
	TakeCode(ctx context.Context, codeHash []byte, now time.Time) (*DesktopAuthCode, error)
	// DeleteExpired purges rows of both kinds that expired before now.
	DeleteExpired(ctx context.Context, now time.Time) error
}

// Session is one signed-in device. Only hashes of refresh tokens are stored.
type Session struct {
	SessionID   uuid.UUID
	PeerID      uuid.UUID
	RefreshHash []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type SessionRepository interface {
	Create(ctx context.Context, s Session) error
	// Rotate replaces the refresh hash of the live session whose current hash
	// is oldHash, keeps oldHash as its previous hash, and moves its expiry.
	// ErrNotFound if no live session has oldHash as its current hash. Of two
	// concurrent rotations with the same hash, exactly one succeeds.
	Rotate(ctx context.Context, oldHash, newHash []byte, expiresAt, now time.Time) (*Session, error)
	// DeleteByHash deletes the session whose current or previous refresh hash
	// is hash; ErrNotFound if there is none.
	DeleteByHash(ctx context.Context, hash []byte) error
	Delete(ctx context.Context, sessionID uuid.UUID) error // ErrNotFound if gone
	DeleteForPeer(ctx context.Context, peerID uuid.UUID) error
	DeleteExpired(ctx context.Context, now time.Time) error
}
