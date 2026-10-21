package service_test

// memStore is an in-memory fake of service.Store for the service tests. It
// follows the repository contract in service/repository.go: missing rows
// return ErrNotFound, duplicates return ErrConflict, and WithTx rolls back
// every change when fn returns an error.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

// =========================================================================
// In-memory store
// =========================================================================

type memberKey struct{ room, peer uuid.UUID }

type docRoleKey struct{ room, doc, peer uuid.UUID }

type docRow struct {
	roomID uuid.UUID
	ref    model.DocumentRef
}

type memberRow struct {
	roomRole  model.RoomRoles
	invitedBy uuid.UUID
	joinedAt  time.Time
}

type memData struct {
	peers    map[uuid.UUID]model.Peer
	rooms    map[uuid.UUID]model.Room
	docs     map[uuid.UUID]docRow
	members  map[memberKey]memberRow
	docRoles map[docRoleKey]model.DocRoles
	events   map[uuid.UUID][]model.RoomEvent
	invites  map[uuid.UUID]inviteRow
	bans     map[memberKey]banRow
}

type inviteRow struct {
	id, roomID, inviter, invitee uuid.UUID
	code                         string
	role                         model.RoomRoles
	status                       model.InviteStatus
	createdAt, expiresAt         time.Time
	respondedAt                  *time.Time
}

type banRow struct {
	bannedBy uuid.UUID
	bannedAt time.Time
}

func newMemData() *memData {
	return &memData{
		peers:    map[uuid.UUID]model.Peer{},
		rooms:    map[uuid.UUID]model.Room{},
		docs:     map[uuid.UUID]docRow{},
		members:  map[memberKey]memberRow{},
		docRoles: map[docRoleKey]model.DocRoles{},
		events:   map[uuid.UUID][]model.RoomEvent{},
		invites:  map[uuid.UUID]inviteRow{},
		bans:     map[memberKey]banRow{},
	}
}

// clone copies the data so WithTx can restore it on rollback.
func (d *memData) clone() *memData {
	c := newMemData()
	for k, v := range d.peers {
		c.peers[k] = v
	}
	for k, v := range d.rooms {
		c.rooms[k] = v
	}
	for k, v := range d.docs {
		c.docs[k] = v
	}
	for k, v := range d.members {
		c.members[k] = v
	}
	for k, v := range d.docRoles {
		c.docRoles[k] = v
	}
	for k, v := range d.invites {
		c.invites[k] = v
	}
	for k, v := range d.bans {
		c.bans[k] = v
	}
	for k, v := range d.events {
		c.events[k] = append([]model.RoomEvent(nil), v...)
	}
	return c
}

type memStore struct {
	txMu sync.Mutex // serializes transactions, like the room row lock
	mu   sync.Mutex // guards data
	data *memData
}

func newMemStore() *memStore { return &memStore{data: newMemData()} }

func (s *memStore) Peers() service.PeerRepository         { return peerRepo{s} }
func (s *memStore) Rooms() service.RoomRepository         { return roomRepo{s} }
func (s *memStore) Documents() service.DocumentRepository { return documentRepo{s} }
func (s *memStore) Members() service.MemberRepository     { return memberRepo{s} }
func (s *memStore) Events() service.EventRepository       { return eventRepo{s} }
func (s *memStore) Invites() service.InviteRepository     { return inviteRepo{s} }
func (s *memStore) Bans() service.BanRepository           { return banRepo{s} }

func (s *memStore) WithTx(ctx context.Context, fn func(service.Repos) error) (err error) {
	s.txMu.Lock()
	defer s.txMu.Unlock()

	s.mu.Lock()
	snapshot := s.data.clone()
	s.mu.Unlock()

	rollback := func() {
		s.mu.Lock()
		s.data = snapshot
		s.mu.Unlock()
	}
	defer func() {
		if p := recover(); p != nil {
			rollback()
			panic(p)
		}
	}()

	if err = fn(s); err != nil {
		rollback()
	}
	return err
}

func notFound(what string) error { return fmt.Errorf("%w: %s", service.ErrNotFound, what) }
func dup(what string) error      { return fmt.Errorf("%w: %s", service.ErrConflict, what) }

// ---- Peers ----

type peerRepo struct{ s *memStore }

func (r peerRepo) Get(ctx context.Context, id uuid.UUID) (*model.Peer, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	p, ok := r.s.data.peers[id]
	if !ok {
		return nil, notFound("peer")
	}
	return &p, nil
}

func (r peerRepo) GetByAuth(ctx context.Context, provider, subject string) (*model.Peer, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, p := range r.s.data.peers {
		if p.AuthProvider == provider && p.AuthSubject == subject {
			return &p, nil
		}
	}
	return nil, notFound("peer")
}

func (r peerRepo) Create(ctx context.Context, in service.NewPeer) (*model.Peer, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.data.peers[in.PeerID]; ok {
		return nil, dup("peer id")
	}
	for _, p := range r.s.data.peers {
		if p.AuthProvider == in.AuthProvider && p.AuthSubject == in.AuthSubject {
			return nil, dup("auth identity")
		}
		if p.Handle == in.Handle {
			return nil, dup("handle")
		}
	}
	p := model.Peer{
		PeerID:       in.PeerID,
		Handle:       in.Handle,
		UserName:     in.UserName,
		AuthProvider: in.AuthProvider,
		AuthSubject:  in.AuthSubject,
	}
	r.s.data.peers[p.PeerID] = p
	return &p, nil
}

func (r peerRepo) UpdateUserName(ctx context.Context, id uuid.UUID, userName string) (*model.Peer, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	p, ok := r.s.data.peers[id]
	if !ok {
		return nil, notFound("peer")
	}
	p.UserName = userName
	r.s.data.peers[id] = p
	return &p, nil
}

func (r peerRepo) UpdateHandle(ctx context.Context, id uuid.UUID, handle string) (*model.Peer, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	p, ok := r.s.data.peers[id]
	if !ok {
		return nil, notFound("peer")
	}
	for _, other := range r.s.data.peers {
		if other.PeerID != id && other.Handle == handle {
			return nil, dup("handle")
		}
	}
	p.Handle = handle
	r.s.data.peers[id] = p
	return &p, nil
}

// ref resolves a peer to its public face; the caller holds mu.
func (s *memStore) ref(id uuid.UUID) model.PeerRef {
	p := s.data.peers[id]
	return model.PeerRef{PeerID: id, Handle: p.Handle, UserName: p.UserName}
}

// ---- Rooms ----

type roomRepo struct{ s *memStore }

func (r roomRepo) Create(ctx context.Context, in service.NewRoom) (*model.Room, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.data.rooms[in.RoomID]; ok {
		return nil, dup("room id")
	}
	room := model.Room{
		RoomID:         in.RoomID,
		Name:           in.Name,
		CreatedAt:      in.CreatedAt,
		Visibility:     in.Visibility,
		DefaultDocRole: in.DefaultDocRole,
	}
	r.s.data.rooms[room.RoomID] = room
	return &room, nil
}

func (r roomRepo) Get(ctx context.Context, roomID uuid.UUID) (*model.Room, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	room, ok := r.s.data.rooms[roomID]
	if !ok {
		return nil, notFound("room")
	}
	return &room, nil
}

func (r roomRepo) LockForUpdate(ctx context.Context, roomID uuid.UUID) (*model.Room, error) {
	// WithTx already serializes transactions, so this is just a load.
	return r.Get(ctx, roomID)
}

func (r roomRepo) ListForPeer(ctx context.Context, peerID uuid.UUID) ([]model.Room, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []model.Room
	for k := range r.s.data.members {
		if k.peer == peerID {
			out = append(out, r.s.data.rooms[k.room])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (r roomRepo) Update(ctx context.Context, roomID uuid.UUID, p service.RoomPatch) (*model.Room, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	room, ok := r.s.data.rooms[roomID]
	if !ok {
		return nil, notFound("room")
	}
	if p.Name != nil {
		room.Name = *p.Name
	}
	if p.Visibility != nil {
		room.Visibility = *p.Visibility
	}
	if p.DefaultDocRole != nil {
		room.DefaultDocRole = *p.DefaultDocRole
	}
	r.s.data.rooms[roomID] = room
	return &room, nil
}

func (r roomRepo) Delete(ctx context.Context, roomID uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := r.s.data
	if _, ok := d.rooms[roomID]; !ok {
		return notFound("room")
	}
	delete(d.rooms, roomID)
	delete(d.events, roomID)
	for k, v := range d.docs {
		if v.roomID == roomID {
			delete(d.docs, k)
		}
	}
	for k := range d.members {
		if k.room == roomID {
			delete(d.members, k)
		}
	}
	for k := range d.docRoles {
		if k.room == roomID {
			delete(d.docRoles, k)
		}
	}
	for k, v := range d.invites {
		if v.roomID == roomID {
			delete(d.invites, k)
		}
	}
	for k := range d.bans {
		if k.room == roomID {
			delete(d.bans, k)
		}
	}
	return nil
}

// summary builds a room summary; the caller holds mu.
func (r roomRepo) summary(room model.Room, peerID uuid.UUID) model.RoomSummary {
	sum := model.RoomSummary{RoomID: room.RoomID, Name: room.Name, Visibility: room.Visibility}
	for k := range r.s.data.members {
		if k.room == room.RoomID {
			sum.MemberCount++
			if k.peer == peerID {
				sum.IsMember = true
			}
		}
	}
	return sum
}

func (r roomRepo) SearchPublic(ctx context.Context, prefix string, peerID uuid.UUID, limit int) ([]model.RoomSummary, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []model.RoomSummary
	for _, room := range r.s.data.rooms {
		if room.Visibility == model.VisibilityPublic && strings.HasPrefix(strings.ToLower(room.Name), prefix) {
			out = append(out, r.summary(room, peerID))
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r roomRepo) Summary(ctx context.Context, roomID, peerID uuid.UUID) (*model.RoomSummary, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	room, ok := r.s.data.rooms[roomID]
	if !ok {
		return nil, notFound("room")
	}
	sum := r.summary(room, peerID)
	return &sum, nil
}

// ---- Documents ----

type documentRepo struct{ s *memStore }

func (r documentRepo) Insert(ctx context.Context, in service.NewDocument) (*model.DocumentRef, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.data.rooms[in.RoomID]; !ok {
		return nil, notFound("room")
	}
	if _, ok := r.s.data.docs[in.DocID]; ok {
		return nil, dup("doc id")
	}
	ref := model.DocumentRef{
		DocID:        in.DocID,
		Title:        in.Title,
		PositionRank: in.PositionRank,
		AddedByPeer:  in.AddedBy,
		AddedAt:      in.AddedAt,
		ClonedFrom:   in.ClonedFrom,
	}
	r.s.data.docs[in.DocID] = docRow{roomID: in.RoomID, ref: ref}
	return &ref, nil
}

func (r documentRepo) Get(ctx context.Context, roomID, docID uuid.UUID) (*model.DocumentRef, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	row, ok := r.s.data.docs[docID]
	if !ok || row.roomID != roomID {
		return nil, notFound("document")
	}
	ref := row.ref
	return &ref, nil
}

func (r documentRepo) List(ctx context.Context, roomID uuid.UUID) ([]model.DocumentRef, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []model.DocumentRef
	for _, row := range r.s.data.docs {
		if row.roomID == roomID {
			out = append(out, row.ref)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PositionRank < out[j].PositionRank })
	return out, nil
}

func (r documentRepo) Update(ctx context.Context, roomID, docID uuid.UUID, p service.DocumentPatch) (*model.DocumentRef, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	row, ok := r.s.data.docs[docID]
	if !ok || row.roomID != roomID {
		return nil, notFound("document")
	}
	if p.Title != nil {
		row.ref.Title = *p.Title
	}
	if p.PositionRank != nil {
		row.ref.PositionRank = *p.PositionRank
	}
	r.s.data.docs[docID] = row
	ref := row.ref
	return &ref, nil
}

func (r documentRepo) Delete(ctx context.Context, roomID, docID uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	row, ok := r.s.data.docs[docID]
	if !ok || row.roomID != roomID {
		return notFound("document")
	}
	delete(r.s.data.docs, docID)
	for k := range r.s.data.docRoles {
		if k.doc == docID {
			delete(r.s.data.docRoles, k)
		}
	}
	return nil
}

// ---- Members ----

type memberRepo struct{ s *memStore }

func (r memberRepo) Add(ctx context.Context, m service.NewMember) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := r.s.data
	if _, ok := d.rooms[m.RoomID]; !ok {
		return notFound("room")
	}
	key := memberKey{m.RoomID, m.PeerID}
	if _, ok := d.members[key]; ok {
		return dup("member")
	}
	row := memberRow{roomRole: m.RoomRole, joinedAt: m.JoinedAt}
	if m.InvitedBy != nil {
		row.invitedBy = *m.InvitedBy
	}
	d.members[key] = row
	for docID, role := range m.DocRoles {
		d.docRoles[docRoleKey{m.RoomID, docID, m.PeerID}] = role
	}
	return nil
}

// build assembles a model.Member; the caller holds mu.
func (r memberRepo) build(roomID, peerID uuid.UUID, row memberRow) model.Member {
	m := model.Member{
		PeerID:    peerID,
		RoomRole:  row.roomRole,
		DocRoles:  map[string]model.DocRoles{},
		InvitedBy: row.invitedBy,
		JoinedAt:  row.joinedAt,
	}
	for k, role := range r.s.data.docRoles {
		if k.room == roomID && k.peer == peerID {
			m.DocRoles[k.doc.String()] = role
		}
	}
	return m
}

func (r memberRepo) Get(ctx context.Context, roomID, peerID uuid.UUID) (*model.Member, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	row, ok := r.s.data.members[memberKey{roomID, peerID}]
	if !ok {
		return nil, notFound("member")
	}
	m := r.build(roomID, peerID, row)
	return &m, nil
}

func (r memberRepo) List(ctx context.Context, roomID uuid.UUID) ([]model.Member, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []model.Member
	for k, row := range r.s.data.members {
		if k.room == roomID {
			out = append(out, r.build(roomID, k.peer, row))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JoinedAt.Before(out[j].JoinedAt) })
	return out, nil
}

func (r memberRepo) GetRoomRole(ctx context.Context, roomID, peerID uuid.UUID) (model.RoomRoles, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	row, ok := r.s.data.members[memberKey{roomID, peerID}]
	if !ok {
		return "", notFound("member")
	}
	return row.roomRole, nil
}

func (r memberRepo) CountRoomOwners(ctx context.Context, roomID uuid.UUID) (int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	n := 0
	for k, row := range r.s.data.members {
		if k.room == roomID && row.roomRole == model.RoleOwner {
			n++
		}
	}
	return n, nil
}

func (r memberRepo) SetRoomRole(ctx context.Context, roomID, peerID uuid.UUID, role model.RoomRoles) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	key := memberKey{roomID, peerID}
	row, ok := r.s.data.members[key]
	if !ok {
		return notFound("member")
	}
	row.roomRole = role
	r.s.data.members[key] = row
	return nil
}

func (r memberRepo) Remove(ctx context.Context, roomID, peerID uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	key := memberKey{roomID, peerID}
	if _, ok := r.s.data.members[key]; !ok {
		return notFound("member")
	}
	delete(r.s.data.members, key)
	for k := range r.s.data.docRoles {
		if k.room == roomID && k.peer == peerID {
			delete(r.s.data.docRoles, k)
		}
	}
	return nil
}

func (r memberRepo) GetDocRole(ctx context.Context, roomID, docID, peerID uuid.UUID) (model.DocRoles, bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	role, ok := r.s.data.docRoles[docRoleKey{roomID, docID, peerID}]
	return role, ok, nil
}

func (r memberRepo) SetDocRole(ctx context.Context, roomID, docID, peerID uuid.UUID, role model.DocRoles) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.data.members[memberKey{roomID, peerID}]; !ok {
		return notFound("member")
	}
	r.s.data.docRoles[docRoleKey{roomID, docID, peerID}] = role
	return nil
}

// ---- Events ----

type eventRepo struct{ s *memStore }

func (r eventRepo) Append(ctx context.Context, e service.NewEvent) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.data.rooms[e.RoomID]; !ok {
		return notFound("room")
	}
	evt := model.RoomEvent{
		RoomID:    e.RoomID,
		EventID:   uint64(len(r.s.data.events[e.RoomID]) + 1),
		ActorID:   e.ActorID,
		EventType: e.Type,
		Details:   e.Details,
		Timestamp: e.At,
	}
	if e.TargetID != nil {
		evt.TargetID = *e.TargetID
	}
	if e.TargetDocID != nil {
		evt.TargetDocID = *e.TargetDocID
	}
	r.s.data.events[e.RoomID] = append(r.s.data.events[e.RoomID], evt)
	return nil
}

func (r eventRepo) List(ctx context.Context, roomID uuid.UUID, after uint64, limit int) ([]model.RoomEvent, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []model.RoomEvent
	for _, e := range r.s.data.events[roomID] {
		if e.EventID > after {
			out = append(out, e)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// ---- Invites ----

type inviteRepo struct{ s *memStore }

// build resolves names like the SQL join does; the caller holds mu.
func (r inviteRepo) build(row inviteRow) model.RoomInvite {
	return model.RoomInvite{
		InviteID:    row.id,
		Code:        row.code,
		RoomID:      row.roomID,
		RoomName:    r.s.data.rooms[row.roomID].Name,
		Inviter:     r.s.ref(row.inviter),
		Invitee:     r.s.ref(row.invitee),
		RoomRole:    row.role,
		Status:      row.status,
		CreatedAt:   row.createdAt,
		ExpiresAt:   row.expiresAt,
		RespondedAt: row.respondedAt,
	}
}

// find returns the invites matching keep, newest first; the caller holds mu.
func (r inviteRepo) find(keep func(inviteRow) bool) []model.RoomInvite {
	var out []model.RoomInvite
	for _, row := range r.s.data.invites {
		if keep(row) {
			out = append(out, r.build(row))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (r inviteRepo) one(keep func(inviteRow) bool) (*model.RoomInvite, error) {
	found := r.find(keep)
	if len(found) == 0 {
		return nil, notFound("invite")
	}
	return &found[0], nil
}

func (r inviteRepo) Create(ctx context.Context, in service.NewInvite) (*model.RoomInvite, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, row := range r.s.data.invites {
		if row.code == in.Code {
			return nil, dup("invite code")
		}
		if row.roomID == in.RoomID && row.invitee == in.InviteeID && row.status == model.InvitePending {
			return nil, dup("pending invite")
		}
	}
	row := inviteRow{
		id: in.InviteID, roomID: in.RoomID, inviter: in.InviterID, invitee: in.InviteeID,
		code: in.Code, role: in.RoomRole, status: model.InvitePending,
		createdAt: in.CreatedAt, expiresAt: in.ExpiresAt,
	}
	r.s.data.invites[row.id] = row
	inv := r.build(row)
	return &inv, nil
}

func (r inviteRepo) GetByID(ctx context.Context, roomID, inviteID uuid.UUID) (*model.RoomInvite, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.one(func(row inviteRow) bool { return row.id == inviteID && row.roomID == roomID })
}

func (r inviteRepo) GetByCode(ctx context.Context, code string) (*model.RoomInvite, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.one(func(row inviteRow) bool { return row.code == code })
}

func (r inviteRepo) GetPending(ctx context.Context, roomID, inviteeID uuid.UUID) (*model.RoomInvite, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.one(func(row inviteRow) bool {
		return row.roomID == roomID && row.invitee == inviteeID && row.status == model.InvitePending
	})
}

func (r inviteRepo) ListPendingForRoom(ctx context.Context, roomID uuid.UUID) ([]model.RoomInvite, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.find(func(row inviteRow) bool { return row.roomID == roomID && row.status == model.InvitePending }), nil
}

func (r inviteRepo) ListPendingForInvitee(ctx context.Context, inviteeID uuid.UUID) ([]model.RoomInvite, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.find(func(row inviteRow) bool { return row.invitee == inviteeID && row.status == model.InvitePending }), nil
}

func (r inviteRepo) Resolve(ctx context.Context, inviteID uuid.UUID, status model.InviteStatus, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	row, ok := r.s.data.invites[inviteID]
	if !ok || row.status != model.InvitePending {
		return notFound("pending invite")
	}
	row.status, row.respondedAt = status, &at
	r.s.data.invites[inviteID] = row
	return nil
}

func (r inviteRepo) RevokePending(ctx context.Context, roomID uuid.UUID, inviteeID *uuid.UUID, at time.Time) ([]model.RoomInvite, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []model.RoomInvite
	for id, row := range r.s.data.invites {
		if row.roomID != roomID || row.status != model.InvitePending || (inviteeID != nil && row.invitee != *inviteeID) {
			continue
		}
		row.status, row.respondedAt = model.InviteRevoked, &at
		r.s.data.invites[id] = row
		out = append(out, r.build(row))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (r inviteRepo) CountSentSince(ctx context.Context, inviterID uuid.UUID, since time.Time) (int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	n := 0
	for _, row := range r.s.data.invites {
		if row.inviter == inviterID && !row.createdAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (r inviteRepo) Candidates(ctx context.Context, roomID uuid.UUID, prefix string, now time.Time, limit int) ([]model.InviteCandidate, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := r.s.data
	var out []model.InviteCandidate
	for _, p := range d.peers {
		byHandle := strings.HasPrefix(p.Handle, prefix)
		if !byHandle && !strings.HasPrefix(strings.ToLower(p.UserName), prefix) {
			continue
		}
		c := model.InviteCandidate{PeerRef: r.s.ref(p.PeerID), Status: model.CandidateAvailable}
		if _, ok := d.members[memberKey{roomID, p.PeerID}]; ok {
			c.Status = model.CandidateMember
		} else if _, ok := d.bans[memberKey{roomID, p.PeerID}]; ok {
			c.Status = model.CandidateBanned
		} else {
			for _, row := range d.invites {
				if row.roomID == roomID && row.invitee == p.PeerID && row.status == model.InvitePending && row.expiresAt.After(now) {
					c.Status = model.CandidateInvited
				}
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		hi, hj := strings.HasPrefix(out[i].Handle, prefix), strings.HasPrefix(out[j].Handle, prefix)
		if hi != hj {
			return hi
		}
		return out[i].Handle < out[j].Handle
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---- Bans ----

type banRepo struct{ s *memStore }

func (r banRepo) Add(ctx context.Context, b service.NewBan) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	key := memberKey{b.RoomID, b.PeerID}
	if _, ok := r.s.data.bans[key]; ok {
		return dup("ban")
	}
	r.s.data.bans[key] = banRow{bannedBy: b.BannedBy, bannedAt: b.BannedAt}
	return nil
}

func (r banRepo) Remove(ctx context.Context, roomID, peerID uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	key := memberKey{roomID, peerID}
	if _, ok := r.s.data.bans[key]; !ok {
		return notFound("ban")
	}
	delete(r.s.data.bans, key)
	return nil
}

func (r banRepo) IsBanned(ctx context.Context, roomID, peerID uuid.UUID) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	_, ok := r.s.data.bans[memberKey{roomID, peerID}]
	return ok, nil
}

func (r banRepo) List(ctx context.Context, roomID uuid.UUID) ([]model.RoomBan, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []model.RoomBan
	for k, b := range r.s.data.bans {
		if k.room == roomID {
			out = append(out, model.RoomBan{Peer: r.s.ref(k.peer), BannedBy: b.bannedBy, BannedAt: b.bannedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BannedAt.After(out[j].BannedAt) })
	return out, nil
}
