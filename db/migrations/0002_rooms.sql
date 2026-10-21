-- Model: Room

CREATE TABLE IF NOT EXISTS rooms (
    room_id          UUID PRIMARY KEY,
    name             TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    visibility       TEXT        NOT NULL
                     CHECK (visibility IN ('PUBLIC', 'PRIVATE')),

    -- DOC_OWNER is deliberately excluded: ownership is never granted by default.
    -- App-level validation still runs at room creation; this is a backstop.
    default_doc_role TEXT        NOT NULL
                     CHECK (default_doc_role IN ('EDITOR', 'VIEWER')),

    -- Per-room event sequence counter, see 0005_room_events.sql.
    -- Bump and read it inside the same transaction that inserts the event:
    --   UPDATE rooms SET last_event_id = last_event_id + 1
    --   WHERE room_id = $1 RETURNING last_event_id;
    last_event_id    BIGINT      NOT NULL DEFAULT 0
);
