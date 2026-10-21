package router

import "net/http"

// InviteHandler covers invitations to PRIVATE rooms. Any member may invite;
// an invite is bound to its invitee, and its code only works for them.
// Public rooms have no invites (the room endpoints answer 409).
type InviteHandler interface {
	Candidates(w http.ResponseWriter, r *http.Request)  // {roomId}, ?q=; people to invite, with their standing in the room
	Create(w http.ResponseWriter, r *http.Request)      // {roomId}; body {inviteePeerId}; emits INVITE_CREATED
	ListForRoom(w http.ResponseWriter, r *http.Request) // {roomId}; pending invites
	Revoke(w http.ResponseWriter, r *http.Request)      // {roomId}, {inviteId}; inviter or OWNER; emits INVITE_REVOKED

	Inbox(w http.ResponseWriter, r *http.Request)   // the caller's pending invites
	Get(w http.ResponseWriter, r *http.Request)     // {code}; preview, invitee only
	Accept(w http.ResponseWriter, r *http.Request)  // {code}; joins the room; emits ADD_MEMBER (via "invite")
	Decline(w http.ResponseWriter, r *http.Request) // {code}; emits INVITE_DECLINED
}

func registerInviteRoutes(api group, h InviteHandler) {
	api.handle("GET /rooms/{roomId}/invite-candidates", h.Candidates)
	api.handle("POST /rooms/{roomId}/invites", h.Create)
	api.handle("GET /rooms/{roomId}/invites", h.ListForRoom)
	api.handle("DELETE /rooms/{roomId}/invites/{inviteId}", h.Revoke)

	api.handle("GET /invites", h.Inbox)
	api.handle("GET /invites/{code}", h.Get)
	api.handle("POST /invites/{code}/accept", h.Accept)
	api.handle("POST /invites/{code}/decline", h.Decline)
}
