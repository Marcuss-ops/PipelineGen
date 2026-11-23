// media-cert spec derivation for the script-generation runtime.
//
// Split from script_generation_runtime.go to stay under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention gate); the
// wiring package's runtime composition keeps the caller, this file owns the
// spec derivation it performs.
package script

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func BuildRuntimeMediaCertSpec(plan *scriptpkg.ResolvedGenerationPlan) mediacert.Spec {
	spec := mediacert.Spec{}
	if plan == nil || !plan.HasVisualMediaPlan() {
		return spec
	}
	// One shared folder repeated across every authored scene is a remote stock
	// pool, not a set of direct per-scene stock bindings. The Producer must not
	// certify or select that pool locally; the Master owns its contents and
	// chooses the clips during assembly. Keep STOCK ISOLATION for genuine
	// scene-specific bindings below.
	sharedRemoteStockPool := false
	if plan.MediaMode == scriptpkg.MediaModeStockOnly && len(plan.Segments) > 0 {
		folderID := strings.TrimSpace(plan.Segments[0].StockFolderID)
		folderLink := strings.TrimSpace(plan.Segments[0].StockFolderLink)
		sharedRemoteStockPool = folderID != "" || folderLink != ""
		for _, segment := range plan.Segments[1:] {
			if strings.TrimSpace(segment.StockFolderID) != folderID || strings.TrimSpace(segment.StockFolderLink) != folderLink {
				sharedRemoteStockPool = false
				break
			}
		}
	}
	// Certify provider relevance only when the resolved media plan actually
	// enables that provider. Mixed mode means the item combines
	// caller-selected clips and stock bindings; it does not imply a provider
	// search. In particular, folder-backed stock bindings must not be
	// mis-certified as provider winners when the provider is disabled.
	if plan.MediaMode == scriptpkg.MediaModeMixed && plan.MediaPlan.ProviderPolicy.YouTube.AsBool() {
		spec.VideoProvider = scriptpkg.VidRushProviderYouTube
	}
	// Only authored scenes that enter the VidRush enrichment path define the
	// external scene-identity contract. Explicit no_voiceover segments are
	// fixed-media cutaways: they remain in the final timeline, but do not
	// produce VidRush results and must not be counted by MediaCertBarrier.
	// Free-form text generation may legitimately produce multiple structured
	// scenes, so do not invent a synthetic scene-0 expectation here.
	for _, segment := range plan.Segments {
		if !segment.NoVoiceover {
			spec.Segments++
		}
	}
	spec.EntitiesPerSegment = plan.MediaPlan.Extraction.MaxEntitiesPerSegment
	// Stock-only and clip-only plans use their video references for scene
	// visuals and do not depend on the secondary-image selection lane. Keep
	// image fanout certification for modes whose scene plan actually depends
	// on selected/generated images.
	if plan.MediaMode != scriptpkg.MediaModeStockOnly && plan.MediaMode != scriptpkg.MediaModeClipOnly {
		spec.ImagesPerSegment = plan.ImagesPerScene
	}
	// Canonical entity scope keeps a person/place visually consistent across
	// scenes. Per-scene scope explicitly asks for independent visual evidence
	// and therefore disables that reuse certification.
	// Entity overlays stay visible in each scene even when their five-image
	// catalog pool has already been used. The binder prefers unseen images and
	// falls back to reuse, so the certifier must see those candidates too.
	spec.AllowCrossSceneAssetReuse = plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() || !plan.MediaPlan.Extraction.EntityImages.PerScene()
	for i, segment := range plan.Segments {
		if segment.NoVoiceover {
			continue
		}
		id := strings.TrimSpace(segment.ID)
		if id == "" {
			id = fmt.Sprintf("scene-%d", i)
		}
		subject := strings.TrimSpace(segment.Topic)
		stockBound := !sharedRemoteStockPool && (strings.TrimSpace(segment.StockFolderID) != "" || strings.TrimSpace(segment.StockFolderLink) != "")
		spec.SegmentsExpected = append(spec.SegmentsExpected, mediacert.SpecSegment{
			ID: id, Subject: subject, WinnerSubjectMatch: subject,
			// A per-segment stock folder is the visual source of record for
			// the scene: the certifier must demand NO provider media for it
			// (STOCK ISOLATION) instead of an image budget it can never meet.
			StockBound: stockBound,
			AllowEntityImageCandidates: stockBound && plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() &&
				plan.MediaPlan.Extraction.EntityImages.Enabled && plan.MediaPlan.Extraction.EntityImages.PerScene(),
		})
	}
	return spec
}
