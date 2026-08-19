package usecase

import (
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestProjectCanonicalTimingsRegeneratesVidRushFields pins that the legacy
// GenerationTimings DTO is fully regenerated as a projection of the canonical
// RunReport stages: orchestration stages map to their named fields, the
// VidRush postprocessor stages map to their legacy named fields, and no value
// is measured a second time.
func TestProjectCanonicalTimingsRegeneratesVidRushFields(t *testing.T) {
	report := &kernobs.RunReport{
		WallTimeMs: 9999,
		Stages: []kernobs.StageReport{
			{Name: "source.resolve", DurationMs: 100},
			{Name: "script.plan", DurationMs: 200},
			{Name: "script.engine", DurationMs: 300},
			{Name: "entities", DurationMs: 11},
			{Name: "clip_search", DurationMs: 22},
			{Name: "internet_images", DurationMs: 33},
			{Name: "images", DurationMs: 44},
			{Name: "persistence", DurationMs: 55},
			{Name: "clip_bindings", DurationMs: 66},
			{Name: "script.postprocess", DurationMs: 700}, // orchestration, not a VidRush named field
		},
	}

	got := projectCanonicalTimings(report)

	if got.SourceResolveMs != 100 || got.PlanBuildMs != 200 || got.EngineMs != 300 || got.TotalMs != 9999 {
		t.Fatalf("orchestration projection = %+v", got)
	}
	if got.SegmentExtractionMs != 11 || got.QueryGenerationMs != 11 ||
		got.ArtlistSearchMs != 22 || got.InternetImageSearchMs != 33 ||
		got.ImageGenerationMs != 44 || got.SQLiteMs != 55 || got.BindingMs != 66 {
		t.Fatalf("VidRush named-field projection = %+v", got)
	}
	if got.PostprocessMs["entities"] != 11 || got.PostprocessMs["clip_bindings"] != 66 {
		t.Fatalf("PostprocessMs must carry the processor stages, got %v", got.PostprocessMs)
	}
	if got.PostprocessMs["script.postprocess"] != 0 {
		t.Fatalf("orchestration stage must not leak into PostprocessMs, got %v", got.PostprocessMs)
	}
}
