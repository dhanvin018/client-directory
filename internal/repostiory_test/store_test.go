package repository_test

// Integration tests: the service layer and the repositories against a real
// PostgreSQL. They are skipped unless TEST_DATABASE_URL is set, e.g.
//
//	TEST_DATABASE_URL="postgres://user:pass@localhost:5432/dirtest?sslmode=disable" go test ./internal/repostiory_test/...
//
// Pending migrations from TEST_SCHEMA_DIR (default ../../db/migrations) are
// applied the same way scripts/migrate.sh does, then every table is emptied
// before each test, so use a throwaway database. The blank import
// below is the driver; swap it (and the "postgres" name in sql.Open) for
// pgx's stdlib adapter ("pgx") if that is what you use.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/repository"
	"github.com/dhanvin018/client-directory/internal/service"
)

type env struct {
	db    *sql.DB
	store *repository.Store
	svc   *service.Services
}

func newEnv(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	dir := os.Getenv("TEST_SCHEMA_DIR")
	if dir == "" {
		dir = "../../db/migrations"
	}
	applyMigrations(t, db, dir)
	if _, err := db.Exec(`TRUNCATE sessions, desktop_logins, desktop_auth_codes, room_invites, room_bans, room_events,
		member_doc_roles, members, document_refs, rooms, peers CASCADE`); err != nil {
		t.Fatal(err)
	}

	store := repository.NewStore(db)
	return &env{db: db, store: store, svc: service.New(store)}
}

// applyMigrations mirrors scripts/migrate.sh: NNNN_*.sql files in version
// order, each applied once and recorded in schema_migrations, all in one
// transaction under the same advisory lock.
func applyMigrations(t *testing.T, db *sql.DB, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "[0-9][0-9][0-9][0-9]_*.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no migrations in %s (err %v)", dir, err)
	}
	sort.Strings(paths)

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`SELECT pg_advisory_xact_lock(4242001)`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
		     version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range paths {
		name := filepath.Base(path)
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			t.Fatalf("bad migration name %s", name)
		}
		var applied bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied); err != nil {
			t.Fatal(err)
		}
		if applied {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(b)); err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, version, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// join brings peer into a private room the only way there is: inviter
// invites, peer accepts.
func (e *env) join(t *testing.T, inviter, roomID, peer uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	inv, _, err := e.svc.Invites.Create(ctx, inviter, roomID, peer)
	mustNoErr(t, "invite", err)
	_, _, err = e.svc.Invites.Accept(ctx, peer, inv.Code)
	mustNoErr(t, "accept", err)
}

func (e *env) peer(t *testing.T, name string) uuid.UUID {
	t.Helper()
	p, _, err := e.svc.Peers.FindOrCreateByAuth(context.Background(), "test", name, name)
	if err != nil {
		t.Fatal(err)
	}
	return p.PeerID
}

func (e *env) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustNoErr(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func wantErr(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: got %v, want %v", what, err, target)
	}
}

func TestPeers(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	p, created, err := e.svc.Peers.FindOrCreateByAuth(ctx, "google", "sub-1", "Ada")
	mustNoErr(t, "first sign-in", err)
	if !created {
		t.Fatal("first sign-in should create the peer")
	}
	again, created, err := e.svc.Peers.FindOrCreateByAuth(ctx, "google", "sub-1", "Ignored")
	mustNoErr(t, "second sign-in", err)
	if created || again.PeerID != p.PeerID || again.UserName != "Ada" {
		t.Fatalf("second sign-in should return the same peer, got %+v created=%v", again, created)
	}

	// The same subject under another provider is a different peer.
	other, _, err := e.svc.Peers.FindOrCreateByAuth(ctx, "github", "sub-1", "Ada")
	mustNoErr(t, "other provider", err)
	if other.PeerID == p.PeerID {
		t.Fatal("different provider must give a different peer")
	}

	// A duplicate identity straight at the repository is a conflict.
	_, err = e.store.Peers().Create(ctx, service.NewPeer{
		PeerID: uuid.New(), Handle: "dup", UserName: "dup", AuthProvider: "google", AuthSubject: "sub-1",
	})
	wantErr(t, "duplicate identity", err, service.ErrConflict)

	renamed, err := e.svc.Peers.UpdateUserName(ctx, p.PeerID, "  Ada L.  ")
	mustNoErr(t, "rename", err)
	if renamed.UserName != "Ada L." {
		t.Fatalf("user name not trimmed/saved: %q", renamed.UserName)
	}

	_, err = e.svc.Peers.Get(ctx, uuid.New())
	wantErr(t, "unknown peer", err, service.ErrNotFound)
}

func TestRoomScenario(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, bob, carol := e.peer(t, "alice"), e.peer(t, "bob"), e.peer(t, "carol")

	room, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "Plans"})
	mustNoErr(t, "create room", err)
	if room.Visibility != model.VisibilityPrivate || room.DefaultDocRole != model.DocRoleViewer {
		t.Fatalf("defaults wrong: %+v", room)
	}
	rid := room.RoomID

	_, err = e.svc.Rooms.Get(ctx, bob, rid)
	wantErr(t, "non-member reads room", err, service.ErrNotFound)

	e.join(t, alice, rid, bob)
	_, _, err = e.svc.Invites.Create(ctx, alice, rid, bob)
	wantErr(t, "invite a member", err, service.ErrConflict)
	_, _, err = e.svc.Invites.Create(ctx, carol, rid, bob)
	wantErr(t, "outsider invites", err, service.ErrNotFound)

	doc, err := e.svc.Documents.Create(ctx, alice, rid, service.CreateDocumentInput{Title: "Spec", PositionRank: "a0"})
	mustNoErr(t, "create doc", err)

	m, err := e.svc.Members.UpdateDocRole(ctx, alice, rid, doc.DocID, bob, model.DocRoleEditor)
	mustNoErr(t, "grant editor", err)
	if m.DocRoles[doc.DocID.String()] != model.DocRoleEditor {
		t.Fatalf("bob's doc role: %+v", m.DocRoles)
	}
	if m.InvitedBy != alice || m.JoinedAt.IsZero() {
		t.Fatalf("bob should record alice as InvitedBy and a join time: %+v", m)
	}
	am, err := e.svc.Members.Get(ctx, alice, rid, alice)
	mustNoErr(t, "get alice", err)
	if am.RoomRole != model.RoleOwner || am.DocRoles[doc.DocID.String()] != model.DocRoleOwner || am.InvitedBy != uuid.Nil {
		t.Fatalf("alice's membership wrong: %+v", am)
	}

	_, err = e.svc.Members.UpdateDocRole(ctx, bob, rid, doc.DocID, carol, model.DocRoleViewer)
	wantErr(t, "editor manages roles", err, service.ErrForbidden)

	_, err = e.svc.Members.UpdateRoomRole(ctx, alice, rid, alice, model.RoleParticipant)
	wantErr(t, "demote the only owner", err, service.ErrConflict)
	_, err = e.svc.Members.UpdateRoomRole(ctx, alice, rid, bob, model.RoleOwner)
	mustNoErr(t, "promote bob", err)
	_, err = e.svc.Members.UpdateRoomRole(ctx, alice, rid, alice, model.RoleParticipant)
	mustNoErr(t, "alice steps down", err)

	wantErr(t, "participant removes owner", e.svc.Members.Remove(ctx, alice, rid, bob), service.ErrForbidden)
	mustNoErr(t, "alice leaves", e.svc.Members.Remove(ctx, alice, rid, alice))
	wantErr(t, "last owner leaves", e.svc.Members.Remove(ctx, bob, rid, bob), service.ErrConflict)

	// The audit log holds exactly the successful changes, in order.
	events, err := e.svc.Events.List(ctx, bob, rid, 0, 100)
	mustNoErr(t, "list events", err)
	var gotTypes []model.RoomEventType
	for i, ev := range events {
		if ev.EventID != uint64(i+1) {
			t.Fatalf("event %d has id %d", i, ev.EventID)
		}
		gotTypes = append(gotTypes, ev.EventType)
	}
	wantTypes := []model.RoomEventType{
		model.EventRoomCreated, model.EventInviteCreated, model.EventAddMember, model.EventCreateDoc, model.EventUpdateDocRole,
		model.EventUpdateRoomRole, model.EventUpdateRoomRole, model.EventRemoveMember,
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types:\n got  %v\n want %v", gotTypes, wantTypes)
	}
	if events[4].TargetID != bob || events[4].TargetDocID != doc.DocID {
		t.Fatalf("UPDATE_DOC_ROLE targets wrong: %+v", events[4])
	}
	if events[0].TargetID != uuid.Nil || events[0].TargetDocID != uuid.Nil || events[0].ActorID != alice {
		t.Fatalf("ROOM_CREATED should have an actor and no targets: %+v", events[0])
	}

	// Rename writes an event whose details round-trip through JSONB.
	_, err = e.svc.Rooms.Update(ctx, bob, rid, service.UpdateRoomInput{Name: ptr("Roadmap")})
	mustNoErr(t, "rename room", err)
	later, err := e.svc.Events.List(ctx, bob, rid, 8, 100)
	mustNoErr(t, "events after 8", err)
	if len(later) != 1 || later[0].EventType != model.EventRenameRoom ||
		later[0].Details["oldName"] != "Plans" || later[0].Details["newName"] != "Roadmap" {
		t.Fatalf("rename event wrong: %+v", later)
	}

	bobRooms, err := e.svc.Rooms.List(ctx, bob)
	mustNoErr(t, "bob rooms", err)
	aliceRooms, err := e.svc.Rooms.List(ctx, alice)
	mustNoErr(t, "alice rooms", err)
	if len(bobRooms) != 1 || len(aliceRooms) != 0 {
		t.Fatalf("room lists: bob=%d alice=%d", len(bobRooms), len(aliceRooms))
	}

	// Deleting the room removes everything that hangs off it.
	mustNoErr(t, "delete room", e.svc.Rooms.Delete(ctx, bob, rid))
	_, err = e.svc.Rooms.Get(ctx, bob, rid)
	wantErr(t, "room gone", err, service.ErrNotFound)
	for _, table := range []string{"rooms", "members", "member_doc_roles", "document_refs", "room_events", "room_invites", "room_bans"} {
		if n := e.count(t, table); n != 0 {
			t.Fatalf("%s still has %d rows after the room was deleted", table, n)
		}
	}
}

func TestDocumentOrderIsBytewise(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.peer(t, "alice")
	room, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "r"})
	mustNoErr(t, "create room", err)

	// Under a locale collation "a0" would sort before "B0"; byte order puts
	// uppercase first.
	for _, rank := range []string{"a0", "B0", "a1", "Zz"} {
		_, err := e.svc.Documents.Create(ctx, alice, room.RoomID, service.CreateDocumentInput{Title: "t-" + rank, PositionRank: rank})
		mustNoErr(t, "create "+rank, err)
	}
	docs, err := e.svc.Documents.List(ctx, alice, room.RoomID)
	mustNoErr(t, "list docs", err)
	var got []string
	for _, d := range docs {
		got = append(got, d.PositionRank)
	}
	if want := []string{"B0", "Zz", "a0", "a1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("document order: got %v, want %v", got, want)
	}

	// Clone records its origin; reorder and rename work through the patch.
	clone, err := e.svc.Documents.Clone(ctx, alice, room.RoomID, service.CloneDocumentInput{SourceDocID: docs[0].DocID, PositionRank: "b0"})
	mustNoErr(t, "clone", err)
	if clone.ClonedFrom == nil || *clone.ClonedFrom != docs[0].DocID || clone.Title != "Copy of t-B0" {
		t.Fatalf("clone wrong: %+v", clone)
	}
	upd, err := e.svc.Documents.Update(ctx, alice, room.RoomID, clone.DocID, service.UpdateDocumentInput{PositionRank: ptr("0")})
	mustNoErr(t, "reorder", err)
	if upd.PositionRank != "0" || upd.Title != clone.Title {
		t.Fatalf("a rank-only update must leave the title alone: %+v", upd)
	}
}

// Adding a member with an invalid doc role must not leave the member behind,
// even without a surrounding transaction.
func TestAddMemberIsAtomic(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, bob := e.peer(t, "alice"), e.peer(t, "bob")

	roomA, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "A"})
	mustNoErr(t, "room A", err)
	roomB, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "B"})
	mustNoErr(t, "room B", err)
	docInB, err := e.svc.Documents.Create(ctx, alice, roomB.RoomID, service.CreateDocumentInput{Title: "x", PositionRank: "a"})
	mustNoErr(t, "doc in B", err)

	err = e.store.Members().Add(ctx, service.NewMember{
		RoomID:   roomA.RoomID,
		PeerID:   bob,
		RoomRole: model.RoleParticipant,
		DocRoles: map[uuid.UUID]model.DocRoles{docInB.DocID: model.DocRoleEditor}, // not in room A
	})
	if err == nil {
		t.Fatal("expected a foreign-key failure for a document from another room")
	}
	if _, err := e.store.Members().GetRoomRole(ctx, roomA.RoomID, bob); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("member must not exist after the failed add, got err=%v", err)
	}
}

// Many concurrent writers to one room must get distinct, gapless event IDs.
func TestConcurrentEventIDs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.peer(t, "alice")
	room, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "busy"})
	mustNoErr(t, "create room", err)

	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.svc.Documents.Create(ctx, alice, room.RoomID, service.CreateDocumentInput{
				Title: fmt.Sprintf("doc-%d", i), PositionRank: fmt.Sprintf("r%02d", i),
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		mustNoErr(t, "concurrent create", err)
	}

	events, err := e.svc.Events.List(ctx, alice, room.RoomID, 0, 500)
	mustNoErr(t, "list events", err)
	if len(events) != writers+1 {
		t.Fatalf("got %d events, want %d", len(events), writers+1)
	}
	for i, ev := range events {
		if ev.EventID != uint64(i+1) {
			t.Fatalf("event IDs not consecutive: position %d has id %d", i, ev.EventID)
		}
	}
}

func TestNotFoundMapping(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, err := e.store.Rooms().Get(ctx, uuid.New())
	wantErr(t, "unknown room", err, service.ErrNotFound)
	wantErr(t, "delete unknown room", e.store.Rooms().Delete(ctx, uuid.New()), service.ErrNotFound)
	err = e.store.Events().Append(ctx, service.NewEvent{RoomID: uuid.New(), ActorID: uuid.New(), Type: model.EventRoomCreated})
	wantErr(t, "event for unknown room", err, service.ErrNotFound)
}

func ptr[T any](v T) *T { return &v }

// Mixed concurrent writers (documents, renames, invites) hitting one room
// must neither deadlock nor lose or duplicate event IDs. Each kind of write
// takes different locks (row lock, foreign-key checks), so this exercises
// how they interact.
func TestConcurrentMixedWrites(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.peer(t, "alice")
	room, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "mixed"})
	mustNoErr(t, "create room", err)

	const each = 10
	newcomers := make([]uuid.UUID, each)
	for i := range newcomers {
		newcomers[i] = e.peer(t, fmt.Sprintf("p%d", i))
	}

	var wg sync.WaitGroup
	errs := make(chan error, 3*each)
	run := func(fn func() error) {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- fn() }()
	}
	for i := 0; i < each; i++ {
		i := i
		run(func() error {
			_, err := e.svc.Documents.Create(ctx, alice, room.RoomID, service.CreateDocumentInput{
				Title: fmt.Sprintf("doc-%d", i), PositionRank: fmt.Sprintf("r%02d", i),
			})
			return err
		})
		run(func() error {
			_, err := e.svc.Rooms.Update(ctx, alice, room.RoomID, service.UpdateRoomInput{Name: ptr(fmt.Sprintf("name-%d", i))})
			return err
		})
		run(func() error {
			_, _, err := e.svc.Invites.Create(ctx, alice, room.RoomID, newcomers[i])
			return err
		})
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("writers did not finish in 30s: probable deadlock")
	}
	close(errs)
	for err := range errs {
		mustNoErr(t, "concurrent write", err)
	}

	events, err := e.svc.Events.List(ctx, alice, room.RoomID, 0, 500)
	mustNoErr(t, "list events", err)
	if len(events) != 1+3*each {
		t.Fatalf("got %d events, want %d", len(events), 1+3*each)
	}
	for i, ev := range events {
		if ev.EventID != uint64(i+1) {
			t.Fatalf("event IDs not consecutive: position %d has id %d", i, ev.EventID)
		}
	}
}

// The services lock the room before writing, which already serializes
// writers. This test removes that safety net and checks that Append alone
// hands out distinct, consecutive IDs.
func TestEventAppendIsSafeWithoutRoomLock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.peer(t, "alice")
	room, err := e.svc.Rooms.Create(ctx, alice, service.CreateRoomInput{Name: "bare"})
	mustNoErr(t, "create room", err)

	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.store.WithTx(ctx, func(r service.Repos) error {
				return r.Events().Append(ctx, service.NewEvent{
					RoomID: room.RoomID, ActorID: alice, Type: model.EventRenameDoc, At: time.Now(),
				})
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		mustNoErr(t, "concurrent append", err)
	}

	events, err := e.svc.Events.List(ctx, alice, room.RoomID, 0, 500)
	mustNoErr(t, "list events", err)
	if len(events) != writers+1 {
		t.Fatalf("got %d events, want %d", len(events), writers+1)
	}
	for i, ev := range events {
		if ev.EventID != uint64(i+1) {
			t.Fatalf("event IDs not consecutive: position %d has id %d", i, ev.EventID)
		}
	}
}
