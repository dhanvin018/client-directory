# Roadmap

The project-wide progress tracker is `STATUS.md` at the project root; item
IDs in brackets (D1, N3, …) refer to it. This page covers the directory.

## Known issues

| Issue | Impact | Fix |
|---|---|---|
| Test directory is misspelled `repostiory_test` | Cosmetic | Rename to `repository_test` |
| The entry point lives in `internal/main` | Unconventional (`cmd/<name>` is usual) | Move to `cmd/client-directory` when convenient |
| Some files are not `gofmt`-formatted (`router/document-router.go`, `router/event-router.go`) | Cosmetic | `gofmt -w` |

## Directory: still to build

Editing is fully peer-to-peer (decision C8): there is no collaboration
server, and the directory is the only central component. What peers need
from it:

**Needed for peer-to-peer editing**
- **Asymmetric token signing** [D12, P1]: HS256 → Ed25519 with `kid` and a
  JWKS endpoint. Peers verify each other's directory-issued tokens when they
  connect (decision C11); with HS256 any verifier could also forge tokens, so
  the shared secret can never be given to clients.
- **Role lookups for peers** [D1]: a peer's effective role on a document, so
  receiving peers can drop operations from viewers, removed or banned
  members. Either an endpoint, or roles carried in a short-lived signed token.
- **Online-peer discovery / signaling** [D17], if the directory is chosen as
  the rendezvous for peers to find and connect to each other.

**Desktop**
- ~~Desktop sign-in~~, ~~invite deep links~~, ~~refresh tokens~~ and
  ~~persistent logout~~: done (see [desktop-clients.md](desktop-clients.md),
  decisions P6–P19).
- A small **web landing page** at `invites.linkBaseUrl` that tries
  `collabdoc://invite/{code}` and otherwise offers the download [D3].
- Pick the final **URL scheme** (`collabdoc` is a placeholder) before the
  first release; changing it later breaks shared links [D2].
- A "signed-in devices" list (`GET /auth/sessions`, end one by ID); the table
  already has what it needs, plus a device name to add [D4].

**Other features**
- Comment threads, if they live in the directory's database (they may
  instead be synced between peers), anchored to CRDT character IDs; a
  COMMENTER role if chosen (decision C5) [D5].
- Push notifications for invites [D18]. Clients poll `GET /invites` today;
  there is no central WebSocket, so push needs its own channel.

**Hardening**
- Fix the known issues above.
- Production settings: `cookieSecure = true`, explicit CORS origins, real JWT
  secret (or signing key, after D12) [D6].
- General rate limiting (sign-in, refresh, search), not only invites [D7].
- Clean up old invites (accepted, declined, revoked, expired) after a
  retention period [D8].
- Handler tests for rooms, documents, members and events (today they are
  covered by the API tests only for the main paths) [D9].
- OpenAPI spec generated from or checked against the routes [D10].
- Metrics (request counts and latency per route) and tracing [D11].

**Later**
- Instant logout, if ever required: a per-request revocation list of session
  IDs (decision P15) [D15].
- Account deletion (blocked by audit events and memberships referencing the
  peer: decide whether to anonymize) [D13].
- Ownership transfer UX (possible today with role updates) [D14].
- Friends list and chat (decision C7) [D16].

## Project-level next steps

See `STATUS.md` for the full, prioritised list. In short:

1. Expose the CRDT engine's sync API (the algorithm is complete): remote
   apply, delta encoding, state-vector diff, change events, UTF-16
   positions, no panics on unknown IDs, no hot-path logging, convergence
   fuzz tests.
2. Decide the peer transport, NAT traversal and online-peer discovery.
3. Spike: two Wails apps with Quill and the engine, syncing one document
   directly peer to peer.
4. Peer authentication and role enforcement, with asymmetric signing here.
5. Desktop app: sign-in, directory screens, local storage, offline editing.
6. Formatting, toolbar, presence, comments.
7. Installer, versioning and release pipeline.
8. Tombstone garbage collection.
9. Friends and chat.
