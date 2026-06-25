// Package scripts — documents_usecase wraps the existing
// DocumentsService with a single typed entry point so the pipeline
// use case can call it without depending on the handler's resolve-fn
// closure shape.
//
// Wave 14 problem #4 (June 2026): previously the handler called the
// service directly. Moving the construction here collapses the wiring
// to a single NewDocumentsUseCase call from composition.
//
// PG-029 (June 2026): DocumentsService struct + NewDocumentsService +
// CreateDoc consolidated here from the now-deleted types.go.
//
// The use case owns:
//   - DocumentsService construction with the resolved driveFolderID
//   - the typed entry point (BuildAndCreate) and its typed error
//   - a logical accessor (DocumentsService) for callers that need
//     to compose it into a Pipeline (kept for back-compat with
//     pipeline.go's NewPipeline signature, which still expects
//     *DocumentsService).
//
// The use case does NOT own:
//   - the resolveFolder closure (caller-supplied; the use case is
//     folder-resolver-agnostic)
//   - the html body content builder (BuildContent)
package scripts

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// DocumentsService creates Google Docs from script content.
type DocumentsService struct {
	docClient       interface{}
	log             *zap.Logger
	defaultFolderID string
}

// NewDocumentsService creates a new DocumentsService.
func NewDocumentsService(docClient interface{}, log interface{}, driveFolderID string) *DocumentsService {
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

// CreateDoc creates a Google Doc.
func (d *DocumentsService) CreateDoc(ctx context.Context, title, content string, resolveFolder FolderResolver, driveFolderID string) (docLink, docID string) {
	if d == nil {
		return "", ""
	}
	client, ok := d.docClient.(drive.DocClient)
	if !ok || client == nil {
		return "", ""
	}
	folderID := strings.TrimSpace(driveFolderID)
	if resolveFolder != nil && folderID != "" {
		if resolved, err := resolveFolder(ctx, folderID, d.defaultFolderID); err == nil && strings.TrimSpace(resolved) != "" {
			folderID = resolved
		}
	}
	doc, err := client.CreateDoc(ctx, title, content, folderID)
	if err != nil || doc == nil || strings.TrimSpace(doc.URL) == "" || strings.TrimSpace(doc.ID) == "" {
		return "", ""
	}
	return doc.URL, doc.ID
}

// ErrDocumentCreationFailed is the sentinel for "CreateDoc returned
// an error or empty doc-link". The typed error chain enables
// errors.Is in the pipeline_usecase + the HTTP-level error mapper.
var ErrDocumentCreationFailed = errors.New("documents: doc creation returned empty link")

// DocumentsUseCase is the typed entry point for Google Doc
// generation. Constructed once at composition; consumed by the
// pipeline use case.
type DocumentsUseCase struct {
	docClient     drive.DocClient
	log           *zap.Logger
	driveFolderID string
}

// NewDocumentsUseCase wires the use case. A nil docClient is
// allowed (no-op CreateDoc on BuildAndCreate); production always
// supplies one.
func NewDocumentsUseCase(docClient drive.DocClient, log *zap.Logger, driveFolderID string) *DocumentsUseCase {
	return &DocumentsUseCase{
		docClient:     docClient,
		log:           log,
		driveFolderID: driveFolderID,
	}
}

// DocumentsService returns the underlying service pointer for callers
// that need to compose it into a Pipeline (pipeline.NewPipeline
// still wants *DocumentsService). Returns nil if the use case was
// built with no docClient.
func (u *DocumentsUseCase) DocumentsService() *DocumentsService {
	if u == nil || u.docClient == nil {
		return nil
	}
	return NewDocumentsService(u.docClient, u.log, u.driveFolderID)
}

// BuildAndCreate is the single typed entry point: takes the
// rendered HTML content, runs DocumentsService.CreateDoc, and
// returns (docLink, docID, err).
//
// Mirrors the old DocumentsService.CreateDoc signature exactly so
// the pipeline use case can call it as a 1:1 substitute.
//
// Errors:
//   - ErrDocumentCreationFailed when the service returns empty
//     results (treated as failure even though DocumentsService itself
//     returns empty strings on internal failure — pipeline sees the
//     typed error chain).
//   - any error from DocumentsService propagated verbatim.
func (u *DocumentsUseCase) BuildAndCreate(
	ctx context.Context,
	title, content string,
	resolveFolder FolderResolver,
	driveFolderID string,
) (docLink, docID string, err error) {
	if u == nil {
		return "", "", ErrDocumentCreationFailed
	}
	svc := u.DocumentsService()
	if svc == nil {
		return "", "", ErrDocumentCreationFailed
	}
	link, id := svc.CreateDoc(ctx, title, content, resolveFolder, driveFolderID)
	if link == "" {
		return "", "", ErrDocumentCreationFailed
	}
	return link, id, nil
}
