package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

const (
	inviteTTL         = 7 * 24 * time.Hour
	maxInvitesPerHour = 30
)

// InviteService is how peers join PRIVATE rooms. Any member may invite a
// peer by ID (found through Candidates); the invite is bound to that peer, and
// its code only works for them. Public rooms have no invites: people join
// them directly (MemberService.Join).
//
// Every write takes the room lock first, like the other services, so accept,
// decline and revoke cannot race each other.
type InviteService struct{ base }

func errRoomIsPublic() error {
	return conflict("room is public: anyone can join it, so share the room link instead of an invite")
}

// effective reports the invite's status as of now: PENDING past its expiry
// is EXPIRED, whether or not the row has been marked yet.
func (s *InviteService) effective(inv *model.RoomInvite) *model.RoomInvite {
	if inv.Status == model.InvitePending && !s.now().Before(inv.ExpiresAt) {
		inv.Status = model.InviteExpired
	}
	return inv
}

func (s *InviteService) livePending(invites []model.RoomInvite) []model.RoomInvite {
	out := invites[:0]
	for i := range invites {
		if s.effective(&invites[i]).Status == model.InvitePending {
			out = append(out, invites[i])
		}
	}
	return out
}

// Candidates searches peers by handle or display-name prefix so a member can
// pick whom to invite. Each result says whether they can be invited.
func (s *InviteService) Candidates(ctx context.Context, actor, roomID uuid.UUID, query string) ([]model.InviteCandidate, error) {
	q, err := cleanSearch(query, maxUserNameLen)
	if err != nil {
		return nil, err
	}
	if _, err := requireMember(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	room, err := s.store.Rooms().Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if room.Visibility == model.VisibilityPublic {
		return nil, errRoomIsPublic()
	}
	return s.store.Invites().Candidates(ctx, roomID, q, s.now(), searchLimit)
}

// Create invites inviteeID to a private room. Any member may invite.
// Inviting someone who already has a live invite returns that invite
// (created=false) instead of making a second one. Records INVITE_CREATED.
func (s *InviteService) Create(ctx context.Context, actor, roomID, inviteeID uuid.UUID) (invite *model.RoomInvite, created bool, err error) {
	if inviteeID == actor {
		return nil, false, invalid("you cannot invite yourself")
	}
	err = s.store.WithTx(ctx, func(r Repos) error {
		room, err := r.Rooms().LockForUpdate(ctx, roomID)
		if err != nil {
			return err
		}
		if _, err := requireMember(ctx, r, roomID, actor); err != nil {
			return err
		}
		if room.Visibility == model.VisibilityPublic {
			return errRoomIsPublic()
		}
		if _, err := r.Peers().Get(ctx, inviteeID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return invalid("peer %s does not exist", inviteeID)
			}
			return err
		}
		if _, err := r.Members().GetRoomRole(ctx, roomID, inviteeID); err == nil {
			return conflict("peer is already a member of this room")
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		banned, err := r.Bans().IsBanned(ctx, roomID, inviteeID)
		if err != nil {
			return err
		}
		if banned {
			return conflict("peer is banned from this room; an owner must lift the ban first")
		}

		pending, err := r.Invites().GetPending(ctx, roomID, inviteeID)
		switch {
		case err == nil && s.effective(pending).Status == model.InvitePending:
			invite = pending
			return nil
		case err == nil: // past its expiry: mark it so a new one can be made
			if err := r.Invites().Resolve(ctx, pending.InviteID, model.InviteExpired, s.now()); err != nil {
				return err
			}
		case !errors.Is(err, ErrNotFound):
			return err
		}

		now := s.now()
		sent, err := r.Invites().CountSentSince(ctx, actor, now.Add(-time.Hour))
		if err != nil {
			return err
		}
		if sent >= maxInvitesPerHour {
			return fmt.Errorf("%w: at most %d invites per hour", ErrRateLimited, maxInvitesPerHour)
		}

		invite, err = r.Invites().Create(ctx, NewInvite{
			InviteID:  s.newID(),
			Code:      s.newCode(),
			RoomID:    roomID,
			InviterID: actor,
			InviteeID: inviteeID,
			RoomRole:  model.RoleParticipant,
			CreatedAt: now,
			ExpiresAt: now.Add(inviteTTL),
		})
		if err != nil {
			return err
		}
		created = true
		return s.event(ctx, r, roomID, actor, model.EventInviteCreated, &inviteeID, nil, map[string]any{
			"inviteId": invite.InviteID.String(),
		})
	})
	if err != nil {
		return nil, false, err
	}
	return invite, created, nil
}

// ListForRoom returns the room's live pending invites. Any member may look.
func (s *InviteService) ListForRoom(ctx context.Context, actor, roomID uuid.UUID) ([]model.RoomInvite, error) {
	if _, err := requireMember(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	invites, err := s.store.Invites().ListPendingForRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	return s.livePending(invites), nil
}

// Revoke cancels a pending invite. Its inviter or a room owner may revoke.
// Records INVITE_REVOKED.
func (s *InviteService) Revoke(ctx context.Context, actor, roomID, inviteID uuid.UUID) error {
	return s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		role, err := requireMember(ctx, r, roomID, actor)
		if err != nil {
			return err
		}
		inv, err := r.Invites().GetByID(ctx, roomID, inviteID)
		if err != nil {
			return err
		}
		if role != model.RoleOwner && inv.Inviter.PeerID != actor {
			return forbidden("only the inviter or a room owner can revoke an invite")
		}
		if st := s.effective(inv).Status; st != model.InvitePending {
			return conflict("invite is already %s", strings.ToLower(string(st)))
		}
		if err := r.Invites().Resolve(ctx, inviteID, model.InviteRevoked, s.now()); err != nil {
			return err
		}
		target := inv.Invitee.PeerID
		return s.event(ctx, r, roomID, actor, model.EventInviteRevoked, &target, nil, map[string]any{
			"inviteId": inviteID.String(),
			"reason":   "revoked",
		})
	})
}

// Inbox returns the caller's live pending invites, newest first.
func (s *InviteService) Inbox(ctx context.Context, actor uuid.UUID) ([]model.RoomInvite, error) {
	invites, err := s.store.Invites().ListPendingForInvitee(ctx, actor)
	if err != nil {
		return nil, err
	}
	return s.livePending(invites), nil
}

// Get previews an invite by its code. Only the invitee can see it; anyone
// else gets ErrNotFound, so a forwarded link reveals nothing.
func (s *InviteService) Get(ctx context.Context, actor uuid.UUID, code string) (*model.RoomInvite, error) {
	inv, err := s.store.Invites().GetByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if inv.Invitee.PeerID != actor {
		return nil, ErrNotFound
	}
	return s.effective(inv), nil
}

// Accept joins the invitee to the room as a PARTICIPANT with the inviter as
// InvitedBy. Records ADD_MEMBER with details.via = "invite".
func (s *InviteService) Accept(ctx context.Context, actor uuid.UUID, code string) (*model.Room, *model.Member, error) {
	inv, err := s.Get(ctx, actor, code)
	if err != nil {
		return nil, nil, err
	}
	roomID := inv.RoomID

	err = s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		inv, err := r.Invites().GetByCode(ctx, code) // re-read under the lock
		if err != nil {
			return err
		}
		if st := s.effective(inv).Status; st != model.InvitePending {
			return conflict("invite is %s", strings.ToLower(string(st)))
		}
		inviter := inv.Inviter.PeerID
		err = s.admit(ctx, r, roomID, actor, &inviter, map[string]any{
			"via":      "invite",
			"inviteId": inv.InviteID.String(),
		})
		if err != nil {
			return err
		}
		return r.Invites().Resolve(ctx, inv.InviteID, model.InviteAccepted, s.now())
	})
	if err != nil {
		return nil, nil, err
	}

	room, err := s.store.Rooms().Get(ctx, roomID)
	if err != nil {
		return nil, nil, err
	}
	member, err := s.store.Members().Get(ctx, roomID, actor)
	if err != nil {
		return nil, nil, err
	}
	return room, member, nil
}

// Decline turns an invite down. Only the invitee can. Records INVITE_DECLINED.
func (s *InviteService) Decline(ctx context.Context, actor uuid.UUID, code string) error {
	inv, err := s.Get(ctx, actor, code)
	if err != nil {
		return err
	}
	roomID := inv.RoomID

	return s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		inv, err := r.Invites().GetByCode(ctx, code)
		if err != nil {
			return err
		}
		if st := s.effective(inv).Status; st != model.InvitePending {
			return conflict("invite is %s", strings.ToLower(string(st)))
		}
		if err := r.Invites().Resolve(ctx, inv.InviteID, model.InviteDeclined, s.now()); err != nil {
			return err
		}
		return s.event(ctx, r, roomID, actor, model.EventInviteDeclined, &actor, nil, map[string]any{
			"inviteId": inv.InviteID.String(),
		})
	})
}
