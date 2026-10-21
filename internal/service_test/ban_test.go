package service_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

func TestBanKeepsPeerOutOfPublicRoom(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.publicRoom(t, alice, "Open")
	if _, _, err := f.svc.Members.Join(f.ctx, bob, room.RoomID); err != nil {
		t.Fatal(err)
	}

	// Removing alone does not keep bob out of a public room.
	if err := f.svc.Members.Remove(f.ctx, alice, room.RoomID, bob); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Members.Join(f.ctx, bob, room.RoomID); err != nil {
		t.Fatalf("rejoin after removal: %v", err)
	}

	// Banning removes him and keeps him out.
	if err := f.svc.Members.Ban(f.ctx, alice, room.RoomID, bob); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Members.Get(f.ctx, alice, room.RoomID, bob)
	wantErr(t, err, service.ErrNotFound)
	_, _, err = f.svc.Members.Join(f.ctx, bob, room.RoomID)
	wantErr(t, err, service.ErrForbidden)

	bans, err := f.svc.Members.ListBans(f.ctx, alice, room.RoomID)
	if err != nil || len(bans) != 1 || bans[0].Peer.PeerID != bob || bans[0].BannedBy != alice {
		t.Fatalf("bans = %+v (err %v)", bans, err)
	}

	// Unbanning lets him back in.
	if err := f.svc.Members.Unban(f.ctx, alice, room.RoomID, bob); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Members.Join(f.ctx, bob, room.RoomID); err != nil {
		t.Errorf("join after unban: %v", err)
	}
	wantErr(t, f.svc.Members.Unban(f.ctx, alice, room.RoomID, bob), service.ErrNotFound)

	events, err := f.svc.Events.List(f.ctx, alice, room.RoomID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ban model.RoomEvent
	for _, e := range events {
		if e.EventType == model.EventBanMember {
			ban = e
		}
	}
	if ban.TargetID != bob || ban.Details["wasMember"] != true {
		t.Errorf("BAN_MEMBER event = %+v", ban)
	}
}

func TestBanInPrivateRoom(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Carol")
	room := f.room(t, alice, "Closed")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)
	pending := f.invite(t, bob, room.RoomID, carol)

	// Banning someone who is not a member yet revokes their pending invite.
	if err := f.svc.Members.Ban(f.ctx, alice, room.RoomID, carol); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.svc.Invites.Accept(f.ctx, carol, pending.Code)
	wantErr(t, err, service.ErrConflict)
	_, _, err = f.svc.Invites.Create(f.ctx, bob, room.RoomID, carol)
	wantErr(t, err, service.ErrConflict)
}

func TestBanRules(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Carol")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleOwner)
	f.addMember(t, alice, room.RoomID, carol, model.RoleParticipant)

	wantErr(t, f.svc.Members.Ban(f.ctx, alice, room.RoomID, alice), service.ErrValidation)      // yourself
	wantErr(t, f.svc.Members.Ban(f.ctx, alice, room.RoomID, bob), service.ErrConflict)          // an owner
	wantErr(t, f.svc.Members.Ban(f.ctx, carol, room.RoomID, bob), service.ErrForbidden)         // not an owner
	wantErr(t, f.svc.Members.Ban(f.ctx, alice, room.RoomID, uuid.New()), service.ErrValidation) // no such peer
	_, err := f.svc.Members.ListBans(f.ctx, carol, room.RoomID)
	wantErr(t, err, service.ErrForbidden)

	if err := f.svc.Members.Ban(f.ctx, alice, room.RoomID, carol); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.Members.Ban(f.ctx, alice, room.RoomID, carol), service.ErrConflict) // already banned

	// A failed ban changes nothing: bob is still an owner and a member.
	if m, err := f.svc.Members.Get(f.ctx, alice, room.RoomID, bob); err != nil || m.RoomRole != model.RoleOwner {
		t.Errorf("bob after failed ban = %+v (err %v)", m, err)
	}
}
