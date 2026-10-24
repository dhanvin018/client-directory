// Package repository implements the service layer's repository interfaces on
// PostgreSQL with database/sql.
//
// It works with any database/sql driver that speaks Postgres (pgx's stdlib
// adapter, lib/pq, ...); import the driver in main. The schema comes from the
// SQL files in db/, which scripts/migrate.sh applies, so nothing here creates tables.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dhanvin018/client-directory/internal/service"
)

// dbtx is the part of *sql.DB and *sql.Tx the repositories use, so the same
// code runs inside or outside a transaction.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

// repos implements service.Repos on one connection or transaction.
type repos struct{ q dbtx }

func (r repos) Peers() service.PeerRepository                 { return peerRepo{r.q} }
func (r repos) Rooms() service.RoomRepository                 { return roomRepo{r.q} }
func (r repos) Documents() service.DocumentRepository         { return documentRepo{r.q} }
func (r repos) Members() service.MemberRepository             { return memberRepo{r.q} }
func (r repos) Events() service.EventRepository               { return eventRepo{r.q} }
func (r repos) Invites() service.InviteRepository             { return inviteRepo{r.q} }
func (r repos) Bans() service.BanRepository                   { return banRepo{r.q} }
func (r repos) DesktopLogins() service.DesktopLoginRepository { return desktopLoginRepo{r.q} }
func (r repos) Sessions() service.SessionRepository           { return sessionRepo{r.q} }

// Store implements service.Store. Calls made directly on the Store run
// outside a transaction; WithTx binds them all to one.
type Store struct {
	repos
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{repos: repos{q: db}, db: db}
}

// WithTx runs fn in a transaction at the driver's default isolation level
// (read committed on Postgres). It commits if fn returns nil and rolls back
// if fn returns an error or panics. Writes to one room are serialized by the
// row lock that RoomRepository.LockForUpdate takes, not by the isolation level.
func (s *Store) WithTx(ctx context.Context, fn func(service.Repos) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(repos{q: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---- Error translation ----

// sqlStater is implemented by the Postgres error types of both common
// drivers (*pgconn.PgError and *pq.Error), so no driver import is needed.
type sqlStater interface{ SQLState() string }

const pgUniqueViolation = "23505"

// translate maps database errors to the service layer's sentinel errors:
// no row means ErrNotFound and a unique violation means ErrConflict.
// Anything else (including foreign-key and check violations, which signal a
// bug rather than a user error) is returned unchanged.
func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrNotFound
	}
	var st sqlStater
	if errors.As(err, &st) && st.SQLState() == pgUniqueViolation {
		return fmt.Errorf("%w: already exists", service.ErrConflict)
	}
	return err
}

// affectedOne turns "no rows affected" into ErrNotFound.
func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return translate(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return service.ErrNotFound
	}
	return nil
}

// collect scans every row with scan and closes rows.
func collect[T any](rows *sql.Rows, scan func(rowScanner) (*T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}
	return out, nil
}
