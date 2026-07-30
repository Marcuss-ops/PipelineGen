// Package adapters — processor_asset_location_reconciliation.go
// verifies every drive_link in the SpecScene bindings against the
// canonical AssetLocationVerifier before the document is published.
//
// The processor runs after clip_bindings and stock_bindings have
// populated the per-scene bindings and before the document and
// persistence processors consume them. It ensures:
//
//   - No broken link reaches the Google Doc renderer.
//   - Stale links are replaced with the canonical webViewLink.
//   - Missing/trashed/inaccessible links are cleared and flagged
//     as warnings.
//
// The processor is BestEffort: transport errors are surfaced as
// warnings rather than hard failures. A nil verifier is still a
// hard composition error.
package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// AssetLocationReconciliationProcessor verifies every drive_link
// in the SpecScene bindings and reconciles them against the
// canonical AssetLocationVerifier.
type AssetLocationReconciliationProcessor struct {
	verifier scriptpkg.AssetLocationVerifier
}

// NewAssetLocationReconciliationProcessor creates the processor.
// verifier must be non-nil (enforced at registration time).
func NewAssetLocationReconciliationProcessor(
	verifier scriptpkg.AssetLocationVerifier,
) *AssetLocationReconciliationProcessor {
	return &AssetLocationReconciliationProcessor{verifier: verifier}
}

func (p *AssetLocationReconciliationProcessor) Name() ProcessorName {
	return ProcessorAssetLocationReconciliation
}

// Policy classifies asset_location_reconciliation as BestEffort:
// transport errors during verification are surfaced as warnings
// rather than hard failures, allowing generation to complete with
// degraded link integrity.
func (p *AssetLocationReconciliationProcessor) Policy(
	_ *scriptpkg.ResolvedGenerationPlan,
) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process iterates over every scene in the SpecScene and verifies
// each drive_link found in the bindings. Verified links are kept;
// updated links are replaced with the canonical webViewLink;
// missing/trashed/inaccessible/malformed links are cleared and
// flagged as warnings. The UpdatedSpecScene field in the result
// carries the reconciled SpecScene for downstream consumption.
func (p *AssetLocationReconciliationProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	_ = plan

	if p.verifier == nil {
		return nil, fmt.Errorf("%w: asset_location_reconciliation processor: AssetLocationVerifier not configured", scriptpkg.ErrPostprocessFailed)
	}

	scenes := input.SpecScene.Scenes
	if len(scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	var (
		warnings          []string
		changed           bool
		okCount           int
		updatedCount      int
		missingCount      int
		trashedCount      int
		inaccessibleCount int
		malformedCount    int
	)

	// Work on a copy of the scenes so we can mutate in place.
	reconciled := cloneSpecScenes(scenes)

	for i := range reconciled {
		scene := &reconciled[i]
		bindings := &scene.Bindings

		// ── Clip binding ────────────────────────────────────
		if bindings.Clip != nil {
			if link := strings.TrimSpace(bindings.Clip.DriveLink); link != "" {
				result, err := p.verifyAndReconcile(
					ctx, bindings.Clip.ClipID,
					"", link,
					&bindings.Clip.DriveLink,
					"clip", scene.ID,
				)
				if err != nil {
					return nil, err
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
			}
			if link := strings.TrimSpace(bindings.Clip.SubtitleLink); link != "" {
				fileID := strings.TrimSpace(bindings.Clip.SubtitleFileID)
				result, err := p.verifyAndReconcile(
					ctx, bindings.Clip.ClipID,
					fileID, link,
					&bindings.Clip.SubtitleLink,
					"subtitle", scene.ID,
				)
				if err != nil {
					return nil, err
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
				if result.changed && strings.TrimSpace(bindings.Clip.SubtitleLink) == "" {
					bindings.Clip.SubtitleFileID = ""
				}
			}
		}

		// ── Stock binding ───────────────────────────────────
		if bindings.Stock != nil {
			if link := strings.TrimSpace(bindings.Stock.DriveLink); link != "" {
				assetID := bindings.Stock.AssetID
				result, err := p.verifyAndReconcile(
					ctx, assetID,
					"", link,
					&bindings.Stock.DriveLink,
					"stock", scene.ID,
				)
				if err != nil {
					return nil, err
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
			}
		}

		// ── Voiceover binding ───────────────────────────────
		if bindings.Voiceover != nil {
			if link := strings.TrimSpace(bindings.Voiceover.Link); link != "" {
				result, err := p.verifyAndReconcile(
					ctx, "voiceover:"+scene.ID,
					"", link,
					&bindings.Voiceover.Link,
					"voiceover", scene.ID,
				)
				if err != nil {
					return nil, err
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
			}
		}

		// ── Media bindings ──────────────────────────────────
		for j := range bindings.Media {
			media := &bindings.Media[j]
			if link := strings.TrimSpace(media.DriveLink); link != "" {
				assetID := media.AssetID
				result, err := p.verifyAndReconcile(
					ctx, assetID,
					"", link,
					&media.DriveLink,
					fmt.Sprintf("media[%d]", j), scene.ID,
				)
				if err != nil {
					return nil, err
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
			}
		}
	}

	return &PostProcessResult{
		Changed:          changed,
		UpdatedSpecScene: scriptpkg.SpecSceneOutput{Version: input.SpecScene.Version, Scenes: reconciled},
		Warnings:         warnings,
	}, nil
}

// reconcileResult captures the outcome of a single link verification.
type reconcileResult struct {
	changed      bool
	ok           int
	updated      int
	missing      int
	trashed      int
	inaccessible int
	malformed    int
	warning      string
}

// verifyAndReconcile calls the verifier for a single link and
// updates the link pointer in place. Returns a reconcileResult
// with counters and an optional warning string.
// Transport errors (network timeout, Drive API 5xx) are surfaced
// as warnings per the ProcessorBestEffort contract — the generation
// continues with the link preserved as-is.
func (p *AssetLocationReconciliationProcessor) verifyAndReconcile(
	ctx context.Context,
	assetID string,
	fileID string,
	link string,
	linkPtr *string,
	label string,
	sceneID string,
) (reconcileResult, error) {
	verified, err := p.verifier.Verify(ctx, assetID, fileID, link)
	if err != nil {
		// BestEffort: transport errors become warnings, link preserved.
		return reconcileResult{
			warning: fmt.Sprintf(
				"asset_location_reconciliation: transport error verifying %s link in %s (link preserved): %v",
				label, sceneID, err),
		}, nil
	}
	if verified == nil {
		// No link to verify — no-op.
		return reconcileResult{}, nil
	}

	switch verified.State {
	case scriptpkg.LocationStateVerified:
		return reconcileResult{ok: 1}, nil

	case scriptpkg.LocationStateUpdated:
		*linkPtr = verified.DriveLink
		return reconcileResult{changed: true, updated: 1}, nil

	case scriptpkg.LocationStateMissing:
		*linkPtr = ""
		return reconcileResult{
			changed: true,
			missing: 1,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is MISSING (Drive file not found): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateTrashed:
		*linkPtr = ""
		return reconcileResult{
			changed: true,
			trashed: 1,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is TRASHED (file in Drive trash): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateInaccessible:
		*linkPtr = ""
		return reconcileResult{
			changed:      true,
			inaccessible: 1,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is INACCESSIBLE (permission denied): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateMalformed:
		*linkPtr = ""
		return reconcileResult{
			changed:   true,
			malformed: 1,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is MALFORMED (cannot extract file ID): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateOrphanDriveFile:
		*linkPtr = ""
		return reconcileResult{
			changed: true,
			missing: 1,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is an ORPHAN (file on Drive but no SQLite record): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateBrokenAssetLocation:
		*linkPtr = ""
		return reconcileResult{
			changed: true,
			missing: 1,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s has a BROKEN location (SQLite references missing Drive file): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateDuplicate:
		*linkPtr = ""
		return reconcileResult{
			changed:   true,
			malformed: 1,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is a DUPLICATE (multiple assets share same drive_file_id): asset=%s",
				label, sceneID, assetID),
		}, nil

	default:
		return reconcileResult{}, nil
	}
}

// cloneSpecScenes returns a shallow copy of the scenes slice for
// safe in-place mutation during reconciliation. Copies all
// binding types including Media (which cloneSceneBindings does
// not yet handle — forward-pointer to merge back when
// cloneSceneBindings gains Media support).
func cloneSpecScenes(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := make([]scriptpkg.SpecScene, len(scenes))
	for i, sc := range scenes {
		out[i] = sc
		if sc.Metadata != nil {
			meta := *sc.Metadata
			out[i].Metadata = &meta
		}
		out[i].Bindings = cloneSceneBindings(sc.Bindings)
		// Clone Media bindings (cloneSceneBindings currently only
		// handles Clip/Image/Voiceover/Stock).
		if len(sc.Bindings.Media) > 0 {
			out[i].Bindings.Media = make([]scriptpkg.ResolvedMediaBinding, len(sc.Bindings.Media))
			copy(out[i].Bindings.Media, sc.Bindings.Media)
		}
	}
	return out
}
