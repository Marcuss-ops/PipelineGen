// Package texttracks — ensure_translated_subtitle.go: the on-demand
// requested-language subtitle materialization used by the localized render.
//
// The runner can emit a localized render before the post-commit
// `asset.text.materialize` fan-out has created the target language's text
// track. The render must not fall back to the source language, and it must not
// grow a second translation path either. This method delegates to the SAME
// BackfillService.ProcessAsset pipeline as the job handler and the operator CLI
// (translate → align cues → persist → deliver ASS), then re-reads the target
// track as the success signal.
package texttracks

import (
	"context"
	"fmt"
	"strings"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// EnsureTranslatedSubtitleTrack guarantees that assetID has a READY transcript
// track with timed cues in targetLang. An existing READY target is reused
// without a translation call. A missing (or text-only) target is repaired
// through the canonical backfill pipeline.
//
// Fail-closed: an asset that cannot be loaded, a per-asset failure reported by
// ProcessAsset, or a target that still has no timed cues after the write is a
// typed error. It is never replaced by the source-language track.
func (s *BackfillService) EnsureTranslatedSubtitleTrack(ctx context.Context, assetID, sourceLang, targetLang string) error {
	assetID = strings.TrimSpace(assetID)
	sourceLang = strings.TrimSpace(sourceLang)
	targetLang = strings.TrimSpace(targetLang)
	if assetID == "" || sourceLang == "" || targetLang == "" {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: asset_id, source_language and target_language are required")
	}
	if strings.EqualFold(sourceLang, targetLang) {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: target language %q equals the source language (a translated track is not applicable)", targetLang)
	}
	if s == nil || s.repo == nil {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: text-track repository is required")
	}

	// Idempotent fast path: a READY target with timing costs one lookup and
	// proves the requested language is already burnable. It deliberately does
	// not require the asset lister, so a wired render can reuse an existing
	// track even in a composition without the media-asset reader.
	if track, cues, err := s.repo.FindReady(ctx, assetID, targetLang, detail.TextTrackTranscript); err != nil {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: find %q/%q: %w", assetID, targetLang, err)
	} else if track != nil && len(cues) > 0 {
		return nil
	}
	if s.clips == nil {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: asset lister is required to materialize %q/%q", assetID, targetLang)
	}

	assets, err := s.clips.List(ctx, asset.Filter{IDs: []string{assetID}, Limit: 1})
	if err != nil {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: load asset %q: %w", assetID, err)
	}
	if len(assets) != 1 || assets[0] == nil {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: asset %q not found in the media SSOT", assetID)
	}
	assetItem := assets[0]
	result, err := s.ProcessAsset(ctx, assetItem, BackfillOptions{
		Source:          string(assetItem.Source),
		SourceLanguage:  sourceLang,
		TargetLanguages: []string{targetLang},
		TextKind:        detail.TextTrackTranscript,
	})
	if err != nil {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: materialize %q/%q: %w", assetID, targetLang, err)
	}
	if result.Err != "" {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: materialize %q/%q: %s", assetID, targetLang, result.Err)
	}

	track, cues, err := s.repo.FindReady(ctx, assetID, targetLang, detail.TextTrackTranscript)
	if err != nil {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: verify %q/%q: %w", assetID, targetLang, err)
	}
	if track == nil || len(cues) == 0 {
		return fmt.Errorf("texttracks.EnsureTranslatedSubtitleTrack: %q has no timed %s track for %q after materialization", assetID, detail.TextTrackTranscript, targetLang)
	}
	return nil
}
