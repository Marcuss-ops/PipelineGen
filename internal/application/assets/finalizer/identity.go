package finalizer

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// ComputeAssetID derives a deterministic AssetID from the asset's logical
// identity components (Piano d'Azione § 5.1).
//
//	asset_identity = artifact kind + logical source ID + source version
//
// The returned AssetID is the first 16 bytes of SHA-256(identity), hex-encoded.
// This gives a 32-character stable identifier that:
//   - Does not change when content changes (only version does)
//   - Is deterministic across workers (same source + version → same ID)
//   - Avoids collisions (128-bit hash space)
//
// Capabilities SHOULD use this helper to compute the ArtifactID before
// producing a VerifiedArtifact. The AssetFinalizerTx then uses ArtifactID
// as the media_assets.id (canonical AssetID).
//
// If sourceID is empty, falls back to a random-looking ID derived from
// kind + version alone (suitable for transient/local-only artifacts).
func ComputeAssetID(kind finalization.ArtifactKind, sourceID string, sourceVersion int64) string {
	var input string
	if sourceID != "" {
		input = fmt.Sprintf("%s:%s:%d", kind, sourceID, sourceVersion)
	} else {
		input = fmt.Sprintf("%s::%d", kind, sourceVersion)
	}
	h := digest.SHA256Bytes([]byte(input))
	return h[:16]
}
