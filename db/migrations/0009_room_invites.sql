-- Model: RoomInvite
--
-- Invitations are how peers join PRIVATE rooms. Each invite is bound to one
-- invitee: the code in the link only works for them. Public rooms have no
-- invites; switching a room to PUBLIC revokes its pending ones.
--
-- The code is stored in plain text so the inbox can show the link again. A
-- leaked code is useless to anyone but the invitee.

CREATE TABLE room_invites (
    invite_id    UUID        PRIMARY KEY,
    code         TEXT        NOT NULL,
    room_id      UUID        NOT NULL REFERENCES rooms (room_id) ON DELETE CASCADE,
    inviter_id   UUID        NOT NULL REFERENCES peers (peer_id),
    invitee_id   UUID        NOT NULL REFERENCES peers (peer_id),

    -- Invites always join as PARTICIPANT; owners promote afterwards.
    room_role    TEXT        NOT NULL DEFAULT 'PARTICIPANT'
                 CHECK (room_role IN ('PARTICIPANT')),

    -- PENDING past expires_at is reported as EXPIRED by the service and is
    -- marked EXPIRED lazily the next time that invitee is invited.
    status       TEXT        NOT NULL DEFAULT 'PENDING'
                 CHECK (status IN ('PENDING', 'ACCEPTED', 'DECLINED', 'REVOKED', 'EXPIRED')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    responded_at TIMESTAMPTZ,

    CONSTRAINT uq_room_invites_code UNIQUE (code)
);

-- At most one live invite per room and invitee.
CREATE UNIQUE INDEX uq_room_invites_pending
    ON room_invites (room_id, invitee_id) WHERE status = 'PENDING';

-- The invitee's inbox.
CREATE INDEX idx_room_invites_invitee_pending
    ON room_invites (invitee_id, created_at) WHERE status = 'PENDING';

-- Per-inviter rate limiting ("invites sent in the last hour").
CREATE INDEX idx_room_invites_inviter_recent
    ON room_invites (inviter_id, created_at);
