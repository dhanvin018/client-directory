# API reference

- Base path for authenticated routes: `/api/v1`.
- Authentication: `Authorization: Bearer <token>` or the `session` cookie set
  by sign-in.
- Request bodies are a single JSON object, at most 1 MiB; unknown fields are
  rejected with 400.
- Errors are `{"error": "message"}`; see
  [architecture.md](architecture.md#6-errors) for the status mapping.
- "Member" means a member of the room in the path. Non-members get 404 for
  anything about a room they cannot see.
- Every response carries `X-Request-ID` (echoed from the request if valid).

## Health and auth

| Method and path | Auth | Description | Success |
|---|---|---|---|
| `GET /healthz` | none | Liveness | 200 `ok` |
| `GET /auth/{provider}/login` | none | Sets the one-time `oauth_state` cookie and redirects to the provider (`google`) | 302 |
| `GET /auth/{provider}/callback` | none | Checks state, exchanges the code, signs up or signs in, sets the `session` cookie, redirects to `auth.postLoginRedirect` | 302 |
| `POST /auth/logout` | token | Revokes the session and clears the cookie | 204 |

Callback failures: bad or missing state 400; missing code 400; user declined
→ redirect to `postLoginRedirect?auth_error=access_denied`; provider exchange
failed 502. Logout returns 503 (and keeps the cookie) if the session could not
be revoked.

## Peers

| Method and path | Who | Body | Success |
|---|---|---|---|
| `GET /peers/me` | anyone | | 200 Peer |
| `PATCH /peers/me` | anyone | `{"userName"?: string, "handle"?: string}` | 200 Peer |
| `GET /peers/{peerId}` | anyone | | 200 Peer |

`PATCH /peers/me`: at least one field; handle is trimmed, a leading `@` is
dropped, lowercased, then must match `^[a-z0-9._]{2,32}$` (400); a handle
someone else has is 409. Both fields change in one transaction.

## Rooms

| Method and path | Who | Body / query | Success |
|---|---|---|---|
| `POST /rooms` | anyone | `{"name", "visibility"?, "defaultDocRole"?}` | 201 Room; caller becomes OWNER |
| `GET /rooms` | anyone | | 200 Room[] the caller belongs to |
| `GET /rooms/discover?q=` | anyone | q: 2–100 chars, prefix of the name | 200 RoomSummary[] (public rooms, max 10) |
| `GET /rooms/{roomId}` | member | | 200 Room |
| `GET /rooms/{roomId}/preview` | anyone (public) / member (private) | | 200 RoomSummary |
| `PATCH /rooms/{roomId}` | OWNER | `{"name"?, "visibility"?, "defaultDocRole"?}` | 200 Room |
| `DELETE /rooms/{roomId}` | OWNER | | 204; cascades everything |

Defaults: visibility PRIVATE, defaultDocRole VIEWER (DOC_OWNER is rejected).
Switching PRIVATE → PUBLIC revokes all pending invites.

## Membership and bans

| Method and path | Who | Body | Success |
|---|---|---|---|
| `GET /rooms/{roomId}/members` | member | | 200 Member[] |
| `GET /rooms/{roomId}/members/{peerId}` | member | | 200 Member |
| `POST /rooms/{roomId}/join` | anyone (PUBLIC rooms) | | 201 Member (joined) or 200 (already a member) |
| `DELETE /rooms/{roomId}/members/{peerId}` | self (leave) or OWNER | | 204 |
| `PUT /rooms/{roomId}/members/{peerId}/room-role` | OWNER | `{"roomRole"}` | 200 Member |
| `PUT /rooms/{roomId}/members/{peerId}/documents/{docId}/role` | document manager | `{"docRole"}` | 200 Member |
| `GET /rooms/{roomId}/bans` | OWNER | | 200 RoomBan[] |
| `PUT /rooms/{roomId}/bans/{peerId}` | OWNER | | 204 |
| `DELETE /rooms/{roomId}/bans/{peerId}` | OWNER | | 204 |

- Joining a private room: 404. Banned peer joining: 403.
- Removing or demoting the last OWNER: 409. Setting the role a member already
  has is a no-op.
- Ban: removes the peer if a member and revokes their pending invites. Banning
  yourself 400; banning an OWNER 409 (demote first); already banned 409.
  Unban of a peer who is not banned: 404.

## Invites (private rooms)

| Method and path | Who | Body / query | Success |
|---|---|---|---|
| `GET /rooms/{roomId}/invite-candidates?q=` | member | q: 2–64 chars, prefix of handle or display name | 200 InviteCandidate[] (max 10) |
| `POST /rooms/{roomId}/invites` | member | `{"inviteePeerId"}` | 201 invite (new) or 200 (existing pending one) |
| `GET /rooms/{roomId}/invites` | member | | 200 invite[] (pending, not expired) |
| `DELETE /rooms/{roomId}/invites/{inviteId}` | inviter or OWNER | | 204 |
| `GET /invites` | anyone | | 200 invite[]: the caller's inbox |
| `GET /invites/{code}` | invitee | | 200 invite |
| `POST /invites/{code}/accept` | invitee | | 200 `{"room": Room, "member": Member}` |
| `POST /invites/{code}/decline` | invitee | | 204 |

Invite responses include `url` (`invites.linkBaseUrl` + code).

Failures:

- Room is public: 409 for candidates and create.
- Create: inviting yourself 400; unknown peer 400; already a member 409;
  banned 409; over 30 invites in the last hour 429.
- Code of someone else's invite, or unknown code: 404.
- Accept, decline or revoke of an invite that is no longer pending (accepted,
  declined, revoked or expired): 409. Accepting while banned: 403.

## Documents

| Method and path | Who | Body | Success |
|---|---|---|---|
| `POST /rooms/{roomId}/documents` | member | `{"title", "positionRank"}` | 201 DocumentRef; caller becomes DOC_OWNER |
| `POST /rooms/{roomId}/documents/clone` | member | `{"sourceDocId", "title"?, "positionRank"}` | 201 DocumentRef (title defaults to "Copy of …") |
| `GET /rooms/{roomId}/documents` | member | | 200 DocumentRef[] in rank order |
| `GET /rooms/{roomId}/documents/{docId}` | member | | 200 DocumentRef |
| `PATCH /rooms/{roomId}/documents/{docId}` | document manager | `{"title"?, "positionRank"?}` | 200 DocumentRef |
| `DELETE /rooms/{roomId}/documents/{docId}` | document manager | | 204 |

The clone copies the reference only; the editor copies the content.

## Events

| Method and path | Who | Query | Success |
|---|---|---|---|
| `GET /rooms/{roomId}/events` | member | `after` (exclusive, default 0), `limit` (default 100, max 500) | 200 RoomEvent[] oldest first |
