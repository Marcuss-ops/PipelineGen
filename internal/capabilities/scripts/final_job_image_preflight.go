package scriptgeneration

import (
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// final_job_image_preflight.go: the pre-enqueue entity-image gate (the r10
// failure class). A plan can promise entity-image overlays whose assets were
// never verified into a content address; the card then fails open to
// text-only and the run renders visibly WITHOUT its photographs — detected
// only after a full remote render. Like enforceFinalJobPreflight for media
// locators, this gate rejects the predictable rejection class locally,
// before a Master attempt exists.
//
// Fail-closed on the TOTAL class only: expected > 0 with zero verified
// assets is the silent-empty-visual incident; it errors. A PARTIAL miss is
// the documented fail-open policy (one invalid binding keeps its card
// text-only rather than failing the whole run) and stays visible through
// the overlay-plan index warn instead.

// finalJobImageVerifiedRef reports whether one asset reference is a
// verified, renderable identity: a 64-hex content address plus a locator
// (Drive/public URL or a usable local path) the worker can actually fetch.
func finalJobImageVerifiedRef(ref capoverlay.OverlayAssetRef) bool {
	sha := strings.TrimSpace(ref.SHA256)
	if len(sha) != 64 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	if strings.TrimSpace(ref.URL) != "" {
		return true
	}
	return usableLocalAssetPath(ref.LocalPath)
}

func anyFinalJobImageVerifiedRef(refs []capoverlay.OverlayAssetRef) bool {
	for _, ref := range refs {
		if finalJobImageVerifiedRef(ref) {
			return true
		}
	}
	return false
}

// finalJobImageVerifiedItem carries the per-kind admission rule:
//   - a plain entity-image item is verified when it holds at least one
//     verified asset reference;
//   - a composite image group (ImageLayers, two or more entities joined into
//     one card) is verified when the refs provide a verified content address
//     for every layer: one unsupported layer would render a partial card.
//     The composite's structural multiplicity (>=2 layers, >=2 refs) is
//     already the model validator's contract; this checks content identity.
func finalJobImageVerifiedItem(item capoverlay.OverlayItem) bool {
	if len(item.ImageLayers) > 0 {
		if len(item.AssetRefs) < len(item.ImageLayers) {
			return false
		}
		verified := 0
		for _, ref := range item.AssetRefs {
			if finalJobImageVerifiedRef(ref) {
				verified++
			}
		}
		return verified >= len(item.ImageLayers)
	}
	return anyFinalJobImageVerifiedRef(item.AssetRefs)
}

// finalJobImagePreflight errors when the plan promises entity-image overlays
// but none carries a verified asset. Items scheduled on intermediate clip
// scenes are excluded through clipScenes: image-style cards there are
// dropped by the final-job handoff by design, so promising them is not a
// broken run.
func finalJobImagePreflight(items []capoverlay.OverlayItem, clipScenes map[string]struct{}) error {
	expected, unverified := 0, 0
	firstUnverified := ""
	for _, item := range items {
		if item.Kind != string(capoverlay.KindEntityImage) {
			continue
		}
		if _, dropped := clipScenes[strings.TrimSpace(item.SceneID)]; dropped {
			continue
		}
		expected++
		if !finalJobImageVerifiedItem(item) {
			unverified++
			if firstUnverified == "" {
				firstUnverified = strings.TrimSpace(item.ID)
			}
		}
	}
	if expected == 0 {
		return nil
	}
	if unverified == expected {
		return fmt.Errorf("final_job preflight: entity image overlays are promised but none carries a verified asset (sha256 + locator), unverified overlay %q; predicted silent visual loss", firstUnverified)
	}
	return nil
}
