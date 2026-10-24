// Package service holds the directory's business rules: authorization,
// validation, invariants, transactions and audit-event writing.
//
// Services depend only on the repository interfaces in repository.go, so the
// rules can be tested with fakes and the SQL repositories implement the same
// interfaces. Handlers translate the errors below into HTTP statuses:
//
//	ErrNotFound   -> 404
//	ErrForbidden  -> 403
//	ErrConflict   -> 409
//	ErrValidation -> 400 (or 422)
//	ErrRateLimited -> 429
//	ErrUnauthenticated -> 401
//
// Use errors.Is to test for them; the concrete errors carry a message.
package service

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrForbidden       = errors.New("forbidden")
	ErrConflict        = errors.New("conflict")
	ErrValidation      = errors.New("invalid input")
	ErrRateLimited     = errors.New("too many requests")
	ErrUnauthenticated = errors.New("unauthenticated")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

func conflict(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, args...))
}

func forbidden(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrForbidden, fmt.Sprintf(format, args...))
}
