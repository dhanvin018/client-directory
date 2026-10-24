package service_test

import (
	"testing"
	"time"

	"github.com/dhanvin018/client-directory/internal/service"
)

func TestSessionRefreshRotates(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "alice")

	first, err := f.svc.Sessions.Start(f.ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if first.PeerID != alice || first.RefreshToken == "" || !first.ExpiresAt.Equal(f.now.Add(30*24*time.Hour)) {
		t.Fatalf("started session = %+v", first)
	}

	f.advance(time.Hour)
	second, err := f.svc.Sessions.Refresh(f.ctx, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != first.SessionID || second.RefreshToken == first.RefreshToken {
		t.Fatalf("refresh must keep the session and replace the token: %+v -> %+v", first, second)
	}
	if !second.ExpiresAt.Equal(f.now.Add(30 * 24 * time.Hour)) {
		t.Errorf("expiry %v did not move forward", second.ExpiresAt)
	}

	third, err := f.svc.Sessions.Refresh(f.ctx, second.RefreshToken)
	if err != nil || third.SessionID != first.SessionID {
		t.Fatalf("second refresh: %+v, %v", third, err)
	}
}

// A spent refresh token presented again means two parties hold it: the
// session ends, so the thief's copy and the user's copy both stop working.
func TestSessionRefreshReuseEndsTheSession(t *testing.T) {
	f := newFixture(t)
	sess, err := f.svc.Sessions.Start(f.ctx, f.peer(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.svc.Sessions.Refresh(f.ctx, sess.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.svc.Sessions.Refresh(f.ctx, sess.RefreshToken) // the spent one
	wantErr(t, err, service.ErrUnauthenticated)
	_, err = f.svc.Sessions.Refresh(f.ctx, next.RefreshToken)
	wantErr(t, err, service.ErrUnauthenticated)
}

func TestSessionExpires(t *testing.T) {
	f := newFixture(t)
	sess, err := f.svc.Sessions.Start(f.ctx, f.peer(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	f.advance(30*24*time.Hour + time.Second)
	_, err = f.svc.Sessions.Refresh(f.ctx, sess.RefreshToken)
	wantErr(t, err, service.ErrUnauthenticated)
}

func TestSessionBadRefreshTokens(t *testing.T) {
	f := newFixture(t)
	for _, tok := range []string{"", "not-a-token"} {
		_, err := f.svc.Sessions.Refresh(f.ctx, tok)
		wantErr(t, err, service.ErrUnauthenticated)
	}
}

func TestSessionEnd(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "alice")

	byToken, _ := f.svc.Sessions.Start(f.ctx, alice)
	if err := f.svc.Sessions.End(f.ctx, byToken.RefreshToken); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Sessions.Refresh(f.ctx, byToken.RefreshToken)
	wantErr(t, err, service.ErrUnauthenticated)

	byID, _ := f.svc.Sessions.Start(f.ctx, alice)
	if err := f.svc.Sessions.EndByID(f.ctx, byID.SessionID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Sessions.Refresh(f.ctx, byID.RefreshToken)
	wantErr(t, err, service.ErrUnauthenticated)

	// Logging out twice, or with an unknown token, is not an error.
	if err := f.svc.Sessions.End(f.ctx, byToken.RefreshToken); err != nil {
		t.Errorf("second End: %v", err)
	}
	if err := f.svc.Sessions.EndByID(f.ctx, byID.SessionID); err != nil {
		t.Errorf("second EndByID: %v", err)
	}
}

func TestSessionEndAll(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "alice"), f.peer(t, "bob")
	laptop, _ := f.svc.Sessions.Start(f.ctx, alice)
	desktop, _ := f.svc.Sessions.Start(f.ctx, alice)
	bobs, _ := f.svc.Sessions.Start(f.ctx, bob)

	if err := f.svc.Sessions.EndAll(f.ctx, alice); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*service.NewSession{laptop, desktop} {
		_, err := f.svc.Sessions.Refresh(f.ctx, s.RefreshToken)
		wantErr(t, err, service.ErrUnauthenticated)
	}
	if _, err := f.svc.Sessions.Refresh(f.ctx, bobs.RefreshToken); err != nil {
		t.Errorf("another peer's session ended: %v", err)
	}
}
