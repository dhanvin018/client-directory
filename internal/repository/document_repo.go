package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

type documentRepo struct{ q dbtx }

func (r documentRepo) Insert(ctx context.Context, d service.NewDocument) (*model.DocumentRef, error) {
	return scanDocument(r.q.QueryRowContext(ctx,
		`INSERT INTO document_refs
		     (doc_id, room_id, title, position_rank, added_by, added_at, cloned_from_doc_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+docCols,
		d.DocID, d.RoomID, d.Title, d.PositionRank, d.AddedBy, d.AddedAt, nullUUID(d.ClonedFrom)))
}

// Get returns ErrNotFound if the document does not exist in this room.
func (r documentRepo) Get(ctx context.Context, roomID, docID uuid.UUID) (*model.DocumentRef, error) {
	return scanDocument(r.q.QueryRowContext(ctx,
		`SELECT `+docCols+` FROM document_refs WHERE room_id = $1 AND doc_id = $2`,
		roomID, docID))
}

// List orders by position_rank, which is COLLATE "C" in the schema, so the
// order is byte-wise as fractional indexing requires. doc_id breaks ties.
func (r documentRepo) List(ctx context.Context, roomID uuid.UUID) ([]model.DocumentRef, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+docCols+` FROM document_refs
		 WHERE room_id = $1
		 ORDER BY position_rank, doc_id`, roomID)
	if err != nil {
		return nil, translate(err)
	}
	return collect(rows, scanDocument)
}

// Update changes only the non-nil fields of the patch.
func (r documentRepo) Update(ctx context.Context, roomID, docID uuid.UUID, p service.DocumentPatch) (*model.DocumentRef, error) {
	return scanDocument(r.q.QueryRowContext(ctx,
		`UPDATE document_refs
		 SET title         = COALESCE($3::text, title),
		     position_rank = COALESCE($4::text, position_rank)
		 WHERE room_id = $1 AND doc_id = $2
		 RETURNING `+docCols,
		roomID, docID, nullText(p.Title), nullText(p.PositionRank)))
}

// Delete also removes the document's per-member roles (ON DELETE CASCADE).
func (r documentRepo) Delete(ctx context.Context, roomID, docID uuid.UUID) error {
	return affectedOne(r.q.ExecContext(ctx,
		`DELETE FROM document_refs WHERE room_id = $1 AND doc_id = $2`,
		roomID, docID))
}
