package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/model"
)

// DocumentService manages document references and metadata. The directory
// never holds document content, so "clone" copies the reference only; the
// content is copied peer-side.
type DocumentService struct{ base }

type CreateDocumentInput struct {
	Title        string
	PositionRank string // fractional index, supplied by the client
}

type CloneDocumentInput struct {
	SourceDocID  uuid.UUID // must be in the same room
	Title        *string   // optional; defaults to "Copy of <source title>"
	PositionRank string
}

type UpdateDocumentInput struct {
	Title        *string
	PositionRank *string
}

// createRows inserts the document row and makes the creator its DOC_OWNER.
func (s *DocumentService) createRows(
	ctx context.Context, r Repos,
	actor, roomID uuid.UUID,
	title, rank string,
	clonedFrom *uuid.UUID,
) (*model.DocumentRef, uuid.UUID, error) {
	docID := s.newID()
	doc, err := r.Documents().Insert(ctx, NewDocument{
		DocID:        docID,
		RoomID:       roomID,
		Title:        title,
		PositionRank: rank,
		AddedBy:      actor,
		AddedAt:      s.now(),
		ClonedFrom:   clonedFrom,
	})
	if err != nil {
		return nil, uuid.Nil, err
	}
	if err := r.Members().SetDocRole(ctx, roomID, docID, actor, model.DocRoleOwner); err != nil {
		return nil, uuid.Nil, err
	}
	return doc, docID, nil
}

// Create is open to any room member; the creator becomes the document's
// DOC_OWNER. Records CREATE_DOC.
func (s *DocumentService) Create(ctx context.Context, actor, roomID uuid.UUID, in CreateDocumentInput) (*model.DocumentRef, error) {
	title, err := cleanText("title", in.Title, maxDocTitleLen)
	if err != nil {
		return nil, err
	}
	rank, err := cleanRank(in.PositionRank)
	if err != nil {
		return nil, err
	}

	var doc *model.DocumentRef
	err = s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		if _, err := requireMember(ctx, r, roomID, actor); err != nil {
			return err
		}

		created, docID, err := s.createRows(ctx, r, actor, roomID, title, rank, nil)
		if err != nil {
			return err
		}
		doc = created

		return s.event(ctx, r, roomID, actor, model.EventCreateDoc, nil, &docID, map[string]any{
			"title":        title,
			"positionRank": rank,
		})
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// Clone is open to any room member. Records CLONE_DOC.
func (s *DocumentService) Clone(ctx context.Context, actor, roomID uuid.UUID, in CloneDocumentInput) (*model.DocumentRef, error) {
	rank, err := cleanRank(in.PositionRank)
	if err != nil {
		return nil, err
	}
	var titleOverride *string
	if in.Title != nil {
		t, err := cleanText("title", *in.Title, maxDocTitleLen)
		if err != nil {
			return nil, err
		}
		titleOverride = &t
	}

	var doc *model.DocumentRef
	err = s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		if _, err := requireMember(ctx, r, roomID, actor); err != nil {
			return err
		}

		source, err := r.Documents().Get(ctx, roomID, in.SourceDocID) // ErrNotFound if not in this room
		if err != nil {
			return err
		}
		title := truncate("Copy of "+source.Title, maxDocTitleLen)
		if titleOverride != nil {
			title = *titleOverride
		}

		sourceID := in.SourceDocID
		created, docID, err := s.createRows(ctx, r, actor, roomID, title, rank, &sourceID)
		if err != nil {
			return err
		}
		doc = created

		return s.event(ctx, r, roomID, actor, model.EventCloneDoc, nil, &docID, map[string]any{
			"title":        title,
			"positionRank": rank,
			"clonedFrom":   sourceID.String(),
		})
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *DocumentService) List(ctx context.Context, actor, roomID uuid.UUID) ([]model.DocumentRef, error) {
	if _, err := requireMember(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	return s.store.Documents().List(ctx, roomID)
}

func (s *DocumentService) Get(ctx context.Context, actor, roomID, docID uuid.UUID) (*model.DocumentRef, error) {
	if _, err := requireMember(ctx, s.store, roomID, actor); err != nil {
		return nil, err
	}
	return s.store.Documents().Get(ctx, roomID, docID)
}

// Update requires a document manager (DOC_OWNER of the doc, or room OWNER).
// A title change records RENAME_DOC; reordering is a plain update.
func (s *DocumentService) Update(ctx context.Context, actor, roomID, docID uuid.UUID, in UpdateDocumentInput) (*model.DocumentRef, error) {
	if in.Title == nil && in.PositionRank == nil {
		return nil, invalid("nothing to update")
	}

	var patch DocumentPatch
	if in.Title != nil {
		title, err := cleanText("title", *in.Title, maxDocTitleLen)
		if err != nil {
			return nil, err
		}
		patch.Title = &title
	}
	if in.PositionRank != nil {
		rank, err := cleanRank(*in.PositionRank)
		if err != nil {
			return nil, err
		}
		patch.PositionRank = &rank
	}

	var updated *model.DocumentRef
	err := s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		if err := requireDocManager(ctx, r, roomID, docID, actor); err != nil {
			return err
		}
		current, err := r.Documents().Get(ctx, roomID, docID)
		if err != nil {
			return err
		}

		updated, err = r.Documents().Update(ctx, roomID, docID, patch)
		if err != nil {
			return err
		}

		if patch.Title != nil && *patch.Title != current.Title {
			return s.event(ctx, r, roomID, actor, model.EventRenameDoc, nil, &docID, map[string]any{
				"oldTitle": current.Title,
				"newTitle": *patch.Title,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// Delete requires a document manager. The title is snapshotted into the
// DELETE_DOC event because the document row is gone afterwards.
func (s *DocumentService) Delete(ctx context.Context, actor, roomID, docID uuid.UUID) error {
	return s.store.WithTx(ctx, func(r Repos) error {
		if _, err := r.Rooms().LockForUpdate(ctx, roomID); err != nil {
			return err
		}
		if err := requireDocManager(ctx, r, roomID, docID, actor); err != nil {
			return err
		}
		doc, err := r.Documents().Get(ctx, roomID, docID)
		if err != nil {
			return err
		}
		if err := r.Documents().Delete(ctx, roomID, docID); err != nil {
			return err
		}
		return s.event(ctx, r, roomID, actor, model.EventDeleteDoc, nil, &docID, map[string]any{
			"title": doc.Title,
		})
	})
}
