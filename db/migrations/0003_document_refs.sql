-- Model: DocumentRef

CREATE TABLE IF NOT EXISTS document_refs (
    doc_id             UUID PRIMARY KEY,
    room_id            UUID NOT NULL REFERENCES rooms (room_id) ON DELETE CASCADE,
    title              TEXT NOT NULL,

    -- Fractional index for ordering docs inside a room.
    -- COLLATE "C" gives byte-wise comparison, which fractional indexing
    -- requires; a locale collation would sort ranks incorrectly.
    position_rank      TEXT COLLATE "C" NOT NULL,

    added_by           UUID NOT NULL REFERENCES peers (peer_id),
    added_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- NULL = created fresh. Deliberately no FK: the source doc may be deleted
    -- later and the clone must survive.
    cloned_from_doc_id UUID,

    -- Lets child tables use a composite FK so a doc can't be linked to
    -- a different room's members.
    CONSTRAINT uq_document_refs_room_doc UNIQUE (room_id, doc_id)
);

-- List a room's docs in display order.
CREATE INDEX IF NOT EXISTS idx_document_refs_room_rank
    ON document_refs (room_id, position_rank);
