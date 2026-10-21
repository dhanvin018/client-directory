package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

// MemberService manages room membership and both kinds of role. Every write
// takes the room's row lock first, so the last-owner rule cannot race.
type MemberService struct{ base }

func (s *MemberService) List(ctx context.Context, actor, roomID uuid.UUID) ([]model.Member, error) {
	if _, err := requireMember(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	return s.store.Members().List(ctx, roomID)
}

func (s *MemberService) Get(ctx context.Context, actor, roomID, peerID uuid.UUID) (*model.Member, error) {
	if _, err := requireMember(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	return s.store.Members().Get(ctx, roomID, peerID)
}

// Join adds the caller to a PUBLIC room as a PARTICIPANT. Joining a room you
// are already in is a no-op (created=false). A private room is ErrNotFound,
// so its existence is not revealed; banned peers get ErrForbidden.
// Records ADD_MEMBER with details.via = "join".
func (s *MemberService) Join(ctx context.Context, actor, roomID uuid.UUID) (member *model.Member, created bool, err error) {
	err = s.store.WithTx(ctx, func(r Repos) error {
		room, err := r.Rooms().LockForUpdate(ctx, roomID)
		if err != nil {
			return err
		}
		if _, err := r.Members().GetRoomRole(ctx, roomID, actor); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if room.Visibility != model.VisibilityPublic {
			return ErrNotFound
		}
		if err := s.admit(ctx, r, roomID, actor, nil, map[string]any{"via": "join"}); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	member, err = s.store.Members().Get(ctx, roomID, actor)
	return member, created, err
}

// admit adds peerID to the room as a PARTICIPANT and records ADD_MEMBER with
// the joining peer as the actor. Banned peers are refused. The caller must
// hold the room lock.
func (b base) admit(ctx context.Context, r Repos, roomID, peerID uuid.UUID, invitedBy *uuid.UUID, details map[string]any) error {
	banned, err := r.Bans().IsBanned(ctx, roomID, peerID)
	if err != nil {
		return err
	}
	if banned {
		return forbidden("you are banned from this room")
	}
	err = r.Members().Add(ctx, NewMember{ // ErrConflict if already a member
		RoomID:    roomID,
		PeerID:    peerID,
		RoomRole:  model.RoleParticipant,
		InvitedBy: invitedBy,
		JoinedAt:  b.now(),
	})
	if err != nil {
		return err
	}
	details["roomRole"] = string(model.RoleParticipant)
	target := peerID
	return b.event(ctx, r, roomID, peerID, model.EventAddMember, &target, nil, details)
}

// Remove removes target from the room. Any member may remove themselves
// (leave); removing someone else needs OWNER. The last OWNER cannot go.
// A removed member of a public room can rejoin; use Ban to keep them out.
// Records REMOVE_MEMBER.
func (s *MemberService) Remove(ctx context.Context, actor, roomID, target uuid.UUID) error {
	return s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		actorRole, err := requireMember(ctx, r, roomID, actor)
		if err != nil {
			return err
		}

		targetRole := actorRole
		if target != actor {
			if actorRole != model.RoleOwner {
				return forbidden("room owner required to remove another member")
			}
			targetRole, err = r.Members().GetRoomRole(ctx, roomID, target) // ErrNotFound if not a member
			if err != nil {
				return err
			}
		}

		if targetRole == model.RoleOwner {
			if err := ensureAnotherOwner(ctx, r, roomID); err != nil {
				return err
			}
		}

		if err := r.Members().Remove(ctx, roomID, target); err != nil {
			return err
		}
		return s.event(ctx, r, roomID, actor, model.EventRemoveMember, &target, nil, map[string]any{
			"roomRole": string(targetRole),
		})
	})
}

// UpdateRoomRole is OWNER-only. Demoting the last OWNER is rejected. Setting
// the role a member already has is a no-op and writes no event.
// Records UPDATE_ROOM_ROLE.
func (s *MemberService) UpdateRoomRole(ctx context.Context, actor, roomID, target uuid.UUID, newRole model.RoomRoles) (*model.Member, error) {
	if !validRoomRole(newRole) {
		return nil, invalid("roomRole must be OWNER or PARTICIPANT")
	}

	err := s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		if err := requireOwner(ctx, r, roomID, actor); err != nil {
			return err
		}

		oldRole, err := r.Members().GetRoomRole(ctx, roomID, target) // ErrNotFound if not a member
		if err != nil {
			return err
		}
		if oldRole == newRole {
			return nil
		}
		if oldRole == model.RoleOwner {
			if err := ensureAnotherOwner(ctx, r, roomID); err != nil {
				return err
			}
		}

		if err := r.Members().SetRoomRole(ctx, roomID, target, newRole); err != nil {
			return err
		}
		return s.event(ctx, r, roomID, actor, model.EventUpdateRoomRole, &target, nil, map[string]any{
			"oldRole": string(oldRole),
			"newRole": string(newRole),
		})
	})
	if err != nil {
		return nil, err
	}
	return s.store.Members().Get(ctx, roomID, target)
}

// UpdateDocRole sets target's role on one document. It requires a document
// manager (DOC_OWNER of the doc, or room OWNER). Records UPDATE_DOC_ROLE; the
// old role in the details is the effective one, so a member with no explicit
// role shows the room's default.
func (s *MemberService) UpdateDocRole(ctx context.Context, actor, roomID, docID, target uuid.UUID, newRole model.DocRoles) (*model.Member, error) {
	if !validDocRole(newRole) {
		return nil, invalid("doc role must be DOC_OWNER, EDITOR or VIEWER")
	}

	err := s.store.WithTx(ctx, func(r Repos) error {
		room, err := r.Rooms().LockForUpdate(ctx, roomID)
		if err != nil {
			return err
		}
		if err := requireDocManager(ctx, r, roomID, docID, actor); err != nil {
			return err
		}
		if _, err := r.Documents().Get(ctx, roomID, docID); err != nil {
			return err
		}
		if _, err := r.Members().GetRoomRole(ctx, roomID, target); err != nil { // target must be a member
			return err
		}

		oldRole, found, err := r.Members().GetDocRole(ctx, roomID, docID, target)
		if err != nil {
			return err
		}
		if found && oldRole == newRole {
			return nil
		}
		effectiveOld := oldRole
		if !found {
			effectiveOld = room.DefaultDocRole
		}

		if err := r.Members().SetDocRole(ctx, roomID, docID, target, newRole); err != nil {
			return err
		}
		return s.event(ctx, r, roomID, actor, model.EventUpdateDocRole, &target, &docID, map[string]any{
			"oldRole": string(effectiveOld),
			"newRole": string(newRole),
		})
	})
	if err != nil {
		return nil, err
	}
	return s.store.Members().Get(ctx, roomID, target)
}

// ---- Bans ----

// Ban is OWNER-only. It removes target from the room if they are a member,
// revokes their pending invites and keeps them from joining or accepting an
// invite until Unban. Owners must be demoted first. Records BAN_MEMBER.
func (s *MemberService) Ban(ctx context.Context, actor, roomID, target uuid.UUID) error {
	if target == actor {
		return invalid("you cannot ban yourself")
	}
	return s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		if err := requireOwner(ctx, r, roomID, actor); err != nil {
			return err
		}
		if _, err := r.Peers().Get(ctx, target); err != nil {
			if errors.Is(err, ErrNotFound) {
				return invalid("peer %s does not exist", target)
			}
			return err
		}
		banned, err := r.Bans().IsBanned(ctx, roomID, target)
		if err != nil {
			return err
		}
		if banned {
			return conflict("peer is already banned from this room")
		}

		role, err := r.Members().GetRoomRole(ctx, roomID, target)
		wasMember := err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if wasMember && role == model.RoleOwner {
			return conflict("demote the owner before banning them")
		}
		if wasMember {
			if err := r.Members().Remove(ctx, roomID, target); err != nil {
				return err
			}
		}
		revoked, err := r.Invites().RevokePending(ctx, roomID, &target, s.now())
		if err != nil {
			return err
		}
		if err := r.Bans().Add(ctx, NewBan{RoomID: roomID, PeerID: target, BannedBy: actor, BannedAt: s.now()}); err != nil {
			return err
		}
		return s.event(ctx, r, roomID, actor, model.EventBanMember, &target, nil, map[string]any{
			"wasMember":      wasMember,
			"revokedInvites": len(revoked),
		})
	})
}

// Unban is OWNER-only. ErrNotFound if the peer is not banned. The peer is not
// re-added; they can join or be invited again. Records UNBAN_MEMBER.
func (s *MemberService) Unban(ctx context.Context, actor, roomID, target uuid.UUID) error {
	return s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		if err := requireOwner(ctx, r, roomID, actor); err != nil {
			return err
		}
		if err := r.Bans().Remove(ctx, roomID, target); err != nil {
			return err
		}
		return s.event(ctx, r, roomID, actor, model.EventUnbanMember, &target, nil, nil)
	})
}

// ListBans is OWNER-only.
func (s *MemberService) ListBans(ctx context.Context, actor, roomID uuid.UUID) ([]model.RoomBan, error) {
	if err := requireOwner(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	return s.store.Bans().List(ctx, roomID)
}
