// Package usecase — process_segment_step6_text.go (GOP-06, November 2026).
//
// Step 6 owner: the central TextTrackResolver chain (payload → DB → YouTube
// subtitles → Whisper) and the cross-package .txt side-channel read by
// clipindexer.lookupTranscriptPath.
package usecase

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// acquireSegmentText runs the canonical acquisition chain with the
// configured multilingual language policy.
//
// An acquisition error is NON-fatal: it is logged and the caller continues
// with an empty bundle (the clip is persisted without text tracks, the
// writer's policy validation is skipped when RequireTranscriptReady=false).
// A sidecar write error IS returned: the READY bundle must not be silently
// lost.
func (u *ProcessYouTubeSegmentUseCase) acquireSegmentText(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	clipID string,
	startSec, endSec int,
	localPath, txtPath string,
) (*detail.ResolvedTextBundle, error) {
	if u.media.TextTrackResolver == nil {
		return nil, nil
	}

	var bundle *detail.ResolvedTextBundle
	var acqErr error
	_ = kernobs.MeasureStage(ctx, kernobs.StageYouTubeTranscript, func(stageCtx context.Context) error {
		bundle, acqErr = u.media.TextTrackResolver.AcquireSegmentText(stageCtx, TextTrackAcquireRequest{
			ClipID:       clipID,
			VideoID:      cmd.VideoID,
			StartSec:     startSec,
			EndSec:       endSec,
			PayloadTexts: cmd.Segment.Texts,
			LocalPath:    localPath,
			// Fase 5 policy wiring: the configured multilingual language
			// list IS the acquisition policy. Without it the chain skipped
			// the priority-2 DB fan-out entirely (empty list = no probe)
			// and accepted ANY subtitle language the fetcher surfaced.
			// Same list the writer's RequireAllLanguagesBeforeVideo gate
			// uses (godlike/06 SSOT: one PreferredLanguages per composition).
			PreferredLanguages: u.observability.PreferredLanguages,
		})
		return acqErr
	})
	if acqErr != nil {
		u.logTextAcquisitionFailure(clipID, acqErr)
		return nil, nil
	}
	if bundle != nil && !bundle.IsEmpty() {
		// Backward-compat: keep the .txt side-channel alive for
		// clipindexer.lookupTranscriptPath (godlike/06 SSOT
		// forward-pointer: PR-CLIPINDEXER-TRANSCRIPT-TYPED-PORT).
		// Errors here are LOGGED, not swallowed, per Fase 1.a
		// (audit 2026-07-11 §2.a) — the caller fails the clip.
		if writeErr := writeTranscriptSidecar(bundle, txtPath); writeErr != nil {
			return nil, writeErr
		}
	}
	return bundle, nil
}

// logTextAcquisitionFailure is the canonical non-fatal log for a failed
// acquisition chain.
func (u *ProcessYouTubeSegmentUseCase) logTextAcquisitionFailure(clipID string, err error) {
	u.core.Log.Warn("text track acquisition failed (Whisper fallback returned an error); continuing with empty bundle — clip will be persisted without text tracks; backfill via Fase 5 admin command",
		zap.String("clip_id", clipID), zap.Error(err))
}

// sidecarPath is the canonical transcript path: the clip path with its
// extension replaced by .txt.
func sidecarPath(localPath string) string {
	return strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".txt"
}

// writeTranscriptSidecar persists the resolved plain text next to the clip.
func writeTranscriptSidecar(bundle *detail.ResolvedTextBundle, txtPath string) error {
	return os.WriteFile(txtPath, []byte(bundle.PlainText), 0o644)
}
