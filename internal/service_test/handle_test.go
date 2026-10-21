package service_test

import (
	"regexp"
	"testing"

	"github.com/dhanvin018/client-directory/internal/service"
)

func TestHandleGeneratedOnSignUp(t *testing.T) {
	suffixed := regexp.MustCompile(`^ada\.lovelace_\d{4}$`)
	tests := []struct {
		name    string
		display string
		want    *regexp.Regexp
	}{
		{"spaces become dots", "Ada Lovelace", regexp.MustCompile(`^ada\.lovelace$`)},
		{"punctuation dropped", "  O'Brien-Smith!! ", regexp.MustCompile(`^obriensmith$`)},
		{"no usable letters", "李雷", regexp.MustCompile(`^user_\d{4}$`)},
		{"long names truncated", "Abcdefghij Klmnopqrst Uvwxyz Extra", regexp.MustCompile(`^[a-z.]{2,24}$`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			p, _, err := f.svc.Peers.FindOrCreateByAuth(f.ctx, "google", "sub", tt.display)
			if err != nil {
				t.Fatal(err)
			}
			if !tt.want.MatchString(p.Handle) {
				t.Errorf("handle = %q, want match for %s", p.Handle, tt.want)
			}
		})
	}

	// A second person with the same name gets a suffixed handle.
	f := newFixture(t)
	first, _, err := f.svc.Peers.FindOrCreateByAuth(f.ctx, "google", "sub-1", "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := f.svc.Peers.FindOrCreateByAuth(f.ctx, "google", "sub-2", "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}
	if first.Handle != "ada.lovelace" || !suffixed.MatchString(second.Handle) {
		t.Errorf("handles = %q, %q; want ada.lovelace and ada.lovelace_NNNN", first.Handle, second.Handle)
	}
}

func TestUpdateProfile(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")

	p, err := f.svc.Peers.UpdateProfile(f.ctx, alice, service.UpdateProfileInput{Handle: ptr("  @Ada.L ")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Handle != "ada.l" || p.UserName != "Alice" {
		t.Errorf("peer = %+v, want handle ada.l and name unchanged", p)
	}

	p, err = f.svc.Peers.UpdateProfile(f.ctx, alice, service.UpdateProfileInput{UserName: ptr("Ada"), Handle: ptr("ada")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Handle != "ada" || p.UserName != "Ada" {
		t.Errorf("peer = %+v, want both fields changed", p)
	}

	// Taken by someone else.
	_, err = f.svc.Peers.UpdateProfile(f.ctx, bob, service.UpdateProfileInput{Handle: ptr("ADA")})
	wantErr(t, err, service.ErrConflict)

	// A failed handle change rolls back the name change in the same request.
	_, err = f.svc.Peers.UpdateProfile(f.ctx, bob, service.UpdateProfileInput{UserName: ptr("Robert"), Handle: ptr("ada")})
	wantErr(t, err, service.ErrConflict)
	if got, _ := f.svc.Peers.Get(f.ctx, bob); got.UserName != "Bob" {
		t.Errorf("name changed to %q despite the failed update", got.UserName)
	}

	for _, bad := range []string{"a", "has space", "émile", "way_too_long_for_a_handle_really_yes"} {
		_, err = f.svc.Peers.UpdateProfile(f.ctx, bob, service.UpdateProfileInput{Handle: ptr(bad)})
		wantErr(t, err, service.ErrValidation)
	}
	_, err = f.svc.Peers.UpdateProfile(f.ctx, bob, service.UpdateProfileInput{})
	wantErr(t, err, service.ErrValidation)
}
