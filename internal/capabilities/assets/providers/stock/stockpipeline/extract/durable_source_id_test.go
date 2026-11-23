package extract

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
)

func TestDurableSourceIDForSectionsOnlyGroup(t *testing.T) {
	const sourceURL = "https://www.youtube.com/watch?v=source"
	const stageKey = "https://www.youtube.com/watch?v=source#section-000"

	got := DurableSourceIDForGroup(stageKey, []core.ClipPlan{{
		SourceID: sourceURL,
		StageKey: stageKey,
	}})
	if got != sourceURL {
		t.Fatalf("durable source identity = %q, want original source URL %q", got, sourceURL)
	}
	if fallback := DurableSourceIDForGroup(stageKey, nil); fallback != stageKey {
		t.Fatalf("empty group fallback = %q, want stage key %q", fallback, stageKey)
	}
}
