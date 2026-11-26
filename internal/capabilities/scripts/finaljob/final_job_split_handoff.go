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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// FinalJobSourceHandoff is the durable half of a split handoff. Prepare is the
// sealed base plan; Finalize already carries every base runtime asset (the
// certified final audio and the Producer-prepared clip fragments) but no
// overlay artifact. BuildFinalJobOverlayFinalize decorates it once the overlay
// render is complete.
type FinalJobSourceHandoff struct {
	Prepare    map[string]any
	Finalize   map[string]any
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
	prepare, finalize, err := finalJobFinalizePayload(ctx, runID, req, source, builder, preparers, hasFinalAudio, false)
	if err != nil {
		return FinalJobSourceHandoff{}, err
	}
	planSHA := FinalJobStringValue(prepare["compiled_render_plan_sha256"])
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
func BuildFinalJobOverlayFinalize(ctx context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult, source FinalJobSourceHandoff) (map[string]any, error) {
	if source.Finalize == nil {
		return nil, fmt.Errorf("final_job split FINALIZE has no source payload")
	}
	finalize, err := cloneFinalJobPayload(source.Finalize)
	if err != nil {
		return nil, fmt.Errorf("clone final_job source FINALIZE: %w", err)
	}
	// The source PREPARE intentionally skips the overlay image gate: the audit
	// and the promised-image checks belong to the finished render. Re-run the
	// audit gate here, before the late FINALIZE describes a projection the
	// worker must be able to produce.
	if result == nil {
		return nil, fmt.Errorf("final_job split FINALIZE has no result")
	}
	imageRequested := FinalJobImageRequested(req)
	if err := FinalJobEntityImageGate(result.OverlayAudit, imageRequested); err != nil {
		return nil, err
	}
	remoteOverlays, overlayManifest, err := FinalJobOverlayHandoff(ctx, result, nil, imageRequested)
	if err != nil {
		return nil, err
	}
	if remoteOverlays == nil {
		remoteOverlays = []any{}
	}
	runtimeAssets, ok := finalize["runtime_assets"].([]any)
	if !ok || len(runtimeAssets) == 0 {
		return nil, fmt.Errorf("final_job split FINALIZE has no base runtime assets")
	}
	seen := finalJobRuntimeAssetSeen(runtimeAssets)
	appendFinalJobOverlayRuntimeAssets(&runtimeAssets, seen, remoteOverlays, overlayManifest)
	finalize["runtime_assets"] = runtimeAssets

	runtimePayload, ok := finalize["runtime_payload"].(map[string]any)
	if !ok || runtimePayload == nil {
		return nil, fmt.Errorf("final_job split FINALIZE has no runtime_payload")
	}
	runtimePayload["overlays"] = remoteOverlays
	if overlayManifest != nil {
		runtimePayload["overlay_manifest"] = overlayManifest
	} else {
		delete(runtimePayload, "overlay_manifest")
	}
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

// appendFinalJobOverlayRuntimeAssets is the single overlay transport append
// shared by the serial and split handoffs. The manifest's timeline artifact is
// role overlay_timeline; dedicated rows are role overlay. Existing base assets
// are kept, and an identity already present is not duplicated.
func appendFinalJobOverlayRuntimeAssets(runtimeAssets *[]any, seen map[string]bool, overlays []any, manifest map[string]any) {
	if manifest != nil {
		if asset, ok := manifest["asset"].(map[string]any); ok {
			AppendFinalJobRuntimeAsset(runtimeAssets, seen, "overlay_timeline", asset)
		}
		if manifests, ok := manifest["manifests"].([]any); ok {
			for _, raw := range manifests {
				if item, ok := raw.(map[string]any); ok {
					if asset, ok := item["asset"].(map[string]any); ok {
						AppendFinalJobRuntimeAsset(runtimeAssets, seen, "overlay_timeline", asset)
					}
				}
			}
		}
	}
	for _, raw := range overlays {
		if asset, ok := raw.(map[string]any); ok {
			AppendFinalJobRuntimeAsset(runtimeAssets, seen, "overlay", asset)
		}
	}
}

func finalJobRuntimeAssetSeen(assets []any) map[string]bool {
	seen := make(map[string]bool, len(assets))
	for _, raw := range assets {
		asset, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := FinalJobStringValue(asset["drive_file_id"])
		if id == "" {
			id = FinalJobStringValue(asset["asset_id"])
		}
		if id == "" {
			continue
		}
		seen[FinalJobStringValue(asset["role"])+":"+id] = true
	}
	return seen
}

func cloneFinalJobPayload(payload map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var cloned map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}
