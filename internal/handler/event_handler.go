package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dhanvin018/client-directory/internal/service"
)

type EventHandler struct {
	base
	svc *service.EventService
}

func NewEventHandler(svc *service.EventService, log *slog.Logger) *EventHandler {
	return &EventHandler{base: newBase(log), svc: svc}
}

// List returns a room's events, oldest first.
// Query: after={eventId} (exclusive, default 0), limit={n} (service caps it).
func (h *EventHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}

	q := r.URL.Query()
	var after uint64
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "after must be a non-negative integer")
			return
		}
		after = n
	}
	var limit int
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}

	events, err := h.svc.List(r.Context(), actor, roomID, after, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(events))
}
