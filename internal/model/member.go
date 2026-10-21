package model

import (
	"time"

	"github.com/google/uuid"
)

type Member struct {
	PeerID    uuid.UUID           `json:"peerId"`
	RoomRole  RoomRoles           `json:"roomRole"`
	DocRoles  map[string]DocRoles `json:"docRoles,omitempty"`
	InvitedBy uuid.UUID           `json:"invitedBy"`
	JoinedAt  time.Time           `json:"joinedAt"`
}
