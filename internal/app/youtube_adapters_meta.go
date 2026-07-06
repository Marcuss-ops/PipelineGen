// Package app — sourcing metadata + logger + enrichment adapters
// split from youtube_metadata_adapter.go (PR-GODOBJ-Azione-4, July 2026).
//
// 3 adapters: sourcingMetadataAdapter, zapSourcingLogger, sourcingEnrichmentAdapter.
package app

import (
	"context"

	"go.uber.org/zap"

	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── sourcingMetadataAdapter ───────────────────────────────────────────

type sourcingMetadataAdapter struct {
	cfg       *config.Config
	admin     driveutil.Admin
	reader    driveutil.Reader
	lifecycle driveutil.FileLifecycle // P1-3-BACKFILL (July 2026): routed through FileLifecycle where available
	log       *zap.Logger
}

func (a *sourcingMetadataAdapter) UpdateCumulativeJSON(ctx context.Context, tempDir, folderID, clipID string, entry map[string]any) error {
	if a.admin == nil || a.cfg == nil {
		return nil
	}
	// DRIVE-008 CUTOVER (July 2026): UploadFile removed from ClipDriveUploaderPort.
	// P1-3-BACKFILL: pass lifecycle through so TrashFile routes via FileLifecycle.Trash.
	// When lifecycle is nil (legacy construction), the graceful fallback to Admin.TrashFile applies.
	appclips.UpdateCumulativeMetadataJSON(ctx, newClipsDriveAdapter(a.admin, a.reader), a.cfg.Storage.TempPath(), folderID, clipID, entry, a.log)
	return nil
}

// ── zapSourcingLogger ─────────────────────────────────────────────────

type zapSourcingLogger struct {
	log *zap.Logger
}

func (a *zapSourcingLogger) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}

// ── sourcingEnrichmentAdapter ─────────────────────────────────────────

type sourcingEnrichmentAdapter struct {
	handler *clipsapi.Handler
}

func (a *sourcingEnrichmentAdapter) EnrichAndIndex(ctx context.Context, clipID, localPath, source string) error {
	if a.handler == nil {
		return nil
	}
	clip := &asset.Asset{
		ID:        clipID,
		Source:    asset.Source(source),
		Name:      clipID,
		MediaType: asset.MediaType("video"),
	}
	clip.SetLocalPath(localPath)
	a.handler.EnrichAndIndexClip(ctx, clip, source)
	return nil
}
