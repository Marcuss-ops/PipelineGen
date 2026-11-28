package finaljob

// final_job_split_handoff.go owns the split PREPARE/FINALIZE contract:
//
//   PREPARE  base timeline only (clips, stock, final audio) — no overlay
//            render output and therefore no overlay windows.
//   FINALIZE base runtime assets + the certified overlay artifact and its
//            manifest, which the Master/worker projects onto the assembled
//            base.
//
// The split exists so PREPARE can be sent while the local overlay render is
// still running. It is strictly additive: BuildFinalJobEnvelopeV1 keeps the
// serial handoff byte-identical for every caller that cannot defer overlays.

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
)

// FinalJobSourceHandoff is the durable half of a split handoff. Prepare is the
// sealed base plan; Finalize already carries every base runtime asset (the
// certified final audio and the Producer-prepared clip fragments) but no
// overlay artifact. BuildFinalJobOverlayFinalize decorates it once the overlay
// render is complete.
type FinalJobSourceHandoff struct {
	Prepare    finaljob.PrepareV1
	Finalize   finaljob.FinalizeEnvelopeV1
	PlanSHA256 string
}

// BuildFinalJobSourceHandoff builds the base PREPARE and the base FINALIZE.
// It deliberately clears every overlay projection from the input before the
// existing scene builder runs: the overlay plan is not part of the base plan,
// and the overlay render is exactly the work PREPARE is allowed to overlap.
func BuildFinalJobSourceHandoff(ctx context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult, resolver FinalJobAssetResolver, preparers ...FinalJobVideoSegmentPreparer) (FinalJobSourceHandoff, error) {
	// The audit gate is cheap and needs no rendered artifact. Run it on the
	// original result (the source projection intentionally hides overlay
	// fields) so a promised-image failure cannot leave a PREPARED base job
	// waiting for a FINALIZE that will never be valid.
	if result != nil {
		if err := FinalJobEntityImageGate(result.OverlayAudit, FinalJobImageRequested(req)); err != nil {
			return FinalJobSourceHandoff{}, err
		}
	}
	source := finalJobSourceResult(result)
	stockPoolFolderID, hasFinalAudio, err := finalJobSourcePreflight(req, source)
	if err != nil {
		return FinalJobSourceHandoff{}, err
	}
	builder := newFinalJobSceneBuilder(ctx, req, source, resolver, stockPoolFolderID)
	if err := builder.appendSegments(); err != nil {
		return FinalJobSourceHandoff{}, err
	}
	prepare, finalize, err := finalJobFinalizeEnvelope(ctx, runID, req, source, builder, preparers, hasFinalAudio, false)
	if err != nil {
		return FinalJobSourceHandoff{}, err
	}
	planSHA := prepare.CompiledRenderPlanSHA256
	if planSHA == "" {
		return FinalJobSourceHandoff{}, fmt.Errorf("final_job split PREPARE has no compiled plan digest")
	}
	return FinalJobSourceHandoff{Prepare: prepare, Finalize: finalize, PlanSHA256: planSHA}, nil
}

// BuildFinalJobOverlayFinalize adds the certified overlay handoff to the base
// FINALIZE. This is the document the Master receives after the render when the
// split is active: the PREPARE plan stays immutable and contains only the base
// timeline, while the overlay artifact, manifest and dedicated rows travel
// here and are projected by the worker after assembly.
func BuildFinalJobOverlayFinalize(ctx context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult, source FinalJobSourceHandoff) (finaljob.FinalizeEnvelopeV1, error) {
	if strings.TrimSpace(runID) == "" || source.Finalize.IdempotencyKey == "" {
		return finaljob.FinalizeEnvelopeV1{}, fmt.Errorf("final_job split FINALIZE has no run id or source payload")
	}
	finalize := source.Finalize
	finalize.RuntimeAssets = append([]finaljob.RuntimeAssetV1(nil), source.Finalize.RuntimeAssets...)
	// The source PREPARE intentionally skips the overlay image gate: the audit
	// and the promised-image checks belong to the finished render. Re-run the
	// audit gate here, before the late FINALIZE describes a projection the
	// worker must be able to produce.
	if result == nil {
		return finaljob.FinalizeEnvelopeV1{}, fmt.Errorf("final_job split FINALIZE has no result")
	}
	imageRequested := FinalJobImageRequested(req)
	if err := FinalJobEntityImageGate(result.OverlayAudit, imageRequested); err != nil {
		return finaljob.FinalizeEnvelopeV1{}, err
	}
	typedOverlays, typedOverlayManifest, err := FinalJobOverlayHandoffTyped(ctx, result, nil, imageRequested)
	if err != nil {
		return finaljob.FinalizeEnvelopeV1{}, err
	}
	if len(finalize.RuntimeAssets) == 0 {
		return finaljob.FinalizeEnvelopeV1{}, fmt.Errorf("final_job split FINALIZE has no base runtime assets")
	}
	seen := finalJobTypedRuntimeAssetSeen(finalize.RuntimeAssets)
	assets, err := appendFinalJobTypedOverlayRuntimeAssets(finalize.RuntimeAssets, seen, typedOverlays, typedOverlayManifest)
	if err != nil {
		return finaljob.FinalizeEnvelopeV1{}, err
	}
	finalize.RuntimeAssets = assets

	runtimePayload := finalize.RuntimePayload
	runtimePayload.Overlays = typedOverlays
	if typedOverlayManifest != nil {
		runtimePayload.OverlayManifest = typedOverlayManifest
	} else {
		runtimePayload.OverlayManifest = nil
	}
	finalize.RuntimePayload = runtimePayload
	return finalize, nil
}

// finalJobSourceResult is the input projection the split PREPARE is allowed to
// consume. Overlay facts are removed instead of nulled one by one so a future
// overlay field cannot silently leak into the sealed base plan.
func finalJobSourceResult(result *contract.GenerateResult) *contract.GenerateResult {
	source := finalJobInputFor(result)
	if source == nil {
		return nil
	}
	source.OverlayPlan = nil
	source.OverlayRender = nil
	source.LocalizedOverlayRenders = nil
	source.OverlayRenderHistory = nil
	source.OverlayAudit = nil
	source.OverlayPlanIntake = nil
	source.FinalJobPreflight = nil
	return source
}

// finalJobSourcePreflight is the cheap source half of finalJobPreflight. It
// validates only what the base plan needs; the promised-image gate is checked
// on the original audit in BuildFinalJobSourceHandoff, and again in the late
// FINALIZE, where the rendered overlay data exists.
func finalJobSourcePreflight(req contract.GenerateRequest, result *contract.GenerateResult) (string, bool, error) {
	if result == nil || result.CanonicalTimeline == nil || result.CanonicalTimeline.DurationUS <= 0 {
		return "", false, fmt.Errorf("final_job requires a canonical timeline with positive duration")
	}
	hasFinalAudio := result.FinalAudio != nil && result.FinalAudio.DurationMS > 0
	if hasFinalAudio && result.FinalAudio.StartPTS != 0 {
		return "", false, fmt.Errorf("final_job final audio start_pts is %d, want 0 for voiceover alignment", result.FinalAudio.StartPTS)
	}
	if len(result.Scenes) == 0 {
		return "", false, fmt.Errorf("final_job has no generated scenes")
	}
	stockPoolFolderID, err := finalJobStockPoolFolder(req)
	if err != nil {
		return "", false, err
	}
	return stockPoolFolderID, hasFinalAudio, nil
}

func finalJobTypedRuntimeAssetSeen(assets []finaljob.RuntimeAssetV1) map[string]bool {
	seen := make(map[string]bool, len(assets))
	for _, asset := range assets {
		id := strings.TrimSpace(asset.DriveFileID)
		if id == "" {
			id = strings.TrimSpace(asset.AssetID)
		}
		if id != "" {
			seen[strings.TrimSpace(asset.Role)+":"+id] = true
		}
	}
	return seen
}

func appendFinalJobTypedOverlayRuntimeAssets(assets []finaljob.RuntimeAssetV1, seen map[string]bool, overlays []finaljob.OverlayRowV1, manifest *finaljob.OverlayManifestV1) ([]finaljob.RuntimeAssetV1, error) {
	appendAsset := func(asset finaljob.RuntimeAssetV1) error {
		id := strings.TrimSpace(asset.DriveFileID)
		if id == "" {
			id = strings.TrimSpace(asset.AssetID)
		}
		if id == "" {
			return fmt.Errorf("final_job overlay runtime asset has no identity")
		}
		key := strings.TrimSpace(asset.Role) + ":" + id
		if !seen[key] {
			seen[key] = true
			assets = append(assets, asset)
		}
		return nil
	}
	appendManifestAsset := func(manifest finaljob.OverlayManifestV1) error {
		if manifest.Asset == nil {
			return fmt.Errorf("final_job overlay manifest has no asset")
		}
		asset, err := finaljob.RuntimeAssetFromOverlayAsset(*manifest.Asset, "overlay_timeline")
		if err != nil {
			return err
		}
		if err := appendAsset(asset); err != nil {
			return fmt.Errorf("final_job overlay timeline runtime asset: %w", err)
		}
		return nil
	}
	if manifest != nil {
		if manifest.SchemaVersion == "pipelinegen.overlay-manifest-set.v1" {
			for _, item := range manifest.Manifests {
				if err := appendManifestAsset(item); err != nil {
					return nil, fmt.Errorf("final_job overlay manifest runtime asset: %w", err)
				}
			}
		} else if err := appendManifestAsset(*manifest); err != nil {
			return nil, err
		}
	}
	for _, row := range overlays {
		asset, err := finaljob.RuntimeAssetFromOverlayRow(row, "overlay")
		if err != nil {
			return nil, fmt.Errorf("final_job overlay runtime asset: %w", err)
		}
		if err := appendAsset(asset); err != nil {
			return nil, fmt.Errorf("final_job overlay runtime asset: %w", err)
		}
	}
	return assets, nil
}
