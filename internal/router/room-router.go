package router

import "net/http"

// RoomHandler covers Room CRUD.
//
// Authorization (e.g. only a room OWNER may rename or delete) belongs in the
// service layer; the router only guarantees the caller is authenticated.
type RoomHandler interface {
	Create(w http.ResponseWriter, r *http.Request) // emits ROOM_CREATED; caller becomes OWNER
	List(w http.ResponseWriter, r *http.Request)   // rooms the caller is a member of
	Get(w http.ResponseWriter, r *http.Request)    // {roomId}
	Update(w http.ResponseWriter, r *http.Request) // {roomId}; name (RENAME_ROOM), visibility, defaultDocRole
	Delete(w http.ResponseWriter, r *http.Request) // {roomId}; cascades docs, members, events, invites and bans

	Discover(w http.ResponseWriter, r *http.Request) // ?q=; search PUBLIC rooms by name, for anyone signed in
	Preview(w http.ResponseWriter, r *http.Request)  // {roomId}; summary of a public room before joining
}

func registerRoomRoutes(api group, h RoomHandler) {
	api.handle("POST /rooms", h.Create)
	api.handle("GET /rooms", h.List)
	api.handle("GET /rooms/discover", h.Discover) // literal beats {roomId} in ServeMux precedence
	api.handle("GET /rooms/{roomId}", h.Get)
	api.handle("PATCH /rooms/{roomId}", h.Update)
	api.handle("DELETE /rooms/{roomId}", h.Delete)
	api.handle("GET /rooms/{roomId}/preview", h.Preview)
}
