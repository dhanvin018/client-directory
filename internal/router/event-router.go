package router

import "net/http"

// EventHandler exposes the append-only room event log. Read-only: events are
// written by the service layer as a side effect of the mutating routes.
type EventHandler interface {
	// List returns events for {roomId} ordered by event_id.
	// Query: after={eventId} (exclusive, default 0) and limit={n}, so clients
	// can sync incrementally: "give me everything after the last event I saw".
	List(w http.ResponseWriter, r *http.Request)
}

func registerEventRoutes(api group, h EventHandler) {
	api.handle("GET /rooms/{roomId}/events", h.List)
}
