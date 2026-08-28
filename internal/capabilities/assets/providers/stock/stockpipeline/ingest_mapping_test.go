package stockpipeline

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/ingest"
)

func TestIngestSourceFromClipPlan(t *testing.T) {
	tests := []struct {
		name string
		plan ClipPlan
		want ingest.Source
	}{
		{
			name: "ordinary source preserves source identity and URL",
			plan: ClipPlan{SourceID: "https://example.com/video.mp4", SourceProvider: "stock"},
			want: ingest.Source{ID: "https://example.com/video.mp4", URL: "https://example.com/video.mp4"},
		},
		{
			name: "youtube source is canonicalized for staging",
			plan: ClipPlan{SourceID: "https://www.youtube.com/watch?v=abc123&pp=tracking", SourceProvider: SourceProviderYouTube},
			want: ingest.Source{ID: "https://www.youtube.com/watch?v=abc123&pp=tracking", URL: "https://www.youtube.com/watch?v=abc123"},
		},
		{
			name: "empty source remains empty",
			plan: ClipPlan{},
			want: ingest.Source{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ingestSourceFromClipPlan(tc.plan); got != tc.want {
				t.Fatalf("ingestSourceFromClipPlan(%+v) = %+v, want %+v", tc.plan, got, tc.want)
			}
		})
	}
}

func TestIngestSourcesFromClipPlansPreservesOrderAndDuplicates(t *testing.T) {
	plans := []ClipPlan{
		{SourceID: "source-b"},
		{SourceID: "source-a"},
		{SourceID: "source-b"},
	}
	got := ingestSourcesFromClipPlans(plans)
	if len(got) != len(plans) {
		t.Fatalf("got %d sources, want %d", len(got), len(plans))
	}
	for i, source := range got {
		if source.ID != plans[i].SourceID {
			t.Fatalf("source[%d].ID = %q, want %q", i, source.ID, plans[i].SourceID)
		}
	}
	unique := ingest.UniqueSources(got)
	if len(unique) != 2 || unique[0].ID != "source-b" || unique[1].ID != "source-a" {
		t.Fatalf("UniqueSources(%+v) = %+v, want source-b, source-a", got, unique)
	}
}
