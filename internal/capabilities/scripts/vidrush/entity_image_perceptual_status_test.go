package vidrush

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestDropPerceptualDuplicatesMarksUnhashedCandidates(t *testing.T) {
	out := dropPerceptualDuplicates([]scriptpkg.SegmentAssetCandidate{{AssetID: "no-hash"}})
	if len(out) != 1 || out[0].PerceptualDedupStatus != "UNHASHED" {
		t.Fatalf("unhashed candidate status = %+v, want explicit UNHASHED", out)
	}
}
