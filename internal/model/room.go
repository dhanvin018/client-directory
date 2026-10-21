package model

import (
	"time"

	"github.com/google/uuid"
)

type Visibility string

const (
	VisibilityPublic  Visibility = "PUBLIC"
	VisibilityPrivate Visibility = "PRIVATE"
)

type RoomRoles string

const (
	RoleOwner       RoomRoles = "OWNER"
	RoleParticipant RoomRoles = "PARTICIPANT"
)

type DocRoles string

const (
	DocRoleOwner  DocRoles = "DOC_OWNER"
	DocRoleEditor DocRoles = "EDITOR"
	DocRoleViewer DocRoles = "VIEWER"
)

type Room struct {
	RoomID         uuid.UUID     `json:"roomId"`
	Name           string        `json:"name"`
	CreatedAt      time.Time     `json:"createdAt"`
	Members        []Member      `json:"members,omitempty"`
	Documents      []DocumentRef `json:"documents,omitempty"`
	Visibility     Visibility    `json:"visibility"`
	DefaultDocRole DocRoles      `json:"defaultDocRole"`
}

// RoomSummary is what a non-member may see of a public room before joining.
type RoomSummary struct {
	RoomID      uuid.UUID  `json:"roomId"`
	Name        string     `json:"name"`
	Visibility  Visibility `json:"visibility"`
	MemberCount int        `json:"memberCount"`
	IsMember    bool       `json:"isMember"` // relative to the caller
}
