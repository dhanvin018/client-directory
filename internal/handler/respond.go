// Package handler implements the HTTP handlers the router dispatches to.
//
// Handlers only translate: they parse the request, call a service with the
// authenticated peer's ID, and map the result or error to HTTP. Business
// rules live in package service.
//
// Responses encode the model structs directly, so the models need camelCase
// json tags. AuthProvider and AuthSubject on model.Peer must be tagged
// `json:"-"`, otherwise the external identity leaks to other members.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/middleware"
	"github.com/dhanvin018/client-directory/internal/service"
)

const (
	maxBodyBytes = 1 << 20 // 1 MiB

	// statusClientClosedRequest is the de-facto (nginx) code for a client that
	// disconnected before the response; it only shows up in logs.
	statusClientClosedRequest = 499
)

// base is embedded by every handler.
type base struct{ log *slog.Logger }

func newBase(log *slog.Logger) base {
	if log == nil {
		log = slog.Default()
	}
	return base{log: log}
}

// fail maps a service error to an HTTP response. Unexpected errors are logged
// with the request ID and answered with a generic 500, so internals never leak.
func (b base) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, service.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, service.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, context.Canceled):
		writeError(w, statusClientClosedRequest, "request cancelled")
	default:
		b.log.ErrorContext(r.Context(), "unhandled error",
			"error", err,
			"method", r.Method,
			"path", r.URL.Path,
			"request_id", middleware.RequestIDFromContext(r.Context()))
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// ---- Responses ----

// writeJSON marshals first, so an encoding failure becomes a clean 500
// instead of a half-written 200.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func noContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// nonNil makes empty lists encode as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ---- Requests ----

// decodeJSON reads a single JSON object into dst. Unknown fields are
// rejected, so a typo in a field name is an error instead of being ignored.
// It writes the error response itself and reports whether decoding worked.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "request body is required")
		default:
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		}
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "request body must contain a single JSON object")
		return false
	}
	return true
}

// actorID returns the authenticated peer. The router guarantees it on
// protected routes; the 401 only fires if a route was wired without Authenticate.
func actorID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := middleware.PeerIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return uuid.Nil, false
	}
	return id, true
}

// pathUUID parses a path parameter such as {roomId}.
func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	return parseUUID(w, name, r.PathValue(name))
}

// parseUUID parses an ID taken from the path or a request body.
func parseUUID(w http.ResponseWriter, field, value string) (uuid.UUID, bool) {
	id, err := uuid.Parse(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, field+" must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}
