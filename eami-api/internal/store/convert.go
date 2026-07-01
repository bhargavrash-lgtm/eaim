package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// uuidFromPgtype converts a pgtype.UUID to uuid.UUID, returning uuid.Nil if invalid.
func uuidFromPgtype(u pgtype.UUID) uuid.UUID {
	if !u.Valid {
		return uuid.Nil
	}
	return uuid.UUID(u.Bytes)
}

// uuidPtrFromPgtype returns nil if the UUID is invalid (NULL in DB).
func uuidPtrFromPgtype(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}

// textPtr returns nil if the pgtype.Text is invalid (NULL in DB).
func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

// timePtr returns nil if the pgtype.Timestamptz is invalid (NULL in DB).
func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// int4Ptr returns nil if the pgtype.Int4 is invalid (NULL in DB).
func int4Ptr(i pgtype.Int4) *int32 {
	if !i.Valid {
		return nil
	}
	return &i.Int32
}

// toPgtypeUUID converts a uuid.UUID to pgtype.UUID.
func toPgtypeUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// toPgtypeText converts a string pointer to pgtype.Text.
func toPgtypeText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *s, Valid: true}
}

// toPgtypeInt4 converts an int pointer to pgtype.Int4.
func toPgtypeInt4(i *int) pgtype.Int4 {
	if i == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*i), Valid: true}
}
