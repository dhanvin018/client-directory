package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/middleware"
)

// ErrInvalidToken wraps middleware.ErrInvalidToken, which is the
// TokenVerifier contract: Authenticate answers it with 401. Any other error
// from Verify would be treated as an outage (503).
var ErrInvalidToken = fmt.Errorf("%w: invalid or expired token", middleware.ErrInvalidToken)

// Claims are the access token's claims: the peer and the session it belongs to.
type Claims struct {
	PeerID    uuid.UUID `json:"peer_id"`
	SessionID uuid.UUID `json:"sid"`
	jwt.RegisteredClaims
}

// AccessTokens signs and checks short-lived access tokens (HS256 JWTs).
//
// Verify needs no database: that is the point of a JWT. The price is that a
// token cannot be withdrawn, so it is kept short-lived; logout ends the
// session in the database (service.SessionService), after which no new
// access tokens are issued for it. It implements middleware.TokenVerifier.
type AccessTokens struct {
	secretKey []byte
	ttl       time.Duration
	now       func() time.Time
}

func NewAccessTokens(secretKey string, ttl time.Duration) *AccessTokens {
	return &AccessTokens{secretKey: []byte(secretKey), ttl: ttl, now: time.Now}
}

// Issue signs an access token for a peer's session.
func (m *AccessTokens) Issue(ctx context.Context, peerID, sessionID uuid.UUID) (string, time.Time, error) {
	now := m.now()
	expiresAt := now.Add(m.ttl)
	claims := Claims{
		PeerID:    peerID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   peerID.String(),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secretKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to sign token: %w", err)
	}
	return signed, expiresAt, nil
}

// Verify checks the signature and expiry and returns the peer.
func (m *AccessTokens) Verify(ctx context.Context, token string) (uuid.UUID, error) {
	c, err := m.parse(token)
	if err != nil {
		return uuid.Nil, err
	}
	return c.PeerID, nil
}

// SessionID returns the session a token belongs to. It checks the signature
// but not the expiry, so a client can still log out with an expired token.
func (m *AccessTokens) SessionID(ctx context.Context, token string) (uuid.UUID, error) {
	c, err := m.parse(token, jwt.WithoutClaimsValidation())
	if err != nil {
		return uuid.Nil, err
	}
	return c.SessionID, nil
}

func (m *AccessTokens) parse(token string, opts ...jwt.ParserOption) (*Claims, error) {
	opts = append(opts, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithTimeFunc(m.now))
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return m.secretKey, nil }, opts...)
	if err != nil {
		return nil, errors.Join(ErrInvalidToken, err)
	}
	// Every token names a session; one that does not predates sessions.
	if c.PeerID == uuid.Nil || c.SessionID == uuid.Nil {
		return nil, ErrInvalidToken
	}
	return &c, nil
}
