-- Model: RoomBan
--
-- A banned peer cannot join a public room or accept an invite to a private
-- one until a room owner lifts the ban.

CREATE TABLE room_bans (
    room_id   UUID        NOT NULL REFERENCES rooms (room_id) ON DELETE CASCADE,
    peer_id   UUID        NOT NULL REFERENCES peers (peer_id),
    banned_by UUID        NOT NULL REFERENCES peers (peer_id),
    banned_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (room_id, peer_id)
);
