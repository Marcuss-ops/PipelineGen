package adapters

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestInternetImageCandidateLimit(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{}
	if got := internetImageCandidateLimit(plan); got != 10 {
		t.Fatalf("default candidate limit = %d, want 10", got)
	}
	plan.MediaPlan.Planner.CandidateLimit = 80
	if got := internetImageCandidateLimit(plan); got != 50 {
		t.Fatalf("capped candidate limit = %d, want 50", got)
	}
}

func TestInternetImagePerQueryLimitSharesSceneBudgetAcrossEntities(t *testing.T) {
	for _, tc := range []struct{ sceneLimit, queries, want int }{
		{30, 1, 30}, {30, 5, 6}, {30, 10, 3}, {30, 15, 2},
	} {
		if got := internetImagePerQueryLimit(tc.sceneLimit, tc.queries); got != tc.want {
			t.Errorf("per-query limit for scene budget %d across %d queries = %d, want %d", tc.sceneLimit, tc.queries, got, tc.want)
		}
	}
}

func TestInternetImageCacheReadable(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{}
	if !internetImageCacheReadable(plan, internetImageProcessOptions{cacheOnly: true}) {
		t.Fatal("cache-only mode must read cache")
	}
	plan.MediaPlan.Mode = mediadomain.MediaPlanModeAuto
	plan.MediaPlan.ForceRefreshAssets = true
	if internetImageCacheReadable(plan, internetImageProcessOptions{}) {
		t.Fatal("forced refresh must bypass cache")
	}
}

func TestInternetImageCacheMissWarning(t *testing.T) {
	want := "internet_images: cache-only miss for segment segment-1"
	if got := internetImageCacheMissWarning("segment-1"); got != want {
		t.Fatalf("warning = %q, want %q", got, want)
	}
}
