package stockpipeline

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestDeriveRunCountsUsesCompletedStages(t *testing.T) {
	input := &RunInput{DirectURLs: []string{"a", "b"}}
	state := &runState{
		Plan:         []ClipPlan{{SourceID: "a"}, {SourceID: "a"}, {SourceID: "b"}},
		StagedAssets: []*assets.StagedAsset{{SourceID: "a"}, {SourceID: "b"}},
		CutPaths:     []string{"one", "two", "three"},
		Published:    []ChunkState{{}, {}, {}},
		FinalStatus:  job.StatusSucceeded,
	}
	got := deriveRunCounts(input, state)
	if got.RequestedVideoCount != 2 || got.SelectedVideoCount != 2 || got.DownloadedVideoCount != 2 {
		t.Fatalf("source counts = %+v", got)
	}
	if got.PlannedClipCount != 3 || got.CreatedClipCount != 3 || got.PublishedClipCount != 3 || got.IndexedClipCount != 3 {
		t.Fatalf("clip counts = %+v", got)
	}
}

func TestValidateRunCountsRejectsPartialSuccess(t *testing.T) {
	if err := ValidateRunCounts(RunCounts{
		SelectedVideoCount: 10, DownloadedVideoCount: 10,
		PlannedClipCount: 150, CreatedClipCount: 150,
		PublishedClipCount: 149, PersistedClipCount: 149, IndexedClipCount: 149,
	}); err == nil {
		t.Fatal("expected partial publication to prevent SUCCEEDED")
	}
}

func TestValidateRunCountsAcceptsCompleteRun(t *testing.T) {
	if err := ValidateRunCounts(RunCounts{
		SelectedVideoCount: 10, DownloadedVideoCount: 10,
		PlannedClipCount: 150, CreatedClipCount: 150,
		PublishedClipCount: 150, PersistedClipCount: 150, IndexedClipCount: 150,
	}); err != nil {
		t.Fatalf("complete run rejected: %v", err)
	}
}
