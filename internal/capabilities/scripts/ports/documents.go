package ports

import (
	"context"
	"errors"
)

// FolderResolver maps a logical folder ID to the canonical destination.
// Concrete Drive/folder resolution belongs to the composition root.
type FolderResolver func(ctx context.Context, folderID, defaultFolderID string) (string, error)

// DocumentReference is the provider-neutral result of publishing a document.
type DocumentReference struct {
	ID  string
	URL string
}

// ErrDocumentReferencePreserved marks the narrow provider case where the
// document exists but a post-create idempotency annotation failed. Consumers
// may safely use the reference and must not retry publication as a new create.
var ErrDocumentReferencePreserved = errors.New("documents: reference preserved after non-fatal idempotency annotation failure")

// DocumentPublisher is the application-owned provider publication port.
// Provider-specific clients and DTOs stay behind composition-root adapters.
type DocumentPublisher interface {
	CreateDocument(ctx context.Context, title, content, folderID, idempotencyKey string, forceRefresh bool) (DocumentReference, error)
	UpdateDocument(ctx context.Context, docID, title, content string) error
}

// DocumentsService is the application-owned document postprocessing contract.
// It is separate from DocumentPublisher so the processor remains independent
// of the use-case implementation and of any provider DTO.
type DocumentsService interface {
	CreateDoc(ctx context.Context, title, content string, resolveFolder FolderResolver, folderID, idempotencyKey string, forceRefresh bool) (link, id string, err error)
	UpdateDoc(ctx context.Context, docID, title, content string) error
}
