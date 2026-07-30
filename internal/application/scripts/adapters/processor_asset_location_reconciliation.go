// Package adapters — processor_asset_location_reconciliation.go
// verifies every drive_link in the SpecScene bindings against the
// canonical AssetLocationResolver before the document is published.
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
// The processor is Required: every SpecScene with bindings MUST
// pass through location reconciliation. A nil resolver at
// composition time is a hard error.
package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// AssetLocationReconciliationProcessor verifies every drive_link
// in the SpecScene bindings and reconciles them against the
// canonical AssetLocationResolver.
type AssetLocationReconciliationProcessor struct {
	resolver scriptpkg.AssetLocationResolver
}

// NewAssetLocationReconciliationProcessor creates the processor.
// resolver must be non-nil (enforced at registration time).
func NewAssetLocationReconciliationProcessor(
	resolver scriptpkg.AssetLocationResolver,
) *AssetLocationReconciliationProcessor {
	return &AssetLocationReconciliationProcessor{resolver: resolver}
}

func (p *AssetLocationReconciliationProcessor) Name() ProcessorName {
	return ProcessorAssetLocationReconciliation
}

// Policy classifies asset_location_reconciliation as Required:
// every generation that produces bindings MUST verify its Drive
// links before the document is published.
func (p *AssetLocationReconciliationProcessor) Policy(
	_ *scriptpkg.ResolvedGenerationPlan,
) ProcessorPolicy {
	return ProcessorRequired
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

	if p.resolver == nil {
		return nil, fmt.Errorf("%w: asset_location_reconciliation processor: AssetLocationResolver not configured", scriptpkg.ErrPostprocessFailed)
	}

	scenes := input.SpecScene.Scenes
	if len(scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	var (
		warnings    []string
		changed     bool
		okCount     int
		updatedCount int
		missingCount int
		trashedCount int
		inaccessibleCount int
		malformedCount int
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
		Changed:           changed || len(warnings) > 0,
		UpdatedSpecScene:  scriptpkg.SpecSceneOutput{Version: input.SpecScene.Version, Scenes: reconciled},
		Warnings:          warnings,
	}, nil
}

// reconcileResult captures the outcome of a single link verification.
type reconcileResult struct {
	changed     bool
	ok          int
	updated     int
	missing     int
	trashed     int
	inaccessible int
	malformed   int
	warning     string
}

// verifyAndReconcile calls the resolver for a single link and
// updates the link pointer in place. Returns a reconcileResult
// with counters and an optional warning string.
// Transport errors (network timeout, Drive API 5xx) are returned
// as Go errors — the caller MUST fail closed per the
// ProcessorRequired contract.
func (p *AssetLocationReconciliationProcessor) verifyAndReconcile(
	ctx context.Context,
	assetID string,
	fileID string,
	link string,
	linkPtr *string,
	label string,
	sceneID string,
) (reconcileResult, error) {
	verified, err := p.resolver.ResolveAndVerify(ctx, assetID, fileID, link)
	if err != nil {
		return reconcileResult{}, fmt.Errorf(
			"asset_location_reconciliation: transport error verifying %s link in %s: %w",
			label, sceneID, err)
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

	default:
		return reconcileResult{}, nil
	}
}

// cloneSpecScenes returns a shallow copy of the scenes slice for
// safe in-place mutation during reconciliation.
func cloneSpecScenes(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := make([]scriptpkg.SpecScene, len(scenes))
	for i, sc := range scenes {
		out[i] = sc
		if sc.Metadata != nil {
			meta := *sc.Metadata
			out[i].Metadata = &meta
		}
		// Shallow-clone the bindings so we can mutate pointers
		// without affecting the original SceneBindings struct.
		out[i].Bindings = cloneSceneBindings(sc.Bindings)
	}
	return out
}
