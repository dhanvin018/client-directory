# Decision log

Status: **Decided** (implemented or agreed), **Proposed** (recommended, not
yet confirmed), **Open** (needs a decision).

## Directory: architecture

| # | Decision | Why | Status |
|---|---|---|---|
| D1 | The directory stores document **references** only, never content | Content sync is a different workload (real-time, CRDT); keeping it out keeps this service a plain transactional REST API | Decided |
| D2 | Layers: handler → service → repository, with repository interfaces declared in the service package | Business rules testable without a database; SQL swappable | Decided |
| D3 | Every room write locks the room row first (`FOR NO KEY UPDATE`) | Serializes writers per room so invariants (last owner, single-use invites, event IDs) hold under concurrency without serializable isolation | Decided |
| D4 | Audit events are written in the same transaction as the change, with gapless per-room IDs from `rooms.last_event_id` | The log can never disagree with the data; clients sync with `after=` | Decided |
| D5 | Non-members get 404 instead of 403 | Outsiders cannot learn which rooms exist | Decided |
| D6 | Services generate IDs, codes and timestamps; the clock is injectable (`WithClock`) | Deterministic tests for expiry and rate limits | Decided |
| D7 | Errors are wrapped sentinels mapped to HTTP status in one place | Consistent responses; internals never leak (500s are generic and logged) | Decided |

## Rooms, roles and joining

| # | Decision | Why | Status |
|---|---|---|---|
| R1 | Two role levels: room role (OWNER, PARTICIPANT) and per-document role (DOC_OWNER, EDITOR, VIEWER) with a room default (EDITOR or VIEWER) | Fine-grained sharing inside a room; DOC_OWNER is never granted by default | Decided |
| R2 | A room must keep at least one OWNER | Otherwise nobody can administer it; the alternative is deleting the room | Decided |
| R3 | **Visibility decides how people join.** PUBLIC: anyone signed in joins directly, no invites. PRIVATE: invite-only | Matches the Discord model; one clear rule per room | Decided |
| R4 | Default visibility is PRIVATE | PUBLIC means anyone can walk in, so it must be an explicit choice | Decided |
| R5 | No direct add: `POST /rooms/{id}/members` was removed | Invites (private) and joins (public) are the only ways in | Decided |
| R6 | Switching PRIVATE → PUBLIC revokes all pending invites (event reason `room_public`) | Invites do not exist for public rooms | Decided |
| R7 | PUBLIC → PRIVATE keeps existing members | Changing visibility controls future joins, not current members | Decided |
| R8 | Public rooms are discoverable by name prefix; private rooms never are | | Decided |

## Invites

| # | Decision | Why | Status |
|---|---|---|---|
| I1 | Any member may invite (not only owners) | "An existing member invites them" | Decided |
| I2 | The inviter finds people by searching handle or display name | Discord-style; handles disambiguate equal names | Decided |
| I3 | An invite is bound to one invitee; anyone else opening the link gets 404 | A forwarded link is useless and reveals nothing | Decided |
| I4 | Invites always join as PARTICIPANT; owners promote afterwards | A participant cannot hand out ownership | Decided |
| I5 | One pending invite per room and invitee; inviting again returns the existing one | No inbox spam, idempotent clients | Decided |
| I6 | Invites expire after 7 days and are single-use | Stale links stop working | Decided |
| I7 | Expiry is evaluated on read; no background job | Simpler; the row is marked EXPIRED lazily on re-invite | Decided |
| I8 | Code: 128 random bits, base64url (22 chars), stored in plain text | Unguessable; bound to the invitee, so a leaked code is harmless and the inbox can show the link again | Decided |
| I9 | The link is built at response time from `invites.linkBaseUrl` | Changing the domain does not break existing invites | Decided |
| I10 | Pending invites survive the inviter leaving the room | Simplest; matches Discord. Owners can revoke | Decided |
| I11 | Rate limit: 30 new invites per inviter per hour (re-sends do not count) | Search plus invite should not be a spam tool | Decided |
| I12 | Notifications: the inbox is polled for now; push over WebSocket later | No real-time channel exists yet | Decided (push pending) |

## Bans

| # | Decision | Why | Status |
|---|---|---|---|
| B1 | Bans exist, managed by room OWNERs | In a public room, removal alone is pointless because people can rejoin | Decided |
| B2 | Banning removes membership and revokes the peer's pending invites; a non-member can be banned too | Keeps someone out before they join | Decided |
| B3 | Owners cannot be banned (demote first); you cannot ban yourself | Protects the last-owner rule; avoids accidents | Decided |
| B4 | Separate endpoints (`PUT`/`DELETE /rooms/{id}/bans/{peerId}`) rather than a flag on remove | Clean REST; bans can be listed and lifted | Decided |
| B5 | Leaving never bans | | Decided |

## Peers and identity

| # | Decision | Why | Status |
|---|---|---|---|
| P1 | Sign-in with Google OAuth; first sign-in creates the peer | No passwords to store | Decided |
| P2 | Unique handle per peer, generated at sign-up from the display name, changeable later | Display names are not unique, so search needs something that is | Decided |
| P3 | Handle format `^[a-z0-9._]{2,32}$`, stored lowercase, `@` prefix accepted on input | Discord-like; case-insensitive uniqueness | Decided |
| P4 | Search requires at least 2 characters and returns at most 10 results, exposing only peer ID, handle and display name | Search must not be a way to list every user | Decided |
| P5 | Access tokens are HS256 JWTs, accepted as cookie or bearer token | Stateless verification: no lookup per request | Decided (lifetime and logout revised by P13–P17) |
| P6 | Desktop sign-in: system browser + loopback redirect + PKCE (RFC 8252 / RFC 7636), token in the OS keychain | A desktop app cannot receive the server-set cookie, and an installed app cannot keep a client secret | Decided |
| P7 | The directory stays the only OAuth client: the app never talks to Google directly | Google's client secret stays on the server; one sign-in implementation for web and desktop; adding providers needs no app release | Decided |
| P8 | Desktop redirect URIs are loopback IP literals only (`127.0.0.1`, `[::1]`, any port); no `localhost`, no custom schemes | Cannot be an open redirect; `localhost` can resolve elsewhere; custom schemes can be claimed by other apps | Decided |
| P9 | One-time desktop codes: 256 random bits, 60 s, single use (spent even on a failed attempt), stored as SHA-256 | A code is a bearer credential until redeemed | Decided |
| P10 | Pending desktop logins and codes live in Postgres, not memory | Work across restarts and multiple instances | Decided |
| P11 | Desktop sessions reuse the same 24h JWT; no refresh tokens yet | Smallest change; users re-sign-in daily through the browser | Superseded by P13 |
| P12 | Invites carry a web link (`url`) and a desktop deep link (`appUrl`, `collabdoc://invite/…`) | People without the app need a page to land on | Decided (scheme name is a placeholder) |
| P13 | **Short access tokens plus refresh tokens stored in Postgres.** Access JWTs last 15 min and are checked without the database; refresh tokens (30 days, sliding) live in a `sessions` table; logout deletes the session row | Replaces the in-memory revocation list, which forgot logouts on restart, was not shared between instances and grew without bound. Long-lived desktop sign-in needs revocation that persists. The database is touched only at sign-in, refresh (about every 15 min per client) and logout, never on ordinary requests | Decided |
| P14 | **Postgres, not Redis**, for sessions | Refresh tokens are durable data: losing them logs users out, losing a revocation brings back a logged-out session. Redis is in-memory by default and would be a second datastore to run, back up and fail; the speed difference is invisible at a few queries per user per hour. Postgres also gives a foreign key to peers (cascade on delete) and "sign out everywhere" as one `DELETE` | Decided. Revisit when the collaboration server needs Redis for pub/sub; a per-request revocation list could then share it (see P15) |
| P15 | After logout, access tokens already issued stay valid until they expire (at most 15 min); there is no per-request revocation check | Checking every request against a list would put a lookup on every request, which is what JWTs avoid. Logged-out clients drop their tokens anyway; the window only matters for a stolen access token | Decided. If instant logout becomes a requirement: a revocation list of `sid`s in Redis with TTL = token lifetime |
| P16 | Refresh tokens rotate on every use; presenting a spent one ends the session (reuse detection) | A stolen refresh token is caught the moment either the thief or the user uses it after the other (RFC 9700 section 4.14). Cost: two concurrent refreshes from one client log it out, so clients refresh one at a time | Decided |
| P17 | Browsers get the refresh token as an `HttpOnly` cookie scoped to `/auth`; desktop apps get it in JSON and keep it in the OS keychain | Page scripts never see a browser's tokens (XSS); the refresh cookie is not sent on API calls. Desktop apps have no cookie jar to rely on | Decided |
| P18 | Logout is public and accepts the refresh token or an access token (expired ones too); `POST /auth/logout-all` ends every session | A client whose access token just expired must still be able to log out; "sign out everywhere" covers a lost device | Decided |
| P19 | Google sign-in requests online access only (no `access_type=offline`) | Google is used only to identify the user at sign-in. Offline access would give the server a long-lived Google refresh token it has no use for, a liability if stored. Clients that lose their network are covered by the directory's own refresh token (P13), not Google's | Decided. Revisit only for a feature that calls Google APIs on the user's behalf (e.g. Drive import) |

## Operations

| # | Decision | Why | Status |
|---|---|---|---|
| O1 | Configuration in `config.json`, every key overridable by an environment variable | One place for settings; env for per-environment and secret values | Decided |
| O2 | Numbered SQL migrations recorded in `schema_migrations`, never edited once applied | Changes to existing tables need ordered, run-once migrations | Decided |
| O3 | All pending migrations run in one transaction under an advisory lock | Replicas starting together do not race; failure leaves the DB unchanged | Decided |
| O4 | Migrations run on every container start (`docker-entrypoint.sh`), then the server starts as PID 1 | No separate deploy step; graceful shutdown still works | Decided |
| O5 | CI: apply migrations twice, vet, all tests against Postgres, build and boot the image | Catches non-idempotent startup, SQL bugs and broken images | Decided |
| O6 | Postgres tests only run when `TEST_DATABASE_URL` is set | They empty every table | Decided |
| O7 | Secrets out of git: `config.json` is git-ignored; `config.example.json` (secrets blank) is committed and is what the Docker image and CI use | GitHub push protection rejected a commit with OAuth credentials | Decided |
| O8 | An empty JWT secret stops startup | The image ships the template with a blank secret; signing with an empty key would let anyone forge sessions | Decided |

## Client and collaboration (cross-cutting)

| # | Decision | Why | Status |
|---|---|---|---|
| C1 | The editor is a **desktop app** | Native storage and notifications; the Go engine runs natively | Decided |
| C1a | Desktop framework: Wails (Go + a web UI in the OS webview) | The CRDT engine is Go; the editor can still use a web rich-text framework | Proposed |
| C2 | Editor framework: Quill 2 | Its change format (insert / delete / retain with attributes) maps directly onto a flat CRDT sequence; rich text built in | Proposed |
| C3 | A separate collaboration server (Go, WebSockets) relays updates, checks document roles and keeps the server copy | The directory stays a metadata service | Proposed |
| C4 | Comments: threads stored in a database, anchored to CRDT character IDs | Anchors survive concurrent edits; threads need authorship and permissions | Proposed |
| C5 | Who may comment: a new COMMENTER document role, or VIEWERs | | Open |
| C6 | Version history: keep it (prevents purging tombstones) or not | Decides the CRDT garbage-collection design | Open |
| C7 | Friends list and chat | After everything else | Deferred |
