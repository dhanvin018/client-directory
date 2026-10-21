-- Room discovery: case-insensitive prefix search over public rooms only.
-- Private rooms are never searchable, so they are left out of the index.

CREATE INDEX idx_rooms_public_name_prefix
    ON rooms (lower(name) text_pattern_ops)
    WHERE visibility = 'PUBLIC';
