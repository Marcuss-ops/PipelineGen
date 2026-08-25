// Package app — sourcing metadata + logger + enrichment adapters
// split from youtube_metadata_adapter.go (PR-GODOBJ-Azione-4, July 2026).
//
// 3 adapters: SourcingMetadataAdapter, ZapSourcingLogger, SourcingEnrichmentAdapter.
package adapters

import (
	"context"

	"go.uber.org/zap"

	sourcing "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// ── SourcingMetadataAdapter ───────────────────────────────────────────

type SourcingMetadataAdapter struct {
	cfg       *config.Config
	admin     driveutil.Admin
	reader    driveutil.Reader
	lifecycle driveutil.FileLifecycle // P1-3-BACKFILL (July 2026): routed through FileLifecycle where available
	publisher delivery.Publisher      // P0-#1 atomic-RMW (July 2026): threaded to UpdateCumulativeMetadataJSON for atomic publish
	log       *zap.Logger
}

// UpdateCumulativeJSON delegates to appclips.UpdateCumulativeMetadataJSON
// with the atomic-RMW flow (P0-#1 cutover, July 2026). The pre-P0-#1
// implementation trashed the old metadata.json BEFORE publishing the new
// one (which was silently skipped), permanently losing the sidecar. The
// new flow reads the old file, builds the merged JSON, and publishes
// the new file via delivery.Publisher.Publish with ConflictOverwrite —
// the publisher's atomic Files.Update replaces the existing sidecar in
// place or returns an error WITHOUT touching the existing file. The
// per-video cleanup runs only AFTER the new sidecar is live.
//
// Returns:
//   - sourcing.ErrSourcingUpdateCumulativeDisabled when admin or cfg
//     is nil (fail-closed at the adapter boundary; PR-SOURCING-ADAPTER-FAIL-CLOSED
//     July 2026 — pre-fix this was a silent-success nil return, masking
//     composition bugs from upstream callers).
//   - appclips.ErrMetadataDriveNotConfigured when publisher is nil
//     (Drive not configured; the clip registration pipeline can
//     branch on this sentinel via errors.Is to decide whether to
//     fail or log a soft warning).
//   - Any error propagated from appclips.UpdateCumulativeMetadataJSON
//     (list / download / decode / marshal / write-temp / publish).
//
// godlike/07 NO-FAKE-AVAILABILITY: the adapter is the canonical SOLE owner
// of the fail-closed return at the adapter boundary; upstream callers MUST
// probe via errors.Is(err, sourcing.ErrSourcingUpdateCumulativeDisabled)
// — NEVER assume a nil return means "no-op succeeded".
func (a *SourcingMetadataAdapter) UpdateCumulativeJSON(ctx context.Context, tempDir, folderID, clipID string, entry map[string]any) error {
	if a.admin == nil || a.cfg == nil {
		// PR-SOURCING-ADAPTER-FAIL-CLOSED (July 2026): fail-closed
		// pre-return — replaces the pre-fix silent-success nil.
		// The canonical composition-time invariant is admin AND cfg
		// both non-nil; if either is nil, the canonical wiring path
		// was bypassed and the caller MUST observe the failure as a
		// typed-sentinel rather than as a no-op success.
		return sourcing.ErrSourcingUpdateCumulativeDisabled
	}
	// P0-#1 (July 2026): the function now returns real errors. The
	// pre-fix adapter hard-returned nil which masked the silent
	// data-loss window in production. Upstream callers
	// (e.g. internal/application/assets/sourcing/youtube/register_helpers.go)
	// currently `_ = s.metadata.UpdateCumulativeJSON(...)` so the
	// returned error is observed at the adapter boundary but not
	// propagated to the registration handler. The registration
	// handler continues to succeed (the canonical clip ledger is
	// dispatcher.EnqueueAndIndex, not the Drive sidecar), and the
	// error is logged here at the adapter for observability.
	//
	// If the publisher is nil (Drive not configured at composition
	// time), surface the typed sentinel so the upstream caller can
	// branch on errors.Is if it wants to fail-closed in the future.
	if a.publisher == nil {
		return appclips.ErrMetadataDriveNotConfigured
	}
	// P0-#1: thread the publisher so the new function can perform
	// the atomic-RMW publish via delivery.Publisher.Publish with
	// ConflictOverwrite. The pre-fix adapter constructed the
	// uploader-only flow and silently skipped the upload step
	// (DRIVE-008 CUTOVER) — the root cause of the data-loss bug.
	err := appclips.UpdateCumulativeMetadataJSON(
		ctx,
		NewClipsDriveAdapter(a.admin, a.reader, a.lifecycle),
		a.publisher, // P0-#1: previously missing — the new function takes a publisher param
		a.cfg.Storage.TempPath(),
		folderID,
		clipID,
		entry,
		a.log,
	)
	if err != nil {
		// Log the error at the adapter boundary so operators see it
		// even when the upstream caller `_ =`-ignores the return
		// value. Use Info level for the soft-skip (Drive not
		// configured) and Error for everything else.
		if err == appclips.ErrMetadataDriveNotConfigured {
			a.log.Debug("SourcingMetadataAdapter: metadata.json sidecar update skipped (Drive not configured)",
				zap.String("folder_id", folderID),
				zap.String("clip_id", clipID))
		} else {
			a.log.Error("SourcingMetadataAdapter: metadata.json sidecar update failed",
				zap.String("folder_id", folderID),
				zap.String("clip_id", clipID),
				zap.Error(err))
		}
	}
	return err
}

// ── ZapSourcingLogger ─────────────────────────────────────────────────

type ZapSourcingLogger struct {
	log *zap.Logger
}

func (a *ZapSourcingLogger) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *ZapSourcingLogger) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *ZapSourcingLogger) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *ZapSourcingLogger) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}

// ── SourcingEnrichmentAdapter ─────────────────────────────────────────
//
// Card 10 (July 2026): the adapter no longer holds a raw *clips.Handler —
// the only non-HTTP consumer of internal/api/assets/clips/. Instead it
// depends on the canonical appclips.ClipEnricher typed port (godlike/06
// SSOT one-canonical-owner-per-fact). The slim boundary lets the
// descriptor expose ONLY routes + job handlers (no Handler field), and
// it future-proofs the consumer against implementation swaps
// (async worker-backed enrichment, Qdrant-direct bulk paths).
type SourcingEnrichmentAdapter struct {
	enricher appclips.ClipEnricher
}

func (a *SourcingEnrichmentAdapter) EnrichAndIndex(ctx context.Context, clipID, localPath, source string) error {
	if a.enricher == nil {
		// PR-SOURCING-ADAPTER-FAIL-CLOSED (July 2026): fail-closed
		// pre-return — replaces the pre-fix silent-success nil.
		// The canonical composition-time invariant is enricher non-nil;
		// if nil, the canonical wiring path was bypassed and the caller
		// MUST observe the failure as a typed-sentinel rather than as
		// a no-op success (godlike/07 NO-FAKE-AVAILABILITY).
		return sourcing.ErrSourcingEnrichAndIndexDisabled
	}
	// Card 10 (July 2026): pass clipID only — the use case performs
	// the assetRepo lookup internally (pre-Card-10 the adapter
	// constructed a minimal Asset shape and called the handler
	// delegator). localPath + source are preserved on the adapter's
	// outer signature for caller-back-compat (composition root never
	// reads them; they're historic register-flow args).
	_ = localPath
	_ = source
	return a.enricher.EnrichAndIndex(ctx, clipID)
}
