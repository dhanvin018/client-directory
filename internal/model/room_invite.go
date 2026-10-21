package model

import (
	"time"

	"github.com/google/uuid"
)

type InviteStatus string

const (
	InvitePending  InviteStatus = "PENDING"
	InviteAccepted InviteStatus = "ACCEPTED"
	InviteDeclined InviteStatus = "DECLINED"
	InviteRevoked  InviteStatus = "REVOKED"
	InviteExpired  InviteStatus = "EXPIRED"
)

// RoomInvite invites one peer into a private room. Code is the token in the
// invite link; it only works for the invitee.
type RoomInvite struct {
	InviteID    uuid.UUID    `json:"inviteId"`
	Code        string       `json:"code"`
	RoomID      uuid.UUID    `json:"roomId"`
	RoomName    string       `json:"roomName"`
	Inviter     PeerRef      `json:"inviter"`
	Invitee     PeerRef      `json:"invitee"`
	RoomRole    RoomRoles    `json:"roomRole"`
	Status      InviteStatus `json:"status"`
	CreatedAt   time.Time    `json:"createdAt"`
	ExpiresAt   time.Time    `json:"expiresAt"`
	RespondedAt *time.Time   `json:"respondedAt,omitempty"`
}

// CandidateStatus says whether a search result can be invited to a room.
type CandidateStatus string

const (
	CandidateAvailable CandidateStatus = "AVAILABLE"
	CandidateMember    CandidateStatus = "MEMBER"
	CandidateInvited   CandidateStatus = "INVITED" // has a live pending invite
	CandidateBanned    CandidateStatus = "BANNED"
)

// InviteCandidate is one result of searching for someone to invite.
type InviteCandidate struct {
	PeerRef
	Status CandidateStatus `json:"status"`
}
