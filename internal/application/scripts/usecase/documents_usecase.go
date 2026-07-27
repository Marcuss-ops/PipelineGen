// Package scripts — documents_usecase wraps the existing
// DocumentsService with a single typed entry point so the pipeline
// use case can call it without depending on the handler's resolve-fn
// closure shape.
package usecase

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// FolderResolver translates a logical folder ID into the canonical
// Drive folder to use. Aliased to the unaliased func type so the
// adapters package can use the same compile-time shape without
// importing usecase (cycle).

// DocumentsService creates Google Docs from script content.
type DocumentsService struct {
	docClient       any
	log             *zap.Logger
	defaultFolderID string
}

// NewDocumentsService creates a new DocumentsService.
func NewDocumentsService(docClient any, log any, driveFolderID string) *DocumentsService {
	var logger *zap.Logger
	if l, ok := log.(*zap.Logger); ok {
		logger = l
	}
	return &DocumentsService{
		docClient:       docClient,
		log:             logger,
		defaultFolderID: driveFolderID,
	}
}

// CreateDoc creates or reuses a Google Doc. When idempotencyKey is
// non-empty, an existing doc tagged with that key is returned unless
// forceRefresh is true, in which case the existing doc is updated.
func (d *DocumentsService) CreateDoc(ctx context.Context, title, content string, resolveFolder adapters.FolderResolver, driveFolderID, idempotencyKey string, forceRefresh bool) (docLink, docID string) {
	if d == nil {
		return "", ""
	}
	client, ok := d.docClient.(drive.DocClient)
	if !ok || client == nil {
		if d.log != nil {
			d.log.Warn("CreateDoc: DocClient type assertion failed or nil")
		}
		return "", ""
	}
	folderID := strings.TrimSpace(driveFolderID)
	if resolveFolder != nil && folderID != "" {
		if resolved, err := resolveFolder(ctx, folderID, d.defaultFolderID); err == nil && strings.TrimSpace(resolved) != "" {
			folderID = resolved
		}
	}
	if d.log != nil {
		d.log.Info("CreateDoc: calling DocClient.CreateDocIdempotent",
			zap.String("title", title),
			zap.String("folderID", folderID),
			zap.String("idempotencyKey", idempotencyKey),
			zap.Bool("forceRefresh", forceRefresh))
	}

	var doc *drive.Doc
	var err error
	if idempotencyKey != "" {
		doc, err = client.CreateDocIdempotent(ctx, title, content, folderID, idempotencyKey, forceRefresh)
	} else {
		doc, err = client.CreateDoc(ctx, title, content, folderID)
	}
	if err != nil {
		// CreateDocIdempotent may return a valid document together with an
		// error when the document was created but its idempotency property
		// could not be written. Preserve the usable publication result;
		// discarding it would report a false document failure and could cause
		// a retry to create a duplicate document.
		if doc != nil && strings.TrimSpace(doc.URL) != "" && strings.TrimSpace(doc.ID) != "" {
			if d.log != nil {
				d.log.Warn("CreateDoc: document created but idempotency tagging failed; preserving document reference", zap.Error(err))
			}
			return doc.URL, doc.ID
		}
		if d.log != nil {
			d.log.Warn("CreateDoc: DocClient doc creation error", zap.Error(err))
		}
		return "", ""
	}
	if doc == nil || strings.TrimSpace(doc.URL) == "" || strings.TrimSpace(doc.ID) == "" {
		if d.log != nil {
			d.log.Warn("CreateDoc: empty result")
		}
		return "", ""
	}
	return doc.URL, doc.ID
}

// UpdateDoc overwrites the content of an existing Google Doc.
func (d *DocumentsService) UpdateDoc(ctx context.Context, docID, title, content string) error {
	if d == nil {
		return ErrDocumentCreationFailed
	}
	client, ok := d.docClient.(drive.DocClient)
	if !ok || client == nil {
		if d.log != nil {
			d.log.Warn("UpdateDoc: DocClient type assertion failed or nil")
		}
		return ErrDocumentCreationFailed
	}
	if d.log != nil {
		d.log.Info("UpdateDoc: calling DocClient.UpdateDoc", zap.String("docID", docID))
	}
	return client.UpdateDoc(ctx, docID, title, content)
}

// ErrDocumentCreationFailed is the sentinel for "CreateDoc returned
// an error or empty doc-link".
var ErrDocumentCreationFailed = errors.New("documents: doc creation returned empty link")

// DocumentsUseCase is the typed entry point for Google Doc generation.
type DocumentsUseCase struct {
	docClient     drive.DocClient
	log           *zap.Logger
	driveFolderID string
}

// NewDocumentsUseCase wires the use case.
func NewDocumentsUseCase(docClient drive.DocClient, log *zap.Logger, driveFolderID string) *DocumentsUseCase {
	return &DocumentsUseCase{
		docClient:     docClient,
		log:           log,
		driveFolderID: driveFolderID,
	}
}

// DocumentsService returns the underlying service pointer for callers
// that need to compose it into a Pipeline.
func (u *DocumentsUseCase) DocumentsService() *DocumentsService {
	if u == nil || u.docClient == nil {
		return nil
	}
	return NewDocumentsService(u.docClient, u.log, u.driveFolderID)
}

// BuildAndCreate is the single typed entry point.
func (u *DocumentsUseCase) BuildAndCreate(
	ctx context.Context,
	title, content string,
	resolveFolder adapters.FolderResolver,
	driveFolderID string,
) (docLink, docID string, err error) {
	if u == nil {
		return "", "", ErrDocumentCreationFailed
	}
	svc := u.DocumentsService()
	if svc == nil {
		return "", "", ErrDocumentCreationFailed
	}
	link, id := svc.CreateDoc(ctx, title, content, resolveFolder, driveFolderID, "", false)
	if link == "" {
		return "", "", ErrDocumentCreationFailed
	}
	return link, id, nil
}
