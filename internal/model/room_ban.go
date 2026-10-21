package model

import (
	"time"

	"github.com/google/uuid"
)

// RoomBan keeps a peer out of a room until an owner lifts it.
type RoomBan struct {
	Peer     PeerRef   `json:"peer"`
	BannedBy uuid.UUID `json:"bannedBy"`
	BannedAt time.Time `json:"bannedAt"`
}
