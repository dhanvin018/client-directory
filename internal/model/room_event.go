package model

import (
	"time"

	"github.com/google/uuid"
)

type RoomEventType string

const (
	EventRoomCreated    RoomEventType = "ROOM_CREATED"
	EventCreateDoc      RoomEventType = "CREATE_DOC"
	EventDeleteDoc      RoomEventType = "DELETE_DOC"
	EventCloneDoc       RoomEventType = "CLONE_DOC"
	EventAddMember      RoomEventType = "ADD_MEMBER"
	EventRemoveMember   RoomEventType = "REMOVE_MEMBER"
	EventRenameRoom     RoomEventType = "RENAME_ROOM"
	EventRenameDoc      RoomEventType = "RENAME_DOC"
	EventUpdateRoomRole RoomEventType = "UPDATE_ROOM_ROLE"
	EventUpdateDocRole  RoomEventType = "UPDATE_DOC_ROLE"
	EventInviteCreated  RoomEventType = "INVITE_CREATED"
	EventInviteDeclined RoomEventType = "INVITE_DECLINED"
	EventInviteRevoked  RoomEventType = "INVITE_REVOKED"
	EventBanMember      RoomEventType = "BAN_MEMBER"
	EventUnbanMember    RoomEventType = "UNBAN_MEMBER"
)

type RoomEvent struct {
	RoomID      uuid.UUID      `json:"roomId"`
	EventID     uint64         `json:"eventId"`
	ActorID     uuid.UUID      `json:"actorId"`
	TargetID    uuid.UUID      `json:"targetId"`
	TargetDocID uuid.UUID      `json:"targetDocId"`
	EventType   RoomEventType  `json:"eventType"`
	Details     map[string]any `json:"details,omitempty"`
	Timestamp   time.Time      `json:"timestamp"`
}
