package repository_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

func TestMigrationsAreRecordedOnce(t *testing.T) {
	e := newEnv(t)
	applyMigrations(t, e.db, "../../db/migrations") // a second run must be a no-op
	var n, maxVersion int
	if err := e.db.QueryRow(`SELECT count(*), max(version) FROM schema_migrations`).Scan(&n, &maxVersion); err != nil {
		t.Fatal(err)
	}
	if n != maxVersion || n < 10 {
		t.Fatalf("schema_migrations has %d rows up to version %d, want one row per version", n, maxVersion)
	}
}

func TestHandlesInPostgres(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	a, _, err := e.svc.Peers.FindOrCreateByAuth(ctx, "google", "s1", "Ada Lovelace")
	mustNoErr(t, "first ada", err)
	b, _, err := e.svc.Peers.FindOrCreateByAuth(ctx, "google", "s2", "Ada Lovelace")
	mustNoErr(t, "second ada", err)
	if a.Handle != "ada.lovelace" || b.Handle == a.Handle || len(b.Handle) != len("ada.lovelace_0000") {
		t.Fatalf("handles %q and %q: want ada.lovelace and a suffixed one", a.Handle, b.Handle)
	}

	_, err = e.svc.Peers.UpdateProfile(ctx, b.PeerID, service.UpdateProfileInput{Handle: ptr("Ada.Lovelace")})
	wantErr(t, "taken handle", err, service.ErrConflict)
	got, err := e.svc.Peers.UpdateProfile(ctx, b.PeerID, service.UpdateProfileInput{Handle: ptr("countess")})
	mustNoErr(t, "free handle", err)
	if got.Handle != "countess" {
		t.Fatalf("handle = %q", got.Handle)
	}

	// The CHECK constraint backs up the service's validation.
	_, err = e.db.Exec(`UPDATE peers SET handle = 'Bad Handle' WHERE peer_id = $1`, a.PeerID)
	if err == nil {
		t.Fatal("database accepted an invalid handle")
	}
}

func TestInviteFlowInPostgres(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, bob := e.peer(t, "alice"), e.peer(t, "bob")
	room, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "Secret"})
	mustNoErr(t, "create room", err)
	rid := room.RoomID

	// "a_" must match the underscore literally, not as a LIKE wildcard.
	under, _, err := e.svc.Peers.FindOrCreateByAuth(ctx, "google", "u", "x")
	mustNoErr(t, "peer", err)
	_, err = e.svc.Peers.UpdateProfile(ctx, under.PeerID, service.UpdateProfileInput{Handle: ptr("a_b")})
	mustNoErr(t, "set handle", err)
	axb, _, err := e.svc.Peers.FindOrCreateByAuth(ctx, "google", "v", "x")
	mustNoErr(t, "peer", err)
	_, err = e.svc.Peers.UpdateProfile(ctx, axb.PeerID, service.UpdateProfileInput{Handle: ptr("axb")})
	mustNoErr(t, "set handle", err)
	cands, err := e.svc.Invites.Candidates(ctx, alice, rid, "a_")
	mustNoErr(t, "candidates", err)
	if len(cands) != 1 || cands[0].Handle != "a_b" {
		t.Fatalf("search \"a_\" = %+v, want only a_b", cands)
	}

	inv, created, err := e.svc.Invites.Create(ctx, alice, rid, bob)
	mustNoErr(t, "invite", err)
	if !created || inv.RoomName != "Secret" || inv.Inviter.Handle != "alice" || inv.Invitee.PeerID != bob {
		t.Fatalf("invite = %+v", inv)
	}
	cands, err = e.svc.Invites.Candidates(ctx, alice, rid, "bob")
	mustNoErr(t, "candidates", err)
	if len(cands) != 1 || cands[0].Status != model.CandidateInvited {
		t.Fatalf("bob's candidate status = %+v, want INVITED", cands)
	}

	inbox, err := e.svc.Invites.Inbox(ctx, bob)
	mustNoErr(t, "inbox", err)
	if len(inbox) != 1 || inbox[0].Inviter.UserName != "alice" || inbox[0].Code != inv.Code {
		t.Fatalf("inbox = %+v", inbox)
	}

	_, m, err := e.svc.Invites.Accept(ctx, bob, inv.Code)
	mustNoErr(t, "accept", err)
	if m.InvitedBy != alice || m.RoomRole != model.RoleParticipant {
		t.Fatalf("member = %+v", m)
	}
	got, err := e.svc.Invites.Get(ctx, bob, inv.Code)
	mustNoErr(t, "get", err)
	if got.Status != model.InviteAccepted || got.RespondedAt == nil {
		t.Fatalf("invite after accept = %+v", got)
	}

	events, err := e.svc.Events.List(ctx, alice, rid, 0, 100)
	mustNoErr(t, "events", err)
	last := events[len(events)-1]
	if last.EventType != model.EventAddMember || last.Details["via"] != "invite" || last.Details["inviteId"] != inv.InviteID.String() {
		t.Fatalf("last event = %+v", last)
	}
}

// Many tabs accepting the same invite at once: exactly one wins.
func TestConcurrentAcceptIsSingleUse(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, bob := e.peer(t, "alice"), e.peer(t, "bob")
	room, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "r"})
	mustNoErr(t, "create room", err)
	inv, _, err := e.svc.Invites.Create(ctx, alice, room.RoomID, bob)
	mustNoErr(t, "invite", err)

	const tries = 10
	var wg sync.WaitGroup
	errs := make(chan error, tries)
	for i := 0; i < tries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := e.svc.Invites.Accept(ctx, bob, inv.Code)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, service.ErrConflict):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d accepts succeeded, want exactly 1", ok)
	}
	if n := e.count(t, "members"); n != 2 {
		t.Fatalf("%d members, want 2", n)
	}
}

// Members inviting the same person at once share one invite.
func TestConcurrentInvitesShareOneRow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, carol := e.peer(t, "alice"), e.peer(t, "carol")
	room, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "r"})
	mustNoErr(t, "create room", err)

	const tries = 10
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, tries)
	for i := 0; i < tries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inv, _, err := e.svc.Invites.Create(ctx, alice, room.RoomID, carol)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- inv.InviteID
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[uuid.UUID]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 || e.count(t, "room_invites") != 1 {
		t.Fatalf("%d distinct invites, %d rows; want 1", len(seen), e.count(t, "room_invites"))
	}
}

func TestPublicRoomsAndBansInPostgres(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, bob, carol := e.peer(t, "alice"), e.peer(t, "bob"), e.peer(t, "carol")
	open, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "Open_Plans", Visibility: model.VisibilityPublic})
	mustNoErr(t, "public room", err)
	_, err = e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "OpenXPlans", Visibility: model.VisibilityPublic})
	mustNoErr(t, "public room", err)
	_, err = e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "Open_Secret"})
	mustNoErr(t, "private room", err)

	found, err := e.svc.Rooms.Discover(ctx, bob, "open_")
	mustNoErr(t, "discover", err)
	if len(found) != 1 || found[0].RoomID != open.RoomID || found[0].MemberCount != 1 || found[0].IsMember {
		t.Fatalf("discover = %+v, want only Open_Plans", found)
	}

	_, created, err := e.svc.Members.Join(ctx, bob, open.RoomID)
	mustNoErr(t, "join", err)
	if !created {
		t.Fatal("join did not create a membership")
	}
	sum, err := e.svc.Rooms.Preview(ctx, carol, open.RoomID)
	mustNoErr(t, "preview", err)
	if sum.MemberCount != 2 {
		t.Fatalf("member count = %d, want 2", sum.MemberCount)
	}

	mustNoErr(t, "ban bob", e.svc.Members.Ban(ctx, alice, open.RoomID, bob))
	_, _, err = e.svc.Members.Join(ctx, bob, open.RoomID)
	wantErr(t, "banned join", err, service.ErrForbidden)
	bans, err := e.svc.Members.ListBans(ctx, alice, open.RoomID)
	mustNoErr(t, "list bans", err)
	if len(bans) != 1 || bans[0].Peer.Handle != "bob" || bans[0].BannedBy != alice {
		t.Fatalf("bans = %+v", bans)
	}
	mustNoErr(t, "unban", e.svc.Members.Unban(ctx, alice, open.RoomID, bob))
	_, _, err = e.svc.Members.Join(ctx, bob, open.RoomID)
	mustNoErr(t, "rejoin", err)

	// Private -> public revokes pending invites in one statement.
	priv, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "Later public"})
	mustNoErr(t, "room", err)
	_, _, err = e.svc.Invites.Create(ctx, alice, priv.RoomID, bob)
	mustNoErr(t, "invite bob", err)
	_, _, err = e.svc.Invites.Create(ctx, alice, priv.RoomID, carol)
	mustNoErr(t, "invite carol", err)
	_, err = e.svc.Rooms.Update(ctx, alice, priv.RoomID, service.UpdateRoomInput{Visibility: ptr(model.VisibilityPublic)})
	mustNoErr(t, "make public", err)
	var pending int
	mustNoErr(t, "count", e.db.QueryRow(
		`SELECT count(*) FROM room_invites WHERE room_id = $1 AND status = 'PENDING'`, priv.RoomID).Scan(&pending))
	if pending != 0 {
		t.Fatalf("%d invites still pending after going public", pending)
	}

	// Deleting a room takes its bans and invites with it.
	mustNoErr(t, "ban carol", e.svc.Members.Ban(ctx, alice, priv.RoomID, carol))
	mustNoErr(t, "delete", e.svc.Rooms.Delete(ctx, alice, priv.RoomID))
	var left int
	mustNoErr(t, "count", e.db.QueryRow(
		`SELECT (SELECT count(*) FROM room_bans WHERE room_id = $1) + (SELECT count(*) FROM room_invites WHERE room_id = $1)`,
		priv.RoomID).Scan(&left))
	if left != 0 {
		t.Fatalf("%d ban/invite rows left after deleting the room", left)
	}
}
