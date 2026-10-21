package service_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

func TestInviteCandidates(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "Alice")
	room := f.room(t, alice, "Room")
	member, invited, banned, free := f.peer(t, "Sam Member"), f.peer(t, "Sam Invited"), f.peer(t, "Sam Banned"), f.peer(t, "Sam Free")
	f.addMember(t, alice, room.RoomID, member, model.RoleParticipant)
	f.invite(t, alice, room.RoomID, invited)
	if err := f.svc.Members.Ban(f.ctx, alice, room.RoomID, banned); err != nil {
		t.Fatal(err)
	}

	got, err := f.svc.Invites.Candidates(f.ctx, alice, room.RoomID, "@SAM")
	if err != nil {
		t.Fatal(err)
	}
	status := map[uuid.UUID]model.CandidateStatus{}
	for _, c := range got {
		status[c.PeerID] = c.Status
	}
	want := map[uuid.UUID]model.CandidateStatus{
		member:  model.CandidateMember,
		invited: model.CandidateInvited,
		banned:  model.CandidateBanned,
		free:    model.CandidateAvailable,
	}
	if fmt.Sprint(status) != fmt.Sprint(want) {
		t.Errorf("statuses = %v, want %v", status, want)
	}

	// Display names match too, not just handles.
	if got, _ := f.svc.Invites.Candidates(f.ctx, alice, room.RoomID, "sam f"); len(got) != 1 || got[0].PeerID != free {
		t.Errorf("display-name search = %+v, want just Sam Free", got)
	}

	_, err = f.svc.Invites.Candidates(f.ctx, alice, room.RoomID, "s")
	wantErr(t, err, service.ErrValidation)
	_, err = f.svc.Invites.Candidates(f.ctx, free, room.RoomID, "sam")
	wantErr(t, err, service.ErrNotFound) // outsiders cannot search on a room's behalf
}

func TestInviteAndAccept(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Carol")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)

	// Any member can invite, not just owners.
	inv, created, err := f.svc.Invites.Create(f.ctx, bob, room.RoomID, carol)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	if inv.Status != model.InvitePending || inv.RoomName != "Room" || inv.Inviter.PeerID != bob ||
		inv.Invitee.PeerID != carol || inv.RoomRole != model.RoleParticipant || len(inv.Code) < 20 {
		t.Errorf("invite = %+v", inv)
	}
	if want := f.now.Add(7 * 24 * time.Hour); !inv.ExpiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", inv.ExpiresAt, want)
	}

	// Inviting again returns the same live invite.
	again, created, err := f.svc.Invites.Create(f.ctx, alice, room.RoomID, carol)
	if err != nil || created || again.InviteID != inv.InviteID {
		t.Errorf("re-invite: created=%v err=%v id match=%v, want the existing invite", created, err, again != nil && again.InviteID == inv.InviteID)
	}

	// It shows up in Carol's inbox, and only Carol can open it.
	inbox, err := f.svc.Invites.Inbox(f.ctx, carol)
	if err != nil || len(inbox) != 1 || inbox[0].Code != inv.Code {
		t.Fatalf("inbox = %+v (err %v)", inbox, err)
	}
	_, err = f.svc.Invites.Get(f.ctx, bob, inv.Code)
	wantErr(t, err, service.ErrNotFound)
	_, _, err = f.svc.Invites.Accept(f.ctx, bob, inv.Code)
	wantErr(t, err, service.ErrNotFound)
	_, _, err = f.svc.Invites.Accept(f.ctx, carol, "no-such-code")
	wantErr(t, err, service.ErrNotFound)

	gotRoom, m, err := f.svc.Invites.Accept(f.ctx, carol, inv.Code)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoom.RoomID != room.RoomID || m.RoomRole != model.RoleParticipant || m.InvitedBy != bob {
		t.Errorf("accept: room=%v member=%+v, want participant invited by bob", gotRoom.RoomID, m)
	}

	// Single use.
	_, _, err = f.svc.Invites.Accept(f.ctx, carol, inv.Code)
	wantErr(t, err, service.ErrConflict)
	if inbox, _ := f.svc.Invites.Inbox(f.ctx, carol); len(inbox) != 0 {
		t.Errorf("inbox still has %d invites after accepting", len(inbox))
	}
	if got, _ := f.svc.Invites.Get(f.ctx, carol, inv.Code); got.Status != model.InviteAccepted || got.RespondedAt == nil {
		t.Errorf("invite after accept = %+v", got)
	}

	events, err := f.svc.Events.List(f.ctx, alice, room.RoomID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != model.EventAddMember || last.ActorID != carol || last.TargetID != carol ||
		last.Details["via"] != "invite" || last.Details["inviteId"] != inv.InviteID.String() {
		t.Errorf("last event = %+v, want ADD_MEMBER by carol via invite", last)
	}
}

func TestInviteRules(t *testing.T) {
	f := newFixture(t)
	alice, bob, eve := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Eve")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)

	_, _, err := f.svc.Invites.Create(f.ctx, alice, room.RoomID, alice)
	wantErr(t, err, service.ErrValidation) // yourself
	_, _, err = f.svc.Invites.Create(f.ctx, alice, room.RoomID, bob)
	wantErr(t, err, service.ErrConflict) // already a member
	_, _, err = f.svc.Invites.Create(f.ctx, alice, room.RoomID, uuid.New())
	wantErr(t, err, service.ErrValidation) // no such peer
	_, _, err = f.svc.Invites.Create(f.ctx, eve, room.RoomID, eve)
	wantErr(t, err, service.ErrValidation)
	carol := f.peer(t, "Carol")
	_, _, err = f.svc.Invites.Create(f.ctx, eve, room.RoomID, carol)
	wantErr(t, err, service.ErrNotFound) // outsiders cannot invite
}

func TestInviteDecline(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.room(t, alice, "Room")
	inv := f.invite(t, alice, room.RoomID, bob)

	wantErr(t, f.svc.Invites.Decline(f.ctx, alice, inv.Code), service.ErrNotFound) // not the invitee
	if err := f.svc.Invites.Decline(f.ctx, bob, inv.Code); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.svc.Invites.Accept(f.ctx, bob, inv.Code)
	wantErr(t, err, service.ErrConflict)

	// A declined invite does not block a fresh one.
	if _, created, err := f.svc.Invites.Create(f.ctx, alice, room.RoomID, bob); err != nil || !created {
		t.Errorf("re-invite after decline: created=%v err=%v", created, err)
	}
	if got := f.eventTypes(t, alice, room.RoomID); fmt.Sprint(got) != fmt.Sprint([]model.RoomEventType{
		model.EventRoomCreated, model.EventInviteCreated, model.EventInviteDeclined, model.EventInviteCreated,
	}) {
		t.Errorf("events = %v", got)
	}
}

func TestInviteRevoke(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol, dave := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Carol"), f.peer(t, "Dave")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)
	f.addMember(t, alice, room.RoomID, carol, model.RoleParticipant)

	byBob := f.invite(t, bob, room.RoomID, dave)
	wantErr(t, f.svc.Invites.Revoke(f.ctx, carol, room.RoomID, byBob.InviteID), service.ErrForbidden)
	if err := f.svc.Invites.Revoke(f.ctx, bob, room.RoomID, byBob.InviteID); err != nil {
		t.Errorf("inviter revoke: %v", err)
	}
	wantErr(t, f.svc.Invites.Revoke(f.ctx, bob, room.RoomID, byBob.InviteID), service.ErrConflict) // already revoked

	byBob = f.invite(t, bob, room.RoomID, dave)
	if err := f.svc.Invites.Revoke(f.ctx, alice, room.RoomID, byBob.InviteID); err != nil {
		t.Errorf("owner revoke: %v", err)
	}
	_, _, err := f.svc.Invites.Accept(f.ctx, dave, byBob.Code)
	wantErr(t, err, service.ErrConflict)
	wantErr(t, f.svc.Invites.Revoke(f.ctx, alice, room.RoomID, uuid.New()), service.ErrNotFound)
}

func TestInviteExpiry(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.room(t, alice, "Room")
	old := f.invite(t, alice, room.RoomID, bob)

	f.advance(7*24*time.Hour + time.Second)

	got, err := f.svc.Invites.Get(f.ctx, bob, old.Code)
	if err != nil || got.Status != model.InviteExpired {
		t.Fatalf("invite = %+v (err %v), want EXPIRED", got, err)
	}
	_, _, err = f.svc.Invites.Accept(f.ctx, bob, old.Code)
	wantErr(t, err, service.ErrConflict)
	if inbox, _ := f.svc.Invites.Inbox(f.ctx, bob); len(inbox) != 0 {
		t.Errorf("expired invite still in the inbox")
	}
	if c, _ := f.svc.Invites.Candidates(f.ctx, alice, room.RoomID, "bob"); len(c) != 1 || c[0].Status != model.CandidateAvailable {
		t.Errorf("candidates = %+v, want bob AVAILABLE again", c)
	}

	// A new invite replaces the expired one.
	fresh, created, err := f.svc.Invites.Create(f.ctx, alice, room.RoomID, bob)
	if err != nil || !created || fresh.InviteID == old.InviteID {
		t.Fatalf("re-invite: created=%v err=%v", created, err)
	}
	if _, _, err := f.svc.Invites.Accept(f.ctx, bob, fresh.Code); err != nil {
		t.Errorf("accept fresh invite: %v", err)
	}
}

func TestInviteRateLimit(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "Alice")
	room := f.room(t, alice, "Room")
	invitees := make([]uuid.UUID, 31)
	for i := range invitees {
		invitees[i] = f.peer(t, fmt.Sprintf("Guest %02d", i))
	}
	for _, p := range invitees[:30] {
		f.invite(t, alice, room.RoomID, p)
	}
	_, _, err := f.svc.Invites.Create(f.ctx, alice, room.RoomID, invitees[30])
	wantErr(t, err, service.ErrRateLimited)

	// Re-sending an existing invite is not a new invite, so it is allowed.
	if _, created, err := f.svc.Invites.Create(f.ctx, alice, room.RoomID, invitees[0]); err != nil || created {
		t.Errorf("re-invite at the limit: created=%v err=%v", created, err)
	}

	f.advance(time.Hour + time.Second) // the window includes invites exactly an hour old
	if _, _, err := f.svc.Invites.Create(f.ctx, alice, room.RoomID, invitees[30]); err != nil {
		t.Errorf("an hour later: %v", err)
	}
}
