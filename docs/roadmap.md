# Roadmap

## Known issues

| Issue | Impact | Fix |
|---|---|---|
| **Session revocation is in memory** (`revokedTokens` map) | Logouts are forgotten on restart and not shared between instances; the map grows without bound | Store revoked `jti`s (with expiry) in Postgres or Redis, or switch to opaque sessions in a table |
| Test directory is misspelled `repostiory_test` | Cosmetic | Rename to `repository_test` |
| The entry point lives in `internal/main` | Unconventional (`cmd/<name>` is usual) | Move to `cmd/client-directory` when convenient |
| Some files are not `gofmt`-formatted (`auth/session*.go`, several `router/*.go`) | Cosmetic | `gofmt -w` |

## Directory: still to build

**Needed by the editor and collaboration server**
- An authorization check for the collaboration server: given a peer and a
  document, return the effective document role (internal endpoint, or claims
  in a short-lived document token).
- Desktop sign-in if the client is a desktop app: system browser, loopback
  redirect with PKCE, token returned to the app instead of a cookie
  (decision P6).
- Invite deep links for the desktop app (for example `collabdoc://invite/{code}`)
  and a small web landing page; `invites.linkBaseUrl` points there.
- Push notifications (new invite, accepted invite) over the collaboration
  server's WebSocket instead of polling `GET /invites`.
- Comment threads (if they live in the directory's database), anchored to CRDT
  character IDs; a COMMENTER role if chosen (decision C5).

**Hardening**
- Fix the known issues above.
- Production settings: `cookieSecure = true`, explicit CORS origins, real JWT
  secret.
- General rate limiting (sign-in, search), not only invites.
- Clean up old invites (accepted, declined, revoked, expired) after a
  retention period.
- Handler tests for rooms, documents, members and events (today they are
  covered by the API tests only for the main paths).
- OpenAPI spec generated from or checked against the routes.
- Metrics (request counts and latency per route) and tracing.

**Later**
- Account deletion (blocked by audit events and memberships referencing the
  peer: decide whether to anonymize).
- Ownership transfer UX (possible today with role updates).
- Friends list and chat (decision C7).

## Project-level next steps

1. These docs (done).
2. Make the CRDT engine sync-capable: public remote apply, update encoding,
   state-vector diff, change events, UTF-16 positions, no panics on unknown
   IDs, remove hot-path logging, convergence fuzz tests.
3. Spike: Wails desktop app with Quill and the native engine, two windows
   editing one document through a minimal Go WebSocket relay.
4. Collaboration server: auth, permission checks, server copy, snapshots,
   reconnect and resync.
5. Formatting in the engine, then the editor toolbar (font, size, styles).
6. Presence (remote cursors), then comments.
7. Version history and offline local storage.
8. Friends and chat.
