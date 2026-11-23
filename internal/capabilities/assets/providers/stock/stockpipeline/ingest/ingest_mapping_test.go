package ingest

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"testing"
)

func TestIngestSourceFromClipPlan(t *testing.T) {
	tests := []struct {
		name string
		plan core.ClipPlan
		want Source
	}{
		{
			name: "ordinary source preserves source identity and URL",
			plan: core.ClipPlan{SourceID: "https://example.com/video.mp4", SourceProvider: "stock"},
			want: Source{ID: "https://example.com/video.mp4", URL: "https://example.com/video.mp4"},
		},
		{
			name: "youtube source is canonicalized for staging",
			plan: core.ClipPlan{SourceID: "https://www.youtube.com/watch?v=abc123&pp=tracking", SourceProvider: core.SourceProviderYouTube},
			want: Source{ID: "https://www.youtube.com/watch?v=abc123&pp=tracking", URL: "https://www.youtube.com/watch?v=abc123"},
		},
		{
			name: "empty source remains empty",
			plan: core.ClipPlan{},
			want: Source{},
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

// TestIngestSourcesFromClipPlansGroupsBySource pins the mapping contract: ONE
// entry per distinct source, in first-appearance order. The download counter is
// per source, and the section window is a property of the source's whole clip
// group, so a per-plan list could not carry either.
func TestIngestSourcesFromClipPlansGroupsBySource(t *testing.T) {
	plans := []core.ClipPlan{
		{SourceID: "source-b"},
		{SourceID: "source-a"},
		{SourceID: "source-b"},
	}
	got := ingestSourcesFromClipPlans(plans, false)
	if len(got) != 2 {
		t.Fatalf("got %d sources, want 2 (one per distinct SourceID)", len(got))
	}
	if got[0].ID != "source-b" || got[1].ID != "source-a" {
		t.Fatalf("first-appearance order not preserved: got %q, %q", got[0].ID, got[1].ID)
	}
	for i, source := range got {
		if source.DownloadSection != "" {
			t.Fatalf("source[%d] carries DownloadSection=%q on a non-sectioned run", i, source.DownloadSection)
		}
	}
	unique := UniqueSources(got)
	if len(unique) != 2 || unique[0].ID != "source-b" || unique[1].ID != "source-a" {
		t.Fatalf("UniqueSources(%+v) = %+v, want source-b, source-a", got, unique)
	}
}

// TestIngestSourcesFromClipPlansCarriesTheSectionWindow pins the sections_only
// contract at the boundary: the group's contiguous span becomes the yt-dlp
// --download-sections value that keeps a 40-second actor set from pulling whole
// interviews. The URL stays canonicalized for staging while the ID keeps the
// original SourceID identity.
func TestIngestSourcesFromClipPlansCarriesTheSectionWindow(t *testing.T) {
	const raw = "https://www.youtube.com/watch?v=abc123&pp=tracking"
	plans := []core.ClipPlan{
		{SourceID: raw, SourceProvider: core.SourceProviderYouTube, StartSec: 32, EndSec: 37},
		{SourceID: raw, SourceProvider: core.SourceProviderYouTube, StartSec: 37, EndSec: 42},
	}

	got := ingestSourcesFromClipPlans(plans, true)
	if len(got) != 1 {
		t.Fatalf("got %d sources, want 1", len(got))
	}
	if got[0].ID != raw {
		t.Fatalf("source ID = %q, want the original SourceID %q", got[0].ID, raw)
	}
	if got[0].URL != "https://www.youtube.com/watch?v=abc123" {
		t.Fatalf("source URL = %q, want the canonicalized watch URL", got[0].URL)
	}
	if got[0].DownloadSection != "*00:00:32.000-00:00:42.000" {
		t.Fatalf("DownloadSection = %q, want the group span", got[0].DownloadSection)
	}
}

// TestIngestSourcesFromClipPlansLeavesScatteredGroupsWhole pins the fail-safe:
// a group that is not one contiguous slice must NOT be given a section, because a
// span from the first to the last clip would re-introduce the whole-source
// download (or truncate a source the plan did not ask to truncate).
func TestIngestSourcesFromClipPlansLeavesScatteredGroupsWhole(t *testing.T) {
	plans := []core.ClipPlan{
		{SourceID: "source", StartSec: 0, EndSec: 5},
		{SourceID: "source", StartSec: 100, EndSec: 105},
	}

	got := ingestSourcesFromClipPlans(plans, true)
	if len(got) != 1 {
		t.Fatalf("got %d sources, want 1", len(got))
	}
	if got[0].DownloadSection != "" {
		t.Fatalf("scattered group must stage the whole source, got section %q", got[0].DownloadSection)
	}
}
