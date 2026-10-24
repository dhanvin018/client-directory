package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// DesktopLoginService implements sign-in for desktop apps: OAuth 2.0 for
// native apps (RFC 8252) with PKCE (RFC 7636).
//
// The browser flow ends with a session cookie, which a desktop app cannot
// receive. Instead:
//
//  1. The app opens the system browser at /auth/{provider}/login with a
//     loopback redirect_uri (its own listener on 127.0.0.1), a PKCE
//     code_challenge and its own state. Begin records that.
//  2. After the provider, Take finds the pending login and IssueCode makes a
//     one-time code, which the browser delivers to the loopback URI.
//  3. The app posts code + code_verifier to /auth/token; Redeem checks them
//     and the handler issues a session token.
//
// The provider's client secret never leaves the server, and PKCE means a code
// intercepted on the way to the loopback listener is useless without the
// verifier that only the app holds.
type DesktopLoginService struct{ base }

const (
	desktopLoginTTL = 10 * time.Minute // time to finish at the provider
	desktopCodeTTL  = 60 * time.Second // time for the app to redeem the code
	maxAppStateLen  = 512
)

var (
	// RFC 7636: the S256 challenge is base64url(SHA-256(verifier)) without
	// padding, always 43 characters; the verifier is 43-128 unreserved characters.
	codeChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	codeVerifierPattern  = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

	errInvalidGrant = invalid("invalid, expired or already used authorization code")
)

// DesktopLoginRequest is what the app sends when it starts sign-in.
type DesktopLoginRequest struct {
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	AppState            string
}

// Begin validates a desktop sign-in request and records it under state, the
// value the directory sends to the OAuth provider.
func (s *DesktopLoginService) Begin(ctx context.Context, state string, in DesktopLoginRequest) error {
	redirect, err := cleanLoopbackRedirect(in.RedirectURI)
	if err != nil {
		return err
	}
	if in.CodeChallengeMethod != "S256" {
		return invalid("code_challenge_method must be S256")
	}
	if !codeChallengePattern.MatchString(in.CodeChallenge) {
		return invalid("code_challenge must be 43 base64url characters (S256)")
	}
	if in.AppState == "" || len(in.AppState) > maxAppStateLen {
		return invalid("state is required and at most %d characters", maxAppStateLen)
	}

	now := s.now()
	if err := s.store.DesktopLogins().DeleteExpired(ctx, now); err != nil {
		return err
	}
	return s.store.DesktopLogins().SaveLogin(ctx, DesktopLogin{
		State:         state,
		RedirectURI:   redirect,
		CodeChallenge: in.CodeChallenge,
		AppState:      in.AppState,
		ExpiresAt:     now.Add(desktopLoginTTL),
	})
}

// Take returns and consumes the desktop login started under state.
// ErrNotFound means the sign-in is not a desktop one (or it expired).
func (s *DesktopLoginService) Take(ctx context.Context, state string) (*DesktopLogin, error) {
	return s.store.DesktopLogins().TakeLogin(ctx, state, s.now())
}

// IssueCode creates the one-time code for a finished desktop sign-in.
func (s *DesktopLoginService) IssueCode(ctx context.Context, peerID uuid.UUID, login DesktopLogin) (string, error) {
	code, err := randomURLToken(32)
	if err != nil {
		return "", err
	}
	err = s.store.DesktopLogins().SaveCode(ctx, DesktopAuthCode{
		CodeHash:      hashCode(code),
		PeerID:        peerID,
		RedirectURI:   login.RedirectURI,
		CodeChallenge: login.CodeChallenge,
		ExpiresAt:     s.now().Add(desktopCodeTTL),
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// Redeem consumes a code and returns the peer it was issued for. The code is
// spent even when the verifier or redirect URI is wrong, so it cannot be
// guessed against. Every failure is the same ErrValidation.
func (s *DesktopLoginService) Redeem(ctx context.Context, code, verifier, redirectURI string) (uuid.UUID, error) {
	if code == "" || !codeVerifierPattern.MatchString(verifier) {
		return uuid.Nil, errInvalidGrant
	}
	c, err := s.store.DesktopLogins().TakeCode(ctx, hashCode(code), s.now())
	if errors.Is(err, ErrNotFound) {
		return uuid.Nil, errInvalidGrant
	}
	if err != nil {
		return uuid.Nil, err
	}
	if c.RedirectURI != redirectURI {
		return uuid.Nil, errInvalidGrant
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(c.CodeChallenge)) != 1 {
		return uuid.Nil, errInvalidGrant
	}
	return c.PeerID, nil
}

// cleanLoopbackRedirect accepts only http://127.0.0.1:<port>/... or
// http://[::1]:<port>/..., as RFC 8252 section 7.3 recommends. Because it can
// only point at the user's own machine, it cannot be abused as an open
// redirect. "localhost" is rejected: it can resolve elsewhere (section 8.3).
func cleanLoopbackRedirect(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" {
		return "", invalid("redirect_uri must be http://127.0.0.1:<port>/<path> without query or fragment")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", invalid("redirect_uri must use a loopback IP literal (127.0.0.1 or [::1])")
	}
	return u.String(), nil
}

func hashCode(code string) []byte {
	sum := sha256.Sum256([]byte(code))
	return sum[:]
}

// randomURLToken returns n random bytes as unpadded base64url.
func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
