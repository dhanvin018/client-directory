package model

import "github.com/google/uuid"

type Peer struct {
	PeerID       uuid.UUID `json:"peerId"`
	Handle       string    `json:"handle"` // unique, lowercase; how other people find this peer
	UserName     string    `json:"userName"`
	AuthProvider string    `json:"-"`
	AuthSubject  string    `json:"-"`
}

// PeerRef is the public face of a peer, embedded where another record points
// at one (an invite's inviter, a ban's target, a search result).
type PeerRef struct {
	PeerID   uuid.UUID `json:"peerId"`
	Handle   string    `json:"handle"`
	UserName string    `json:"userName"`
}
