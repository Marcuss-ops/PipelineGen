package stockpipeline

import (
	"errors"
	"strings"
	"testing"

	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestStagedSourceWindowUsesAbsoluteSourceTimeline(t *testing.T) {
	staged := &assets.StagedAsset{
		Bytes: 100,
		SourceWindow: &media.SourceWindow{
			AssetSHA256: strings.Repeat("a", 64),
			SizeBytes:   100,
			StartUS:     30_000_000,
			EndUS:       40_000_000,
		},
	}
	plans := []ClipPlan{{StartSec: 30, EndSec: 35}, {StartSec: 35, EndSec: 40}}
	offset, err := stagedSourceCutOffset(staged, plans, 30)
	if err != nil {
		t.Fatalf("stagedSourceCutOffset: %v", err)
	}
	if offset != 0 {
		t.Fatalf("full source offset = %v, want absolute timeline offset 0", offset)
	}
}

func TestStagedSourceWindowMatchesRoundedSectionWireBounds(t *testing.T) {
	staged := &assets.StagedAsset{
		Bytes: 100,
		SourceWindow: &media.SourceWindow{
			AssetSHA256: strings.Repeat("a", 64),
			SizeBytes:   100,
			StartUS:     30_000_000,
			EndUS:       40_000_000,
		},
	}
	plans := []ClipPlan{{StartSec: 30.0004, EndSec: 35.0004}, {StartSec: 35.0004, EndSec: 40.0004}}
	if _, err := stagedSourceCutOffset(staged, plans, 30.0004); err != nil {
		t.Fatalf("millisecond-rounded section bounds rejected: %v", err)
	}
}

func TestStagedSourceWindowRejectsPlanDrift(t *testing.T) {
	staged := &assets.StagedAsset{
		Bytes: 100,
		SourceWindow: &media.SourceWindow{
			AssetSHA256: strings.Repeat("a", 64),
			SizeBytes:   100,
			StartUS:     30_000_000,
			EndUS:       40_000_000,
		},
	}
	_, err := stagedSourceCutOffset(staged, []ClipPlan{{StartSec: 30, EndSec: 39}}, 30)
	if !errors.Is(err, ErrStockStagedSourceWindowMismatch) {
		t.Fatalf("error = %v, want ErrStockStagedSourceWindowMismatch", err)
	}
}
