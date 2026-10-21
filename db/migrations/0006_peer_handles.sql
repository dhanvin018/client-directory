-- Model: Peer.Handle
--
-- A unique handle (like a Discord username) so people with the same display
-- name can be told apart when searching for someone to invite. Stored
-- lowercase; the service validates the format and the CHECK is a backstop.

ALTER TABLE peers ADD COLUMN handle TEXT;

-- Peers created before handles existed get a slug of their display name plus
-- the start of their peer ID, which keeps the backfill unique. They can pick
-- a nicer handle afterwards.
UPDATE peers
SET handle = left(coalesce(nullif(regexp_replace(lower(user_name), '[^a-z0-9._]', '', 'g'), ''), 'user'), 24)
             || '_' || left(replace(peer_id::text, '-', ''), 6)
WHERE handle IS NULL;

ALTER TABLE peers
    ALTER COLUMN handle SET NOT NULL,
    ADD CONSTRAINT uq_peers_handle UNIQUE (handle),
    ADD CONSTRAINT ck_peers_handle_format CHECK (handle ~ '^[a-z0-9._]{2,32}$');

-- Case-insensitive prefix search on handle and display name. text_pattern_ops
-- lets LIKE 'abc%' use the index under any database collation.
CREATE INDEX idx_peers_handle_prefix    ON peers (handle text_pattern_ops);
CREATE INDEX idx_peers_user_name_prefix ON peers (lower(user_name) text_pattern_ops);
