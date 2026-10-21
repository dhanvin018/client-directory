package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

type RoomService struct{ base }

type CreateRoomInput struct {
	Name           string
	Visibility     model.Visibility // empty defaults to PRIVATE
	DefaultDocRole model.DocRoles   // empty defaults to VIEWER; DOC_OWNER is rejected
}

// Create makes the room, adds the caller as its OWNER and records
// ROOM_CREATED, all in one transaction.
func (s *RoomService) Create(ctx context.Context, actor uuid.UUID, in CreateRoomInput) (*model.Room, error) {
	name, err := cleanText("name", in.Name, maxRoomNameLen)
	if err != nil {
		return nil, err
	}
	visibility := in.Visibility
	if visibility == "" {
		visibility = model.VisibilityPrivate
	}
	if !validVisibility(visibility) {
		return nil, invalid("visibility must be PUBLIC or PRIVATE")
	}
	defaultRole := in.DefaultDocRole
	if defaultRole == "" {
		defaultRole = model.DocRoleViewer
	}
	if !validDefaultDocRole(defaultRole) {
		return nil, invalid("defaultDocRole must be EDITOR or VIEWER")
	}

	roomID := s.newID()
	now := s.now()

	var room *model.Room
	err = s.store.WithTx(ctx, func(r Repos) error {
		created, err := r.Rooms().Create(ctx, NewRoom{
			RoomID:         roomID,
			Name:           name,
			CreatedAt:      now,
			Visibility:     visibility,
			DefaultDocRole: defaultRole,
		})
		if err != nil {
			return err
		}
		room = created

		err = r.Members().Add(ctx, NewMember{
			RoomID:   roomID,
			PeerID:   actor,
			RoomRole: model.RoleOwner,
			JoinedAt: now,
		})
		if err != nil {
			return err
		}

		return s.event(ctx, r, roomID, actor, model.EventRoomCreated, nil, nil, map[string]any{
			"name":           name,
			"visibility":     string(visibility),
			"defaultDocRole": string(defaultRole),
		})
	})
	if err != nil {
		return nil, err
	}
	return room, nil
}

// List returns the rooms the caller belongs to.
func (s *RoomService) List(ctx context.Context, actor uuid.UUID) ([]model.Room, error) {
	return s.store.Rooms().ListForPeer(ctx, actor)
}

func (s *RoomService) Get(ctx context.Context, actor, roomID uuid.UUID) (*model.Room, error) {
	if _, err := requireMember(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	return s.store.Rooms().Get(ctx, roomID)
}

// Discover searches public rooms by name prefix, for anyone signed in.
func (s *RoomService) Discover(ctx context.Context, actor uuid.UUID, query string) ([]model.RoomSummary, error) {
	q, err := cleanSearch(query, maxRoomNameLen)
	if err != nil {
		return nil, err
	}
	return s.store.Rooms().SearchPublic(ctx, q, actor, searchLimit)
}

// Preview shows a room's summary before joining. Anyone signed in may preview
// a public room; a private room is ErrNotFound unless the caller is a member.
func (s *RoomService) Preview(ctx context.Context, actor, roomID uuid.UUID) (*model.RoomSummary, error) {
	sum, err := s.store.Rooms().Summary(ctx, roomID, actor)
	if err != nil {
		return nil, err
	}
	if sum.Visibility != model.VisibilityPublic && !sum.IsMember {
		return nil, ErrNotFound
	}
	return sum, nil
}

// UpdateRoomInput changes only the fields that are set.
type UpdateRoomInput struct {
	Name           *string
	Visibility     *model.Visibility
	DefaultDocRole *model.DocRoles
}

// Update is OWNER-only. A name change records RENAME_ROOM; visibility and
// default-doc-role changes are plain updates with no event of their own.
// Public rooms have no invites, so switching a room to PUBLIC revokes its
// pending invites (one INVITE_REVOKED each, reason "room_public").
func (s *RoomService) Update(ctx context.Context, actor, roomID uuid.UUID, in UpdateRoomInput) (*model.Room, error) {
	if in.Name == nil && in.Visibility == nil && in.DefaultDocRole == nil {
		return nil, invalid("nothing to update")
	}

	var patch RoomPatch
	if in.Name != nil {
		name, err := cleanText("name", *in.Name, maxRoomNameLen)
		if err != nil {
			return nil, err
		}
		patch.Name = &name
	}
	if in.Visibility != nil {
		if !validVisibility(*in.Visibility) {
			return nil, invalid("visibility must be PUBLIC or PRIVATE")
		}
		patch.Visibility = in.Visibility
	}
	if in.DefaultDocRole != nil {
		if !validDefaultDocRole(*in.DefaultDocRole) {
			return nil, invalid("defaultDocRole must be EDITOR or VIEWER")
		}
		patch.DefaultDocRole = in.DefaultDocRole
	}

	var updated *model.Room
	err := s.store.WithTx(ctx, func(r Repos) error {
		current, err := r.Rooms().LockForUpdate(ctx, roomID)
		if err != nil {
			return err
		}
		if err := requireOwner(ctx, r, roomID, actor); err != nil {
			return err
		}

		updated, err = r.Rooms().Update(ctx, roomID, patch)
		if err != nil {
			return err
		}

		if current.Visibility != model.VisibilityPublic && updated.Visibility == model.VisibilityPublic {
			revoked, err := r.Invites().RevokePending(ctx, roomID, nil, s.now())
			if err != nil {
				return err
			}
			for _, inv := range revoked {
				target := inv.Invitee.PeerID
				err := s.event(ctx, r, roomID, actor, model.EventInviteRevoked, &target, nil, map[string]any{
					"inviteId": inv.InviteID.String(),
					"reason":   "room_public",
				})
				if err != nil {
					return err
				}
			}
		}

		if patch.Name != nil && *patch.Name != current.Name {
			return s.event(ctx, r, roomID, actor, model.EventRenameRoom, nil, nil, map[string]any{
				"oldName": current.Name,
				"newName": *patch.Name,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// Delete is OWNER-only. Documents, members and the event log go with the room
// (cascade), so no event is written.
func (s *RoomService) Delete(ctx context.Context, actor, roomID uuid.UUID) error {
	return s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		if err := requireOwner(ctx, r, roomID, actor); err != nil {
			return err
		}
		return r.Rooms().Delete(ctx, roomID)
	})
}
