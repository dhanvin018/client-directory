package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

// ---- Authorization ----
//
// Permission defaults:
//   room OWNER   : rename/delete room, visibility and default doc role,
//                  add/remove members, change room roles
//   any member   : read the room, documents and members; create/clone documents;
//                  leave the room (unless the last OWNER)
//   doc manager  : DOC_OWNER of that document, or room OWNER. May rename,
//                  reorder and delete it and change doc roles on it.

// requireMember returns the caller's room role. A non-member gets ErrNotFound
// rather than ErrForbidden, so a room's existence is not revealed to outsiders.
func requireMember(ctx context.Context, r Repos, roomID, actor uuid.UUID) (model.RoomRoles, error) {
	return r.Members().GetRoomRole(ctx, roomID, actor)
}

func requireOwner(ctx context.Context, r Repos, roomID, actor uuid.UUID) error {
	role, err := requireMember(ctx, r, roomID, actor)
	if err != nil {
		return err
	}
	if role != model.RoleOwner {
		return forbidden("room owner required")
	}
	return nil
}

func requireDocManager(ctx context.Context, r Repos, roomID, docID, actor uuid.UUID) error {
	role, err := requireMember(ctx, r, roomID, actor)
	if err != nil {
		return err
	}
	if role == model.RoleOwner {
		return nil
	}
	docRole, found, err := r.Members().GetDocRole(ctx, roomID, docID, actor)
	if err != nil {
		return err
	}
	if found && docRole == model.DocRoleOwner {
		return nil
	}
	return forbidden("document owner or room owner required")
}

// ensureAnotherOwner is called before removing or demoting an OWNER. The
// counted owners include the one being changed, so more than one must exist.
func ensureAnotherOwner(ctx context.Context, r Repos, roomID uuid.UUID) error {
	n, err := r.Members().CountRoomOwners(ctx, roomID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return conflict("a room must keep at least one owner; delete the room instead")
	}
	return nil
}

// ---- Validation ----

const (
	maxUserNameLen = 64
	maxRoomNameLen = 100
	maxDocTitleLen = 200
	maxRankLen     = 128
)

func validVisibility(v model.Visibility) bool {
	return v == model.VisibilityPublic || v == model.VisibilityPrivate
}

func validRoomRole(r model.RoomRoles) bool {
	return r == model.RoleOwner || r == model.RoleParticipant
}

func validDocRole(r model.DocRoles) bool {
	return r == model.DocRoleOwner || r == model.DocRoleEditor || r == model.DocRoleViewer
}

// validDefaultDocRole excludes DOC_OWNER: ownership is never granted by default.
func validDefaultDocRole(r model.DocRoles) bool {
	return r == model.DocRoleEditor || r == model.DocRoleViewer
}

// cleanText trims s and checks it is non-empty and at most max characters.
func cleanText(field, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalid("%s must not be empty", field)
	}
	if utf8.RuneCountInString(s) > max {
		return "", invalid("%s must be at most %d characters", field, max)
	}
	return s, nil
}

// cleanRank checks a fractional-index rank. Ranks are opaque, so no trimming.
func cleanRank(s string) (string, error) {
	if s == "" {
		return "", invalid("positionRank is required")
	}
	if len(s) > maxRankLen {
		return "", invalid("positionRank must be at most %d bytes", maxRankLen)
	}
	return s, nil
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
