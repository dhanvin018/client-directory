-- Model: Peer

CREATE TABLE IF NOT EXISTS peers (
    peer_id       UUID PRIMARY KEY,
    user_name     TEXT NOT NULL,
    auth_provider TEXT NOT NULL,   -- e.g. 'google'
    auth_subject  TEXT NOT NULL,   -- provider's stable user id

    -- one directory peer per third-party identity
    CONSTRAINT uq_peers_auth_identity UNIQUE (auth_provider, auth_subject)
);
