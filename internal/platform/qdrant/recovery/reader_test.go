package recovery

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/enrichment"
	"testing"
)

func TestBetterPrefersRicherHistoricalText(t *testing.T) {
	a := &enrichment.HistoricalText{Description: "long description", SearchText: "tokens", Projection: "z"}
	b := &enrichment.HistoricalText{Description: "short", SearchText: "x", Projection: "a"}
	if !better(a, b) {
		t.Fatal("richer text must win")
	}
}
