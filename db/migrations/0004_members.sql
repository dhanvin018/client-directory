-- Model: Member, including its DocRoles map

CREATE TABLE IF NOT EXISTS members (
    room_id    UUID        NOT NULL REFERENCES rooms (room_id) ON DELETE CASCADE,
    peer_id    UUID        NOT NULL REFERENCES peers (peer_id),
    room_role  TEXT        NOT NULL
               CHECK (room_role IN ('OWNER', 'PARTICIPANT')),
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    invited_by UUID        REFERENCES peers (peer_id),  -- NULL for the room creator

    PRIMARY KEY (room_id, peer_id)
);

-- "Which rooms is this peer in?"
CREATE INDEX IF NOT EXISTS idx_members_peer ON members (peer_id);

-- Member.DocRoles map[docID]DocRole, one row per (member, document).
-- Assumption: no row means the member falls back to rooms.default_doc_role;
-- resolve that in the application layer.
CREATE TABLE IF NOT EXISTS member_doc_roles (
    room_id  UUID NOT NULL,
    peer_id  UUID NOT NULL,
    doc_id   UUID NOT NULL,
    doc_role TEXT NOT NULL
             CHECK (doc_role IN ('DOC_OWNER', 'EDITOR', 'VIEWER')),

    PRIMARY KEY (doc_id, peer_id),

    -- Both references include room_id, so the member and the document
    -- are guaranteed to belong to the same room.
    FOREIGN KEY (room_id, peer_id)
        REFERENCES members (room_id, peer_id) ON DELETE CASCADE,
    FOREIGN KEY (room_id, doc_id)
        REFERENCES document_refs (room_id, doc_id) ON DELETE CASCADE
);

-- "Everything this member can access in a room".
CREATE INDEX IF NOT EXISTS idx_member_doc_roles_member
    ON member_doc_roles (room_id, peer_id);
