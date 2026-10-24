# Roadmap

## Known issues

| Issue | Impact | Fix |
|---|---|---|
| Test directory is misspelled `repostiory_test` | Cosmetic | Rename to `repository_test` |
| The entry point lives in `internal/main` | Unconventional (`cmd/<name>` is usual) | Move to `cmd/client-directory` when convenient |
| Some files are not `gofmt`-formatted (`router/document-router.go`, `router/event-router.go`) | Cosmetic | `gofmt -w` |

## Directory: still to build

**Needed by the editor and collaboration server**
- An authorization check for the collaboration server: given a peer and a
  document, return the effective document role (internal endpoint, or claims
  in a short-lived document token).
- ~~Desktop sign-in~~ and ~~invite deep links~~: done (see
  [desktop-clients.md](desktop-clients.md)). Still to do for desktop:
  - ~~Refresh tokens~~ and ~~persistent logout~~: done (decisions P13–P18).
  - A small **web landing page** at `invites.linkBaseUrl` that tries
    `collabdoc://invite/{code}` and otherwise offers the download.
  - Pick the final **URL scheme** (`collabdoc` is a placeholder) before the
    first release; changing it later breaks shared links.
  - A "signed-in devices" list (`GET /auth/sessions`, end one by ID); the
    table already has what it needs, plus a device name to add.
  - Instant logout, if ever required: a revocation list of session IDs
    checked per request, in Redis once the collaboration server brings it in
    (decision P15).
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
