package service_test

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/dhanvin018/client-directory/internal/service"
)

// pkcePair returns a verifier and its S256 challenge, as a desktop app would.
func pkcePair(seed string) (verifier, challenge string) {
	verifier = strings.Repeat(seed, 43)[:43]
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

const loopback = "http://127.0.0.1:53682/callback"

func desktopRequest(challenge string) service.DesktopLoginRequest {
	return service.DesktopLoginRequest{
		RedirectURI:         loopback,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		AppState:            "app-state-123",
	}
}

func TestDesktopLoginRequestValidation(t *testing.T) {
	_, challenge := pkcePair("v")
	tests := []struct {
		name   string
		mutate func(*service.DesktopLoginRequest)
	}{
		{"https is not loopback", func(r *service.DesktopLoginRequest) { r.RedirectURI = "https://127.0.0.1:5000/cb" }},
		{"remote host", func(r *service.DesktopLoginRequest) { r.RedirectURI = "http://evil.example:5000/cb" }},
		{"localhost name", func(r *service.DesktopLoginRequest) { r.RedirectURI = "http://localhost:5000/cb" }},
		{"no port", func(r *service.DesktopLoginRequest) { r.RedirectURI = "http://127.0.0.1/cb" }},
		{"query", func(r *service.DesktopLoginRequest) { r.RedirectURI = loopback + "?x=1" }},
		{"custom scheme", func(r *service.DesktopLoginRequest) { r.RedirectURI = "collabdoc://callback" }},
		{"plain method", func(r *service.DesktopLoginRequest) { r.CodeChallengeMethod = "plain" }},
		{"short challenge", func(r *service.DesktopLoginRequest) { r.CodeChallenge = "abc" }},
		{"missing app state", func(r *service.DesktopLoginRequest) { r.AppState = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			req := desktopRequest(challenge)
			tt.mutate(&req)
			wantErr(t, f.svc.Desktop.Begin(f.ctx, "state", req), service.ErrValidation)
		})
	}

	// IPv6 loopback is fine too.
	f := newFixture(t)
	req := desktopRequest(challenge)
	req.RedirectURI = "http://[::1]:40000/cb"
	if err := f.svc.Desktop.Begin(f.ctx, "state", req); err != nil {
		t.Errorf("[::1] rejected: %v", err)
	}
}

func TestDesktopLoginFullCycle(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "Alice")
	verifier, challenge := pkcePair("v")

	if err := f.svc.Desktop.Begin(f.ctx, "provider-state", desktopRequest(challenge)); err != nil {
		t.Fatal(err)
	}
	login, err := f.svc.Desktop.Take(f.ctx, "provider-state")
	if err != nil {
		t.Fatal(err)
	}
	if login.AppState != "app-state-123" || login.RedirectURI != loopback {
		t.Fatalf("login = %+v", login)
	}
	_, err = f.svc.Desktop.Take(f.ctx, "provider-state")
	wantErr(t, err, service.ErrNotFound) // single use

	code, err := f.svc.Desktop.IssueCode(f.ctx, alice, *login)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) < 40 {
		t.Errorf("code %q is too short to be unguessable", code)
	}

	peer, err := f.svc.Desktop.Redeem(f.ctx, code, verifier, loopback)
	if err != nil || peer != alice {
		t.Fatalf("redeem = %v, %v; want alice", peer, err)
	}
	_, err = f.svc.Desktop.Redeem(f.ctx, code, verifier, loopback)
	wantErr(t, err, service.ErrValidation) // single use
}

func TestDesktopCodeRedemptionFailures(t *testing.T) {
	verifier, challenge := pkcePair("v")
	otherVerifier, _ := pkcePair("w")
	tests := []struct {
		name     string
		verifier string
		redirect string
		wait     time.Duration
	}{
		{"wrong verifier", otherVerifier, loopback, 0},
		{"malformed verifier", "short", loopback, 0},
		{"different redirect uri", verifier, "http://127.0.0.1:1/other", 0},
		{"expired", verifier, loopback, 61 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			alice := f.peer(t, "Alice")
			code, err := f.svc.Desktop.IssueCode(f.ctx, alice, service.DesktopLogin{RedirectURI: loopback, CodeChallenge: challenge})
			if err != nil {
				t.Fatal(err)
			}
			f.advance(tt.wait)
			_, err = f.svc.Desktop.Redeem(f.ctx, code, tt.verifier, tt.redirect)
			wantErr(t, err, service.ErrValidation)

			// A failed attempt spends the code (except a malformed verifier,
			// which is rejected before the code is looked up).
			if tt.name != "malformed verifier" {
				_, err = f.svc.Desktop.Redeem(f.ctx, code, verifier, loopback)
				wantErr(t, err, service.ErrValidation)
			}
		})
	}
}

func TestDesktopLoginExpires(t *testing.T) {
	f := newFixture(t)
	_, challenge := pkcePair("v")
	if err := f.svc.Desktop.Begin(f.ctx, "s", desktopRequest(challenge)); err != nil {
		t.Fatal(err)
	}
	f.advance(10*time.Minute + time.Second)
	_, err := f.svc.Desktop.Take(f.ctx, "s")
	wantErr(t, err, service.ErrNotFound)
}
