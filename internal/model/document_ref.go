package model

import (
	"time"

	"github.com/google/uuid"
)

type DocumentRef struct {
	DocID        uuid.UUID  `json:"docId"`
	Title        string     `json:"title"`
	PositionRank string     `json:"positionRank"`
	AddedByPeer  uuid.UUID  `json:"addedByPeer"`
	AddedAt      time.Time  `json:"addedAt"`
	ClonedFrom   *uuid.UUID `json:"clonedFrom,omitempty"`
}
