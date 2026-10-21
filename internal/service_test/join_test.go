package service_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

func TestDiscoverPublicRooms(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	f.publicRoom(t, alice, "Plans 2026")
	f.publicRoom(t, alice, "planning poker")
	f.room(t, alice, "Plans (secret)") // private: never discoverable
	f.publicRoom(t, alice, "Other")

	rooms, err := f.svc.Rooms.Discover(f.ctx, bob, "  PLAN ")
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 2 || rooms[0].Name != "planning poker" || rooms[1].Name != "Plans 2026" {
		t.Fatalf("rooms = %+v, want the two public rooms starting with \"plan\"", rooms)
	}
	if rooms[0].MemberCount != 1 || rooms[0].IsMember {
		t.Errorf("summary = %+v, want 1 member and bob not in it", rooms[0])
	}

	_, err = f.svc.Rooms.Discover(f.ctx, bob, "p")
	wantErr(t, err, service.ErrValidation)
}

func TestPreview(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	public := f.publicRoom(t, alice, "Open")
	private := f.room(t, alice, "Closed")

	sum, err := f.svc.Rooms.Preview(f.ctx, bob, public.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Name != "Open" || sum.IsMember {
		t.Errorf("preview = %+v", sum)
	}

	_, err = f.svc.Rooms.Preview(f.ctx, bob, private.RoomID)
	wantErr(t, err, service.ErrNotFound)
	if _, err := f.svc.Rooms.Preview(f.ctx, alice, private.RoomID); err != nil {
		t.Errorf("a member should preview their private room: %v", err)
	}
}

func TestJoinPublicRoom(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.publicRoom(t, alice, "Open")

	m, created, err := f.svc.Members.Join(f.ctx, bob, room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !created || m.RoomRole != model.RoleParticipant || m.InvitedBy != uuid.Nil {
		t.Errorf("member = %+v created=%v, want a new PARTICIPANT with no inviter", m, created)
	}

	// Joining again is a no-op.
	_, created, err = f.svc.Members.Join(f.ctx, bob, room.RoomID)
	if err != nil || created {
		t.Errorf("second join: created=%v err=%v, want a no-op", created, err)
	}

	events, err := f.svc.Events.List(f.ctx, bob, room.RoomID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if len(events) != 2 || last.EventType != model.EventAddMember || last.ActorID != bob || last.Details["via"] != "join" {
		t.Errorf("events = %+v, want ROOM_CREATED then ADD_MEMBER by bob via join", events)
	}
}

func TestJoinPrivateRoomIsHidden(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.room(t, alice, "Closed")

	_, _, err := f.svc.Members.Join(f.ctx, bob, room.RoomID)
	wantErr(t, err, service.ErrNotFound)
	_, _, err = f.svc.Members.Join(f.ctx, bob, uuid.New())
	wantErr(t, err, service.ErrNotFound)
}

func TestPublicRoomsHaveNoInvites(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.publicRoom(t, alice, "Open")

	_, _, err := f.svc.Invites.Create(f.ctx, alice, room.RoomID, bob)
	wantErr(t, err, service.ErrConflict)
	_, err = f.svc.Invites.Candidates(f.ctx, alice, room.RoomID, "bo")
	wantErr(t, err, service.ErrConflict)
}

func TestSwitchingToPublicRevokesInvites(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Carol")
	room := f.room(t, alice, "Room")
	toBob := f.invite(t, alice, room.RoomID, bob)
	f.invite(t, alice, room.RoomID, carol)

	if _, err := f.svc.Rooms.Update(f.ctx, alice, room.RoomID, service.UpdateRoomInput{Visibility: ptr(model.VisibilityPublic)}); err != nil {
		t.Fatal(err)
	}

	pending, err := f.svc.Invites.ListForRoom(f.ctx, alice, room.RoomID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending invites = %d (err %v), want 0", len(pending), err)
	}
	inv, err := f.svc.Invites.Get(f.ctx, bob, toBob.Code)
	if err != nil || inv.Status != model.InviteRevoked {
		t.Fatalf("bob's invite = %+v (err %v), want REVOKED", inv, err)
	}
	_, _, err = f.svc.Invites.Accept(f.ctx, bob, toBob.Code)
	wantErr(t, err, service.ErrConflict)

	// Bob joins the now-public room directly instead.
	if _, _, err := f.svc.Members.Join(f.ctx, bob, room.RoomID); err != nil {
		t.Fatal(err)
	}

	events, err := f.svc.Events.List(f.ctx, alice, room.RoomID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	revoked := 0
	for _, e := range events {
		if e.EventType == model.EventInviteRevoked && e.Details["reason"] == "room_public" {
			revoked++
		}
	}
	if revoked != 2 {
		t.Errorf("%d INVITE_REVOKED(room_public) events, want 2", revoked)
	}
}
