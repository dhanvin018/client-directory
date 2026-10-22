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
| P5 | Sessions are HS256 JWTs (24h), accepted as cookie or bearer token | Stateless verification | Decided (revocation needs work, see roadmap) |
| P6 | Desktop sign-in: system browser + loopback redirect + PKCE, token in the OS keychain | A desktop app cannot receive the server-set cookie | Proposed (depends on C1) |

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
| C1 | The editor is a **desktop app** built with Wails (Go + a web UI in the OS webview) | The CRDT engine is Go, so it runs natively in the app and in the collaboration server; native storage and notifications | Proposed |
| C2 | Editor framework: Quill 2 | Its change format (insert / delete / retain with attributes) maps directly onto a flat CRDT sequence; rich text built in | Proposed |
| C3 | A separate collaboration server (Go, WebSockets) relays updates, checks document roles and keeps the server copy | The directory stays a metadata service | Proposed |
| C4 | Comments: threads stored in a database, anchored to CRDT character IDs | Anchors survive concurrent edits; threads need authorship and permissions | Proposed |
| C5 | Who may comment: a new COMMENTER document role, or VIEWERs | | Open |
| C6 | Version history: keep it (prevents purging tombstones) or not | Decides the CRDT garbage-collection design | Open |
| C7 | Friends list and chat | After everything else | Deferred |
