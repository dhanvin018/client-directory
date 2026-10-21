-- Model: RoomEvent

CREATE TABLE IF NOT EXISTS room_events (
    room_id       UUID        NOT NULL REFERENCES rooms (room_id) ON DELETE CASCADE,

    -- Per-room monotonic sequence assigned by the directory from
    -- rooms.last_event_id inside the inserting transaction.
    -- Clients sync with: WHERE room_id = $1 AND event_id > $2 ORDER BY event_id
    event_id      BIGINT      NOT NULL,

    actor_id      UUID        NOT NULL REFERENCES peers (peer_id),

    -- Deliberately no FKs on the targets: members and docs get removed,
    -- but the audit log must keep pointing at them.
    target_id     UUID,
    target_doc_id UUID,

    event_type    TEXT        NOT NULL
                  CHECK (event_type IN (
                      'ROOM_CREATED',
                      'CREATE_DOC',
                      'DELETE_DOC',
                      'CLONE_DOC',
                      'ADD_MEMBER',
                      'REMOVE_MEMBER',
                      'UPDATE_ROLE',        -- legacy: superseded by the two below, drop if unused
                      'RENAME_ROOM',
                      'RENAME_DOC',
                      'UPDATE_ROOM_ROLE',
                      'UPDATE_DOC_ROLE'
                  )),

    details       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (room_id, event_id)
);
