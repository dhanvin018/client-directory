-- Sessions: one row per signed-in device (browser or desktop app).
--
-- Access tokens are short-lived JWTs that carry the session ID and are
-- verified without touching the database. A session's refresh token gets new
-- access tokens at POST /auth/refresh; logout deletes the row, so a logout is
-- durable across restarts and shared by every instance of the directory.
CREATE TABLE sessions (
    session_id            UUID        PRIMARY KEY,
    peer_id               UUID        NOT NULL REFERENCES peers (peer_id) ON DELETE CASCADE,
    refresh_hash          BYTEA       NOT NULL UNIQUE,  -- SHA-256 of the current refresh token
    previous_refresh_hash BYTEA       UNIQUE,           -- the token it replaced; presenting it again means theft
    created_at            TIMESTAMPTZ NOT NULL,
    refreshed_at          TIMESTAMPTZ NOT NULL,
    expires_at            TIMESTAMPTZ NOT NULL          -- moves forward on every refresh
);

CREATE INDEX idx_sessions_peer    ON sessions (peer_id);     -- "sign out everywhere"
CREATE INDEX idx_sessions_expires ON sessions (expires_at);  -- purge
