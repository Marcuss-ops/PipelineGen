// Package usecase — documents_service.go: retired no-op documents service.
//
// Sprint 1.0 retired the inline Google-Doc creation path; document
// generation is now produced by the downstream document.generate job.
// This stub satisfies legacy wiring in wire_script_adapters.go.
package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"go.uber.org/zap"
)

// DocumentsService is a retired no-op service. It satisfies the call
// site in docCreatorImpl.CreateDoc so legacy composition wiring compiles.
type DocumentsService struct {
	docClient delivery.DocPublisher
	log       *zap.Logger
	folderID  string
}

// NewDocumentsService returns a no-op DocumentsService.
func NewDocumentsService(docClient delivery.DocPublisher, log *zap.Logger, folderID string) *DocumentsService {
	if log == nil {
		log = zap.NewNop()
	}
	return &DocumentsService{docClient: docClient, log: log, folderID: folderID}
}

// CreateDoc is a retired no-op. It returns empty strings.
func (s *DocumentsService) CreateDoc(_ context.Context, _, _ string, _ func(ctx context.Context, input, defaultRootID string) (string, error), _, _ string, _ bool) (string, string) {
	return "", ""
}
