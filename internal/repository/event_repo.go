package repository

import (
	"context"
	"encoding/json"
	"math"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/service"

	"github.com/dhanvin018/client-directory/internal/model"
)

type eventRepo struct{ q dbtx }

// appendEventSQL assigns the next per-room event ID and inserts the event in
// ONE statement: the UPDATE ... RETURNING takes the room row's lock, so two
// concurrent appends to a room get distinct, consecutive IDs, and the second
// waits for the first transaction to commit. If the room does not exist the
// CTE returns no row and nothing is inserted.
const appendEventSQL = `
WITH next AS (
    UPDATE rooms
    SET last_event_id = last_event_id + 1
    WHERE room_id = $1::uuid
    RETURNING last_event_id
)
INSERT INTO room_events
    (room_id, event_id, actor_id, target_id, target_doc_id, event_type, details, occurred_at)
SELECT $1::uuid, next.last_event_id, $2::uuid, $3::uuid, $4::uuid,
       $5::text, $6::jsonb, $7::timestamptz
FROM next`

// Append must run in the same transaction as the change it describes.
// It returns ErrNotFound if the room does not exist.
func (r eventRepo) Append(ctx context.Context, e service.NewEvent) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return affectedOne(r.q.ExecContext(ctx, appendEventSQL,
		e.RoomID, e.ActorID, nullUUID(e.TargetID), nullUUID(e.TargetDocID),
		string(e.Type), string(payload), e.At))
}

// List returns events with event_id greater than after, oldest first.
func (r eventRepo) List(ctx context.Context, roomID uuid.UUID, after uint64, limit int) ([]model.RoomEvent, error) {
	if after > math.MaxInt64 {
		after = math.MaxInt64
	}
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+eventCols+` FROM room_events
		 WHERE room_id = $1 AND event_id > $2
		 ORDER BY event_id
		 LIMIT $3`,
		roomID, int64(after), limit)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanEvent)
}
