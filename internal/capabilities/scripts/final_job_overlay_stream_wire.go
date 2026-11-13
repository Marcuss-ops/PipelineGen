package scriptgeneration

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"strings"
)

// finalJobOverlayStreamWire transports the certified video-stream identity
// without inventing it. A V3 artifact was finalize-validated against the
// canonical timebase and avcC, so its canonical facts are safe to relay. Any
// other profile is a legacy source: it keeps its profile id but makes no
// canonical bitstream claim, and the fragment producer must normalize it.
// finalJobOverlayDuplicateIdentity reports whether the current row repeats an
// already-admitted entity visual in an overlapping window. The caller skips
// that row instead of failing the whole handoff: two planned occurrences of
// one identity are one visual, and the first row already replaces those
// frames with the same certified bytes.
func finalJobOverlayDuplicateIdentity(priorIDs []string, frames map[string]finalJobFrameWindow, itemsByID map[string]capmodel.OverlayItem, current capmodel.OverlayItem, startFrame, endFrame int64) bool {
	if len(priorIDs) == 0 || len(frames) == 0 {
		return false
	}
	for _, priorID := range priorIDs {
		priorWindow, tracked := frames[priorID]
		if !tracked || startFrame >= priorWindow.end || priorWindow.start >= endFrame {
			continue
		}
		prior, exists := itemsByID[strings.TrimSpace(priorID)]
		if exists && finalJobOverlayIdentity(prior) != "" && finalJobOverlayIdentity(prior) == finalJobOverlayIdentity(current) {
			return true
		}
	}
	return false
}

// finalJobOverlayIdentity is the identity join for duplicate collapse. It
// prefers the canonical entity id, then the stable entity id, and only then
// the type/name pair; an item without any identity never collapses.
func finalJobOverlayIdentity(item capmodel.OverlayItem) string {
	if ref := item.EntityRef; ref != nil {
		if canonical := strings.TrimSpace(ref.CanonicalEntityID); canonical != "" {
			return "canonical:" + canonical
		}
		if kind := strings.TrimSpace(ref.Type); kind != "" {
			if name := strings.TrimSpace(ref.Name); name != "" {
				return "entity:" + strings.ToLower(kind) + ":" + strings.ToLower(name)
			}
		}
	}
	if stable := strings.TrimSpace(item.EntityID); stable != "" {
		return "stable:" + stable
	}
	return ""
}

func finalJobOverlayStreamWire(manifest *capmodel.OverlayManifest, artifact *RenderArtifact) map[string]any {
	if artifact == nil {
		return nil
	}
	profileID := strings.TrimSpace(artifact.ProfileID)
	if profileID == "" {
		return nil
	}
	if manifest != nil && manifest.Stream != nil {
		declared := manifest.Stream
		// A declared stream is a CLAIM, not proof. Relay the canonical facts
		// only when the manifest's claim matches both the artifact's lane and
		// every canonical value; otherwise downgrade to copy_ready=false and
		// do not transport an extradata/timebase that would invite a copy.
		if strings.TrimSpace(declared.ProfileID) == profileID &&
			declared.ProfileID == kernelmedia.OverlayVideoOnlyAssemblyProfileID &&
			declared.ExtradataSHA256 == kernelmedia.CanonicalVideoExtradataSHA256 &&
			declared.TimeBaseNum == 1 && declared.TimeBaseDen == 90000 &&
			declared.EncoderRevision == kernelmedia.CanonicalVideoProfileRevision {
			return map[string]any{
				"profile_id": declared.ProfileID, "extradata_sha256": declared.ExtradataSHA256,
				"time_base_num": declared.TimeBaseNum, "time_base_den": declared.TimeBaseDen,
				"encoder_revision": declared.EncoderRevision, "copy_ready": true,
			}
		}
		return map[string]any{"profile_id": profileID, "copy_ready": false}
	}
	if profileID != kernelmedia.OverlayVideoOnlyAssemblyProfileID {
		return map[string]any{"profile_id": profileID, "copy_ready": false}
	}
	return map[string]any{
		"profile_id": profileID, "extradata_sha256": kernelmedia.CanonicalVideoExtradataSHA256,
		"time_base_num": 1, "time_base_den": 90000,
		"encoder_revision": kernelmedia.CanonicalVideoProfileRevision, "copy_ready": true,
	}
}
