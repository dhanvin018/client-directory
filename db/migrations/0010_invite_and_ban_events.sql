-- Audit events for invites and bans. Joining (by invite or by public join)
-- reuses ADD_MEMBER, with details.via saying how the member got in.
--
-- The original CHECK was declared inline on the column, so Postgres named it
-- room_events_event_type_check.

ALTER TABLE room_events DROP CONSTRAINT room_events_event_type_check;

ALTER TABLE room_events ADD CONSTRAINT room_events_event_type_check
    CHECK (event_type IN (
        'ROOM_CREATED',
        'CREATE_DOC',
        'DELETE_DOC',
        'CLONE_DOC',
        'ADD_MEMBER',
        'REMOVE_MEMBER',
        'UPDATE_ROLE',        -- legacy: superseded by the two below, drop if unused
        'RENAME_ROOM',
        'RENAME_DOC',
        'UPDATE_ROOM_ROLE',
        'UPDATE_DOC_ROLE',
        'INVITE_CREATED',
        'INVITE_DECLINED',
        'INVITE_REVOKED',
        'BAN_MEMBER',
        'UNBAN_MEMBER'
    ));
