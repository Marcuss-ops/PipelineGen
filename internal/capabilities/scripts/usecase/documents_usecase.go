// Package usecase owns document publication orchestration without provider knowledge.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
)

// DocumentsService publishes application-generated document content.
type DocumentsService struct {
	publisher       ports.DocumentPublisher
	log             *zap.Logger
	defaultFolderID string
}

// NewDocumentsService creates a service backed by an application publisher port.
func NewDocumentsService(publisher ports.DocumentPublisher, log *zap.Logger, driveFolderID string) *DocumentsService {
	if log == nil {
		log = zap.NewNop()
	}
	return &DocumentsService{publisher: publisher, log: log, defaultFolderID: driveFolderID}
}

// CreateDoc creates or reuses a document. Required dependencies and provider
// references are validated explicitly; unavailable providers never look like success.
func (d *DocumentsService) CreateDoc(ctx context.Context, title, content string, resolveFolder ports.FolderResolver, driveFolderID, idempotencyKey string, forceRefresh bool) (string, string, error) {
	if d == nil || d.publisher == nil {
		return "", "", ErrDocumentPublisherUnavailable
	}
	folderID := strings.TrimSpace(driveFolderID)
	if resolveFolder != nil && folderID != "" {
		resolved, err := resolveFolder(ctx, folderID, d.defaultFolderID)
		if err != nil {
			return "", "", fmt.Errorf("resolve document folder: %w", err)
		}
		resolved = strings.TrimSpace(resolved)
		if resolved == "" {
			return "", "", errors.New("resolve document folder: empty folder reference")
		}
		folderID = resolved
	}
	d.log.Info("CreateDoc: publishing document",
		zap.String("title", title), zap.String("folderID", folderID),
		zap.String("idempotencyKey", idempotencyKey), zap.Bool("forceRefresh", forceRefresh))
	ref, err := d.publisher.CreateDocument(ctx, title, content, folderID, idempotencyKey, forceRefresh)
	if err != nil {
		if errors.Is(err, ports.ErrDocumentReferencePreserved) && strings.TrimSpace(ref.ID) != "" && strings.TrimSpace(ref.URL) != "" {
			return strings.TrimSpace(ref.URL), strings.TrimSpace(ref.ID), nil
		}
		return "", "", fmt.Errorf("publish document: %w", err)
	}
	if strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.URL) == "" {
		return "", "", ErrDocumentCreationFailed
	}
	return strings.TrimSpace(ref.URL), strings.TrimSpace(ref.ID), nil
}

// UpdateDoc overwrites an existing document through the application port.
func (d *DocumentsService) UpdateDoc(ctx context.Context, docID, title, content string) error {
	if d == nil || d.publisher == nil {
		return ErrDocumentPublisherUnavailable
	}
	return d.publisher.UpdateDocument(ctx, docID, title, content)
}

var ErrDocumentPublisherUnavailable = errors.New("documents: document publisher is not configured")
var ErrDocumentCreationFailed = errors.New("documents: document publisher returned an incomplete reference")

// DocumentsUseCase is the typed document-generation entry point.
type DocumentsUseCase struct {
	publisher     ports.DocumentPublisher
	log           *zap.Logger
	driveFolderID string
}

func NewDocumentsUseCase(publisher ports.DocumentPublisher, log *zap.Logger, driveFolderID string) *DocumentsUseCase {
	return &DocumentsUseCase{publisher: publisher, log: log, driveFolderID: driveFolderID}
}

func (u *DocumentsUseCase) DocumentsService() *DocumentsService {
	if u == nil {
		return nil
	}
	return NewDocumentsService(u.publisher, u.log, u.driveFolderID)
}

func (u *DocumentsUseCase) BuildAndCreate(ctx context.Context, title, content string, resolveFolder ports.FolderResolver, driveFolderID string) (string, string, error) {
	if u == nil {
		return "", "", ErrDocumentPublisherUnavailable
	}
	svc := u.DocumentsService()
	if svc == nil {
		return "", "", ErrDocumentPublisherUnavailable
	}
	return svc.CreateDoc(ctx, title, content, resolveFolder, driveFolderID, "", false)
}
