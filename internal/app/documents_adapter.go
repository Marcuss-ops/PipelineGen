package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

type driveDocumentPublisherAdapter struct{ client drive.DocClient }

func (a *driveDocumentPublisherAdapter) Preflight(_ context.Context, folderID string) error {
	if a == nil || a.client == nil || strings.TrimSpace(folderID) == "" {
		return fmt.Errorf("GOOGLE_DOCS_UNAVAILABLE: real Drive document publisher or destination is unavailable")
	}
	return nil
}

func newDriveDocumentPublisherAdapter(client drive.DocClient) ports.DocumentPublisher {
	if client == nil {
		return nil
	}
	return &driveDocumentPublisherAdapter{client: client}
}

func (a *driveDocumentPublisherAdapter) CreateDocument(ctx context.Context, title, content, folderID, idempotencyKey string, forceRefresh bool) (ports.DocumentReference, error) {
	if a == nil || a.client == nil {
		return ports.DocumentReference{}, fmt.Errorf("drive document publisher: client is not configured")
	}
	var (
		doc *drive.Doc
		err error
	)
	if idempotencyKey != "" {
		doc, err = a.client.CreateDocIdempotent(ctx, title, content, folderID, idempotencyKey, forceRefresh)
	} else {
		doc, err = a.client.CreateDoc(ctx, title, content, folderID)
	}
	ref := ports.DocumentReference{}
	if doc != nil {
		ref = ports.DocumentReference{ID: strings.TrimSpace(doc.ID), URL: strings.TrimSpace(doc.URL)}
	}
	if err != nil {
		if ref.ID != "" && ref.URL != "" && idempotencyKey != "" && strings.Contains(strings.ToLower(err.Error()), "idempotency") {
			return ref, ports.ErrDocumentReferencePreserved
		}
		return ref, err
	}
	if ref.ID == "" || ref.URL == "" {
		return ports.DocumentReference{}, fmt.Errorf("drive document publisher: provider returned incomplete document reference")
	}
	return ref, nil
}

func (a *driveDocumentPublisherAdapter) UpdateDocument(ctx context.Context, docID, title, content string) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("drive document publisher: client is not configured")
	}
	return a.client.UpdateDoc(ctx, docID, title, content)
}

var _ ports.DocumentPublisher = (*driveDocumentPublisherAdapter)(nil)
