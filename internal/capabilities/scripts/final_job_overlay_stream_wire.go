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
