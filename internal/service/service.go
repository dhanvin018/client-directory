package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

// Services bundles every service. Handlers receive the one they need.
type Services struct {
	Peers     *PeerService
	Rooms     *RoomService
	Documents *DocumentService
	Members   *MemberService
	Events    *EventService
	Invites   *InviteService
}

// Option customizes New.
type Option func(*base)

// WithClock replaces the clock services use for timestamps and invite expiry.
// Tests use it to move time forward.
func WithClock(now func() time.Time) Option {
	return func(b *base) { b.now = now }
}

func New(store Store, opts ...Option) *Services {
	b := base{
		store:   store,
		now:     func() time.Time { return time.Now().UTC() },
		newID:   uuid.New,
		newCode: randomCode,
	}
	for _, opt := range opts {
		opt(&b)
	}
	return &Services{
		Peers:     &PeerService{b},
		Rooms:     &RoomService{b},
		Documents: &DocumentService{b},
		Members:   &MemberService{b},
		Events:    &EventService{b},
		Invites:   &InviteService{b},
	}
}

// base holds what every service needs. now, newID and newCode are fields so
// tests can make time and IDs deterministic (see WithClock).
type base struct {
	store   Store
	now     func() time.Time
	newID   func() uuid.UUID
	newCode func() string // invite codes
}

// randomCode returns 128 random bits as 22 URL-safe characters.
func randomCode() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("service: crypto/rand failed: " + err.Error()) // never happens on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// event records an audit event through r, which must be the transaction's
// repositories so the event commits or rolls back with the change it describes.
func (b base) event(
	ctx context.Context, r Repos,
	roomID, actor uuid.UUID,
	t model.RoomEventType,
	target, targetDoc *uuid.UUID,
	details map[string]any,
) error {
	return r.Events().Append(ctx, NewEvent{
		RoomID:      roomID,
		ActorID:     actor,
		TargetID:    target,
		TargetDocID: targetDoc,
		Type:        t,
		Details:     details,
		At:          b.now(),
	})
}
