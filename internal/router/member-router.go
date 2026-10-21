package router

import "net/http"

// MemberHandler covers room membership and both kinds of role.
//
// Nobody is added directly: people join a PUBLIC room themselves (Join) or
// accept an invite to a PRIVATE one (see InviteHandler).
type MemberHandler interface {
	List(w http.ResponseWriter, r *http.Request)   // {roomId}
	Join(w http.ResponseWriter, r *http.Request)   // {roomId}; PUBLIC rooms only; emits ADD_MEMBER (via "join")
	Get(w http.ResponseWriter, r *http.Request)    // {roomId}, {peerId}
	Remove(w http.ResponseWriter, r *http.Request) // emits REMOVE_MEMBER; also how a member leaves (peerId = caller)

	UpdateRoomRole(w http.ResponseWriter, r *http.Request) // PUT; emits UPDATE_ROOM_ROLE
	UpdateDocRole(w http.ResponseWriter, r *http.Request)  // PUT; sets the role on one doc; emits UPDATE_DOC_ROLE

	ListBans(w http.ResponseWriter, r *http.Request) // OWNER only
	Ban(w http.ResponseWriter, r *http.Request)      // PUT {peerId}; removes them if a member; emits BAN_MEMBER
	Unban(w http.ResponseWriter, r *http.Request)    // DELETE {peerId}; emits UNBAN_MEMBER
}

func registerMemberRoutes(api group, h MemberHandler) {
	api.handle("GET /rooms/{roomId}/members", h.List)
	api.handle("POST /rooms/{roomId}/join", h.Join)
	api.handle("GET /rooms/{roomId}/members/{peerId}", h.Get)
	api.handle("DELETE /rooms/{roomId}/members/{peerId}", h.Remove)

	api.handle("PUT /rooms/{roomId}/members/{peerId}/room-role", h.UpdateRoomRole)
	api.handle("PUT /rooms/{roomId}/members/{peerId}/documents/{docId}/role", h.UpdateDocRole)

	api.handle("GET /rooms/{roomId}/bans", h.ListBans)
	api.handle("PUT /rooms/{roomId}/bans/{peerId}", h.Ban)
	api.handle("DELETE /rooms/{roomId}/bans/{peerId}", h.Unban)
}
