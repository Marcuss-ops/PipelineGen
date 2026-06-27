// Package scripts — service interfaces extracted from types.go (PG-029, June 2026).
package scripts

import (
	"context"

	"go.uber.org/zap"
)

// ── ClipServices ─────────────────────────────────────────────────────────

// ClipServices bundles the service dependencies that the script-flow use cases
// actually consume in production wiring (PR2 2b/c, June 2026). The historical
// shape carried 13 typed ports; this struct keeps only the 5 fields actually
// populated by WireScriptFlow (post-merge). The dropped ports — realtime
// search, association, harvest, image-gen, etc. — depended on packages
// removed from origin (commit d61068b3) and would have been permanently nil,
// short-circuiting every consumer.
type ClipServices struct {
	Logger        *zap.Logger
	Translator    TranslatorService
	DriveSvc      DriveCheckService
	ArtlistFolder string
	MetadataModel string
}

// ── Service interfaces ───────────────────────────────────────────────────

// DriveCheckService narrows drive check operations.
type DriveCheckService interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// TranslatorService narrows translator operations with model support.
type TranslatorService interface {
	TranslateTextWithModel(ctx context.Context, text, lang, model string) (string, error)
}

// RealtimeSearchService and AssocSearchService remain as typed-nil
// compatibility ports for handlers that still carry the fields in their
// constructor surfaces. The live pipeline no longer invokes methods on
// these ports, so they stay empty by design.
type RealtimeSearchService interface{}
type AssocSearchService interface{}
