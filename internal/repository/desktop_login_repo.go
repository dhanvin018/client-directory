package repository

import (
	"context"
	"time"

	"github.com/dhanvin018/client-directory/internal/service"
)

type desktopLoginRepo struct{ q dbtx }

func (r desktopLoginRepo) SaveLogin(ctx context.Context, l service.DesktopLogin) error {
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO desktop_logins (state, redirect_uri, code_challenge, app_state, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		l.State, l.RedirectURI, l.CodeChallenge, l.AppState, l.ExpiresAt)
	return translate(err)
}

// TakeLogin deletes and returns in one statement, so a state is used once.
func (r desktopLoginRepo) TakeLogin(ctx context.Context, state string, now time.Time) (*service.DesktopLogin, error) {
	var l service.DesktopLogin
	err := r.q.QueryRowContext(ctx,
		`DELETE FROM desktop_logins WHERE state = $1 AND expires_at > $2
		 RETURNING state, redirect_uri, code_challenge, app_state, expires_at`,
		state, now).Scan(&l.State, &l.RedirectURI, &l.CodeChallenge, &l.AppState, &l.ExpiresAt)
	if err != nil {
		return nil, translate(err)
	}
	l.ExpiresAt = l.ExpiresAt.UTC()
	return &l, nil
}

func (r desktopLoginRepo) SaveCode(ctx context.Context, c service.DesktopAuthCode) error {
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO desktop_auth_codes (code_hash, peer_id, redirect_uri, code_challenge, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		c.CodeHash, c.PeerID, c.RedirectURI, c.CodeChallenge, c.ExpiresAt)
	return translate(err)
}

// TakeCode deletes and returns in one statement: of two concurrent
// redemptions of the same code, exactly one gets the row.
func (r desktopLoginRepo) TakeCode(ctx context.Context, codeHash []byte, now time.Time) (*service.DesktopAuthCode, error) {
	var c service.DesktopAuthCode
	err := r.q.QueryRowContext(ctx,
		`DELETE FROM desktop_auth_codes WHERE code_hash = $1 AND expires_at > $2
		 RETURNING code_hash, peer_id, redirect_uri, code_challenge, expires_at`,
		codeHash, now).Scan(&c.CodeHash, &c.PeerID, &c.RedirectURI, &c.CodeChallenge, &c.ExpiresAt)
	if err != nil {
		return nil, translate(err)
	}
	c.ExpiresAt = c.ExpiresAt.UTC()
	return &c, nil
}

func (r desktopLoginRepo) DeleteExpired(ctx context.Context, now time.Time) error {
	_, err := r.q.ExecContext(ctx,
		`WITH a AS (DELETE FROM desktop_logins WHERE expires_at <= $1)
		 DELETE FROM desktop_auth_codes WHERE expires_at <= $1`, now)
	return translate(err)
}
