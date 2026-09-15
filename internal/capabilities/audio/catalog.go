package audio

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// BuiltInAssetAliases are the stable asset names exposed by the generation
// payload. The values are the canonical asset IDs used by the media registry
// after `admin index-provided-sound-effects` has seeded the catalog.
//
// Keep these names independent from Drive filenames: callers should be able
// to select an audio cue without knowing where it is stored or how it was
// originally named.
var BuiltInAssetAliases = mediaregistry.EditorialAudioAliases()

// CanonicalAssetID resolves a public payload alias. Unknown IDs are already
// canonical registry IDs and pass through unchanged.
func CanonicalAssetID(id string) string {
	id = strings.TrimSpace(id)
	if canonical, ok := BuiltInAssetAliases[strings.ToLower(id)]; ok {
		return canonical
	}
	return id
}
