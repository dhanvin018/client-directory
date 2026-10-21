package service_test

// Unit tests for the service layer's business rules, run against the
// in-memory store in memstore_test.go.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

// =========================================================================
// Helpers
// =========================================================================

type fixture struct {
	ctx   context.Context
	store *memStore
	svc   *service.Services
	now   time.Time // the services' clock; move it with advance
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{ctx: context.Background(), store: newMemStore(), now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	f.svc = service.New(f.store, service.WithClock(func() time.Time { return f.now }))
	return f
}

func (f *fixture) advance(d time.Duration) { f.now = f.now.Add(d) }

func (f *fixture) peer(t *testing.T, name string) uuid.UUID {
	t.Helper()
	p, created, err := f.svc.Peers.FindOrCreateByAuth(f.ctx, "google", "sub-"+name, name)
	if err != nil {
		t.Fatalf("create peer %s: %v", name, err)
	}
	if !created {
		t.Fatalf("peer %s already existed", name)
	}
	return p.PeerID
}

func (f *fixture) room(t *testing.T, owner uuid.UUID, name string) *model.Room {
	t.Helper()
	room, err := f.svc.Rooms.Create(f.ctx, owner, service.CreateRoomInput{Name: name})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	return room
}

// addMember brings peerID into a private room the only way there is: owner
// invites, peer accepts. An OWNER role is then granted by promotion.
func (f *fixture) addMember(t *testing.T, owner, roomID, peerID uuid.UUID, role model.RoomRoles) {
	t.Helper()
	inv := f.invite(t, owner, roomID, peerID)
	if _, _, err := f.svc.Invites.Accept(f.ctx, peerID, inv.Code); err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	if role == model.RoleOwner {
		if _, err := f.svc.Members.UpdateRoomRole(f.ctx, owner, roomID, peerID, model.RoleOwner); err != nil {
			t.Fatalf("promote: %v", err)
		}
	}
}

func (f *fixture) invite(t *testing.T, inviter, roomID, invitee uuid.UUID) *model.RoomInvite {
	t.Helper()
	inv, _, err := f.svc.Invites.Create(f.ctx, inviter, roomID, invitee)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	return inv
}

func (f *fixture) publicRoom(t *testing.T, owner uuid.UUID, name string) *model.Room {
	t.Helper()
	room, err := f.svc.Rooms.Create(f.ctx, owner, service.CreateRoomInput{Name: name, Visibility: model.VisibilityPublic})
	if err != nil {
		t.Fatalf("create public room: %v", err)
	}
	return room
}

func (f *fixture) eventTypes(t *testing.T, actor, roomID uuid.UUID) []model.RoomEventType {
	t.Helper()
	events, err := f.svc.Events.List(f.ctx, actor, roomID, 0, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	types := make([]model.RoomEventType, len(events))
	for i, e := range events {
		types[i] = e.EventType
	}
	return types
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func ptr[T any](v T) *T { return &v }

// =========================================================================
// Peers
// =========================================================================

func TestPeerFindOrCreateByAuth(t *testing.T) {
	f := newFixture(t)

	p1, created, err := f.svc.Peers.FindOrCreateByAuth(f.ctx, "google", "sub-1", "  Alice  ")
	if err != nil || !created {
		t.Fatalf("first sign-in: created=%v err=%v", created, err)
	}
	if p1.UserName != "Alice" {
		t.Errorf("userName = %q, want trimmed %q", p1.UserName, "Alice")
	}

	p2, created, err := f.svc.Peers.FindOrCreateByAuth(f.ctx, "google", "sub-1", "Ignored")
	if err != nil || created {
		t.Fatalf("second sign-in: created=%v err=%v", created, err)
	}
	if p2.PeerID != p1.PeerID {
		t.Errorf("second sign-in returned a different peer")
	}

	_, _, err = f.svc.Peers.FindOrCreateByAuth(f.ctx, "", "sub-2", "Bob")
	wantErr(t, err, service.ErrValidation)
}

func TestPeerUpdateUserName(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "Alice")

	p, err := f.svc.Peers.UpdateUserName(f.ctx, alice, "Alicia")
	if err != nil {
		t.Fatal(err)
	}
	if p.UserName != "Alicia" {
		t.Errorf("userName = %q, want Alicia", p.UserName)
	}

	_, err = f.svc.Peers.UpdateUserName(f.ctx, alice, "   ")
	wantErr(t, err, service.ErrValidation)

	_, err = f.svc.Peers.Get(f.ctx, uuid.New())
	wantErr(t, err, service.ErrNotFound)
}

// =========================================================================
// Rooms
// =========================================================================

func TestRoomCreateDefaultsAndOwner(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "Alice")

	room := f.room(t, alice, "Collab Workspace")
	if room.Visibility != model.VisibilityPrivate {
		t.Errorf("visibility = %q, want PRIVATE", room.Visibility)
	}
	if room.DefaultDocRole != model.DocRoleViewer {
		t.Errorf("defaultDocRole = %q, want VIEWER", room.DefaultDocRole)
	}

	m, err := f.svc.Members.Get(f.ctx, alice, room.RoomID, alice)
	if err != nil {
		t.Fatal(err)
	}
	if m.RoomRole != model.RoleOwner {
		t.Errorf("creator role = %q, want OWNER", m.RoomRole)
	}

	got := f.eventTypes(t, alice, room.RoomID)
	if len(got) != 1 || got[0] != model.EventRoomCreated {
		t.Errorf("events = %v, want [ROOM_CREATED]", got)
	}

	rooms, err := f.svc.Rooms.List(f.ctx, alice)
	if err != nil || len(rooms) != 1 {
		t.Fatalf("list rooms: %v, %d rooms", err, len(rooms))
	}
}

func TestRoomCreateValidation(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "Alice")

	_, err := f.svc.Rooms.Create(f.ctx, alice, service.CreateRoomInput{Name: ""})
	wantErr(t, err, service.ErrValidation)

	_, err = f.svc.Rooms.Create(f.ctx, alice, service.CreateRoomInput{Name: "x", DefaultDocRole: model.DocRoleOwner})
	wantErr(t, err, service.ErrValidation)

	_, err = f.svc.Rooms.Create(f.ctx, alice, service.CreateRoomInput{Name: "x", Visibility: "SECRET"})
	wantErr(t, err, service.ErrValidation)
}

func TestRoomUpdateRules(t *testing.T) {
	f := newFixture(t)
	alice, bob, eve := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Eve")
	room := f.room(t, alice, "Collab Workspace")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)

	// Participants cannot change room settings.
	_, err := f.svc.Rooms.Update(f.ctx, bob, room.RoomID, service.UpdateRoomInput{Name: ptr("Hacked")})
	wantErr(t, err, service.ErrForbidden)

	// Outsiders do not learn the room exists.
	_, err = f.svc.Rooms.Update(f.ctx, eve, room.RoomID, service.UpdateRoomInput{Name: ptr("Hacked")})
	wantErr(t, err, service.ErrNotFound)
	_, err = f.svc.Rooms.Get(f.ctx, eve, room.RoomID)
	wantErr(t, err, service.ErrNotFound)

	// Empty patch is rejected.
	_, err = f.svc.Rooms.Update(f.ctx, alice, room.RoomID, service.UpdateRoomInput{})
	wantErr(t, err, service.ErrValidation)

	updated, err := f.svc.Rooms.Update(f.ctx, alice, room.RoomID, service.UpdateRoomInput{
		Name:       ptr("Official Workspace"),
		Visibility: ptr(model.VisibilityPublic),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Official Workspace" || updated.Visibility != model.VisibilityPublic {
		t.Errorf("updated room = %+v", updated)
	}

	// A visibility-only change writes no event.
	if _, err := f.svc.Rooms.Update(f.ctx, alice, room.RoomID, service.UpdateRoomInput{Visibility: ptr(model.VisibilityPrivate)}); err != nil {
		t.Fatal(err)
	}

	got := f.eventTypes(t, alice, room.RoomID)
	want := []model.RoomEventType{model.EventRoomCreated, model.EventInviteCreated, model.EventAddMember, model.EventRenameRoom}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestRoomDelete(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.room(t, alice, "Temp")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)

	wantErr(t, f.svc.Rooms.Delete(f.ctx, bob, room.RoomID), service.ErrForbidden)

	if err := f.svc.Rooms.Delete(f.ctx, alice, room.RoomID); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Rooms.Get(f.ctx, alice, room.RoomID)
	wantErr(t, err, service.ErrNotFound)
}

// =========================================================================
// Members
// =========================================================================

func TestMemberLastOwnerProtection(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)

	// The only owner can neither leave nor be demoted.
	wantErr(t, f.svc.Members.Remove(f.ctx, alice, room.RoomID, alice), service.ErrConflict)
	_, err := f.svc.Members.UpdateRoomRole(f.ctx, alice, room.RoomID, alice, model.RoleParticipant)
	wantErr(t, err, service.ErrConflict)

	// With a second owner, the first may step down.
	if _, err := f.svc.Members.UpdateRoomRole(f.ctx, alice, room.RoomID, bob, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	m, err := f.svc.Members.UpdateRoomRole(f.ctx, alice, room.RoomID, alice, model.RoleParticipant)
	if err != nil {
		t.Fatal(err)
	}
	if m.RoomRole != model.RoleParticipant {
		t.Errorf("alice role = %q, want PARTICIPANT", m.RoomRole)
	}

	// Bob is now the last owner.
	wantErr(t, f.svc.Members.Remove(f.ctx, bob, room.RoomID, bob), service.ErrConflict)
}

func TestMemberRemove(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Carol")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)
	f.addMember(t, alice, room.RoomID, carol, model.RoleParticipant)

	// A participant cannot remove someone else.
	wantErr(t, f.svc.Members.Remove(f.ctx, bob, room.RoomID, carol), service.ErrForbidden)

	// But may leave.
	if err := f.svc.Members.Remove(f.ctx, bob, room.RoomID, bob); err != nil {
		t.Fatal(err)
	}

	// An owner may remove a participant.
	if err := f.svc.Members.Remove(f.ctx, alice, room.RoomID, carol); err != nil {
		t.Fatal(err)
	}

	// Removing a non-member.
	wantErr(t, f.svc.Members.Remove(f.ctx, alice, room.RoomID, carol), service.ErrNotFound)

	events, err := f.svc.Events.List(f.ctx, alice, room.RoomID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != model.EventRemoveMember || last.ActorID != alice || last.TargetID != carol {
		t.Errorf("last event = %+v, want REMOVE_MEMBER by alice of carol", last)
	}
}

func TestMemberUpdateRoomRoleNoOp(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)

	before := len(f.eventTypes(t, alice, room.RoomID))
	if _, err := f.svc.Members.UpdateRoomRole(f.ctx, alice, room.RoomID, bob, model.RoleParticipant); err != nil {
		t.Fatal(err)
	}
	if after := len(f.eventTypes(t, alice, room.RoomID)); after != before {
		t.Errorf("no-op role change wrote %d event(s)", after-before)
	}

	_, err := f.svc.Members.UpdateRoomRole(f.ctx, alice, room.RoomID, bob, "ADMIN")
	wantErr(t, err, service.ErrValidation)
}

// =========================================================================
// Documents
// =========================================================================

func TestDocumentCreateMakesCreatorDocOwner(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)

	doc, err := f.svc.Documents.Create(f.ctx, bob, room.RoomID, service.CreateDocumentInput{
		Title:        "Design Spec",
		PositionRank: "a0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Design Spec" || doc.AddedByPeer != bob || doc.ClonedFrom != nil {
		t.Errorf("doc = %+v", doc)
	}

	m, err := f.svc.Members.Get(f.ctx, bob, room.RoomID, bob)
	if err != nil {
		t.Fatal(err)
	}
	if m.DocRoles[doc.DocID.String()] != model.DocRoleOwner {
		t.Errorf("creator doc role = %q, want DOC_OWNER", m.DocRoles[doc.DocID.String()])
	}

	_, err = f.svc.Documents.Create(f.ctx, bob, room.RoomID, service.CreateDocumentInput{Title: "x"})
	wantErr(t, err, service.ErrValidation)

	outsider := f.peer(t, "Eve")
	_, err = f.svc.Documents.Create(f.ctx, outsider, room.RoomID, service.CreateDocumentInput{Title: "x", PositionRank: "a1"})
	wantErr(t, err, service.ErrNotFound)
}

func TestDocumentManagerRules(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.peer(t, "Alice"), f.peer(t, "Bob"), f.peer(t, "Carol")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)
	f.addMember(t, alice, room.RoomID, carol, model.RoleParticipant)

	doc, err := f.svc.Documents.Create(f.ctx, bob, room.RoomID, service.CreateDocumentInput{Title: "Notes", PositionRank: "a0"})
	if err != nil {
		t.Fatal(err)
	}

	// Carol is neither DOC_OWNER nor room OWNER.
	_, err = f.svc.Documents.Update(f.ctx, carol, room.RoomID, doc.DocID, service.UpdateDocumentInput{Title: ptr("Mine")})
	wantErr(t, err, service.ErrForbidden)
	wantErr(t, f.svc.Documents.Delete(f.ctx, carol, room.RoomID, doc.DocID), service.ErrForbidden)

	// Bob (DOC_OWNER) may rename.
	updated, err := f.svc.Documents.Update(f.ctx, bob, room.RoomID, doc.DocID, service.UpdateDocumentInput{Title: ptr("Meeting Notes")})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Meeting Notes" {
		t.Errorf("title = %q", updated.Title)
	}

	// Alice (room OWNER) may reorder; that writes no event.
	if _, err := f.svc.Documents.Update(f.ctx, alice, room.RoomID, doc.DocID, service.UpdateDocumentInput{PositionRank: ptr("b0")}); err != nil {
		t.Fatal(err)
	}

	// Bob grants Carol DOC_OWNER, after which she may delete it.
	if _, err := f.svc.Members.UpdateDocRole(f.ctx, bob, room.RoomID, doc.DocID, carol, model.DocRoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Documents.Delete(f.ctx, carol, room.RoomID, doc.DocID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Documents.Get(f.ctx, alice, room.RoomID, doc.DocID)
	wantErr(t, err, service.ErrNotFound)

	got := f.eventTypes(t, alice, room.RoomID)
	want := []model.RoomEventType{
		model.EventRoomCreated,
		model.EventInviteCreated, model.EventAddMember, model.EventInviteCreated, model.EventAddMember,
		model.EventCreateDoc, model.EventRenameDoc, model.EventUpdateDocRole, model.EventDeleteDoc,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestDocumentClone(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "Alice")
	room := f.room(t, alice, "Room")
	other := f.room(t, alice, "Other")

	src, err := f.svc.Documents.Create(f.ctx, alice, room.RoomID, service.CreateDocumentInput{Title: "Spec", PositionRank: "a0"})
	if err != nil {
		t.Fatal(err)
	}

	clone, err := f.svc.Documents.Clone(f.ctx, alice, room.RoomID, service.CloneDocumentInput{SourceDocID: src.DocID, PositionRank: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	if clone.Title != "Copy of Spec" {
		t.Errorf("clone title = %q, want %q", clone.Title, "Copy of Spec")
	}
	if clone.ClonedFrom == nil || *clone.ClonedFrom != src.DocID {
		t.Errorf("clonedFrom = %v, want %v", clone.ClonedFrom, src.DocID)
	}

	// Sources must be in the same room.
	_, err = f.svc.Documents.Clone(f.ctx, alice, other.RoomID, service.CloneDocumentInput{SourceDocID: src.DocID, PositionRank: "a0"})
	wantErr(t, err, service.ErrNotFound)

	docs, err := f.svc.Documents.List(f.ctx, alice, room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].DocID != src.DocID || docs[1].DocID != clone.DocID {
		t.Errorf("docs not ordered by position rank: %+v", docs)
	}
}

func TestUpdateDocRoleRecordsEffectiveOldRole(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.peer(t, "Alice"), f.peer(t, "Bob")
	room := f.room(t, alice, "Room")
	f.addMember(t, alice, room.RoomID, bob, model.RoleParticipant)
	doc, err := f.svc.Documents.Create(f.ctx, alice, room.RoomID, service.CreateDocumentInput{Title: "Doc", PositionRank: "a0"})
	if err != nil {
		t.Fatal(err)
	}

	m, err := f.svc.Members.UpdateDocRole(f.ctx, alice, room.RoomID, doc.DocID, bob, model.DocRoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if m.DocRoles[doc.DocID.String()] != model.DocRoleEditor {
		t.Errorf("bob doc role = %q, want EDITOR", m.DocRoles[doc.DocID.String()])
	}

	events, err := f.svc.Events.List(f.ctx, alice, room.RoomID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != model.EventUpdateDocRole || last.TargetDocID != doc.DocID {
		t.Fatalf("last event = %+v", last)
	}
	if last.Details["oldRole"] != string(model.DocRoleViewer) {
		t.Errorf("oldRole = %v, want room default VIEWER", last.Details["oldRole"])
	}

	// Bob (EDITOR) is not a document manager.
	_, err = f.svc.Members.UpdateDocRole(f.ctx, bob, room.RoomID, doc.DocID, bob, model.DocRoleOwner)
	wantErr(t, err, service.ErrForbidden)
}

// =========================================================================
// Events
// =========================================================================

func TestEventsListCursorAndLimit(t *testing.T) {
	f := newFixture(t)
	alice := f.peer(t, "Alice")
	room := f.room(t, alice, "Room")
	for i := 0; i < 3; i++ {
		if _, err := f.svc.Documents.Create(f.ctx, alice, room.RoomID, service.CreateDocumentInput{
			Title: fmt.Sprintf("Doc %d", i), PositionRank: fmt.Sprintf("a%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := f.svc.Events.List(f.ctx, alice, room.RoomID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("got %d events, want 4", len(all))
	}
	for i, e := range all {
		if e.EventID != uint64(i+1) {
			t.Errorf("event %d has id %d", i, e.EventID)
		}
	}

	page, err := f.svc.Events.List(f.ctx, alice, room.RoomID, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].EventID != 3 {
		t.Errorf("page after 2 limit 1 = %+v, want event 3", page)
	}

	_, err = f.svc.Events.List(f.ctx, f.peer(t, "Eve"), room.RoomID, 0, 0)
	wantErr(t, err, service.ErrNotFound)
}
