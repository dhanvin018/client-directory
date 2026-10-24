-- Desktop sign-in (OAuth 2.0 for native apps, RFC 8252, with PKCE, RFC 7636).
--
-- The directory stays the only OAuth client Google knows about. A desktop app
-- starts sign-in with a loopback redirect URI and a PKCE challenge; after
-- Google, the directory hands the app a one-time code through that loopback
-- URI, and the app trades code + verifier for a session token at /auth/token.
-- Both tables hold short-lived, single-use rows (deleted when used).

-- A desktop sign-in waiting for the user to finish at Google, keyed by the
-- OAuth state the directory sent to Google.
CREATE TABLE desktop_logins (
    state          TEXT        PRIMARY KEY,
    redirect_uri   TEXT        NOT NULL,  -- http://127.0.0.1:<port>/<path>, validated by the service
    code_challenge TEXT        NOT NULL,  -- base64url(SHA-256(code_verifier)), method S256 only
    app_state      TEXT        NOT NULL,  -- the app's own state, echoed back to its loopback listener
    expires_at     TIMESTAMPTZ NOT NULL
);

-- A one-time code issued to a desktop app after a successful sign-in. Only a
-- hash of the code is stored: it is a bearer credential until redeemed.
CREATE TABLE desktop_auth_codes (
    code_hash      BYTEA       PRIMARY KEY,  -- SHA-256 of the code
    peer_id        UUID        NOT NULL REFERENCES peers (peer_id) ON DELETE CASCADE,
    redirect_uri   TEXT        NOT NULL,     -- must match on redemption
    code_challenge TEXT        NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL
);

-- Expired rows are purged opportunistically when new ones are written.
CREATE INDEX idx_desktop_logins_expires     ON desktop_logins (expires_at);
CREATE INDEX idx_desktop_auth_codes_expires ON desktop_auth_codes (expires_at);
