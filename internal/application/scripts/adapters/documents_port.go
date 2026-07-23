// Package adapters — documents_port.go holds the document-service port
// contracts consumed by the downstream document use-case.
//
// Keeping these types in their own file lets processor_document.go
// depend on them without redeclaring them, avoiding the
// redeclaration conflicts that happen when the same alias lives in
// multiple files.
package adapters

import "context"

// FolderResolver translates a logical folder ID into the canonical
// Drive folder to use. The resolver may return the input verbatim
// when no rewriting is needed.
type FolderResolver = func(ctx context.Context, folderID, defaultFolderID string) (string, error)

// DocumentsService is the minimal document-service contract the
// DocumentsProcessor relies on. Production wiring injects the
// concrete *usecase.DocumentsService (whose method set satisfies
// this interface) - verified at composition time via direct pointer
// assignment.
type DocumentsService interface {
	// CreateDoc creates a Google Doc. idempotencyKey is used to
	// avoid duplicate documents on retry; forceRefresh forces an
	// update of an existing doc instead of reusing it.
	CreateDoc(ctx context.Context, title, content string, resolveFolder FolderResolver, driveFolderID, idempotencyKey string, forceRefresh bool) (link, id string)
	// UpdateDoc overwrites the content of an existing Google Doc.
	UpdateDoc(ctx context.Context, docID, title, content string) error
}
