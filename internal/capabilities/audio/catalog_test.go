package audio

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// TestCanonicalAssetIDRewritesOnlyRegistryBoundAliases pins the alias →
// Drive-identity boundary that the audio asset resolver depends on.
//
// Editorial aliases (bgm1..bgm6, whop1..whop6 and the bound whoosh1..whoosh3)
// map to their bound Drive identity. Everything else passes through trimmed and
// unchanged — in particular the UNBOUND compatibility aliases (whoosh4..whoosh9,
// the retired whoop* space and the random_whoosh directive). Passing through is
// what keeps the media registry lookup as the single fail-closed gate: an
// unbound alias errors there instead of being silently rewritten onto some
// other asset.
func TestCanonicalAssetIDRewritesOnlyRegistryBoundAliases(t *testing.T) {
	bound := mediaregistry.EditorialAudioAliases()
	if len(bound) == 0 {
		t.Fatal("editorial audio catalog binds no aliases")
	}
	for alias, driveID := range bound {
		if got := CanonicalAssetID(alias); got != driveID {
			t.Errorf("CanonicalAssetID(%q) = %q, want the bound Drive identity %q", alias, got, driveID)
		}
		if got := CanonicalAssetID("  " + strings.ToUpper(alias) + "  "); got != driveID {
			t.Errorf("CanonicalAssetID(%q) = %q, want %q (matching must be trim/case tolerant)", alias, got, driveID)
		}
	}

	for _, id := range []string{"whoosh4", "whoosh9", "random_whoosh", "whoop1", "whoop", "caller_asset_42"} {
		if _, isBound := bound[id]; isBound {
			t.Fatalf("%q must not be a canonical editorial alias for this test to be meaningful", id)
		}
		if got := CanonicalAssetID("  " + id + "  "); got != id {
			t.Errorf("CanonicalAssetID(%q) = %q, want pass-through %q (resolution belongs to the media registry)", id, got, id)
		}
	}
}
