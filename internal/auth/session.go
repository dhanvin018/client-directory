package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/middleware"
)

// Both errors wrap middleware.ErrInvalidToken, which is the TokenVerifier
// contract: Authenticate answers them with 401. Any other error from Verify
// would be treated as an outage (503).
var (
	ErrInvalidToken = fmt.Errorf("%w: invalid or expired token", middleware.ErrInvalidToken)
	ErrTokenRevoked = fmt.Errorf("%w: token has been revoked", middleware.ErrInvalidToken)
)

// Claims defines the custom JWT claims containing the peer ID.
type Claims struct {
	PeerID uuid.UUID `json:"peer_id"`
	jwt.RegisteredClaims
}

// JWTSessionManager implements both SessionManager and middleware.TokenVerifier.
type JWTSessionManager struct {
	secretKey     []byte
	tokenDuration time.Duration

	// mu and revokedTokens provide a lightweight in-memory revocation list
	// for stateless JWTs. For multi-instance scaling, replace this with Redis/DB.
	mu            sync.RWMutex
	revokedTokens map[string]bool
}

// NewJWTSessionManager creates a new JWT-based session manager.
func NewJWTSessionManager(secretKey string, tokenDuration time.Duration) *JWTSessionManager {
	return &JWTSessionManager{
		secretKey:     []byte(secretKey),
		tokenDuration: tokenDuration,
		revokedTokens: make(map[string]bool),
	}
}

// Issue generates a signed JWT for the given peer ID.
func (m *JWTSessionManager) Issue(ctx context.Context, peerID uuid.UUID) (string, time.Time, error) {
	expiresAt := time.Now().Add(m.tokenDuration)

	// Generate a unique token ID (jti) to allow individual token revocation
	jti := uuid.New().String()

	claims := Claims{
		PeerID: peerID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Subject:   peerID.String(),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(m.secretKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to sign token: %w", err)
	}

	return signedToken, expiresAt, nil
}

// Verify validates the JWT and returns the associated PeerID.
func (m *JWTSessionManager) Verify(ctx context.Context, tokenString string) (uuid.UUID, error) {
	// Check if token is revoked
	m.mu.RLock()
	isRevoked := m.revokedTokens[tokenString]
	m.mu.RUnlock()

	if isRevoked {
		return uuid.Nil, ErrTokenRevoked
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		// Ensure signing method is HMAC
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secretKey, nil
	})

	if err != nil || !token.Valid {
		return uuid.Nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return uuid.Nil, ErrInvalidToken
	}

	return claims.PeerID, nil
}

// Revoke adds the token to the revocation list.
func (m *JWTSessionManager) Revoke(ctx context.Context, tokenString string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.revokedTokens[tokenString] = true
	return nil
}
