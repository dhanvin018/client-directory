package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

const (
	defaultEventLimit = 100
	maxEventLimit     = 500
)

// EventService is read-only: events are written by the other services, in the
// same transaction as the change they describe.
type EventService struct{ base }

// List returns events with event_id greater than after, oldest first, so a
// client can sync with "everything after the last event I saw".
func (s *EventService) List(ctx context.Context, actor, roomID uuid.UUID, after uint64, limit int) ([]model.RoomEvent, error) {
	if _, err := requireMember(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultEventLimit
	}
	if limit > maxEventLimit {
		limit = maxEventLimit
	}
	return s.store.Events().List(ctx, roomID, after, limit)
}
