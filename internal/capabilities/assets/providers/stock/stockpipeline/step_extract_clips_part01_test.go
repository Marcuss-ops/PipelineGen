// Package stockpipeline — step_extract_clips_test.go
// (PR-STOCK-TIMESTAMP-CLIPS Front 4, July 2026).
//
// TDD contract test for StockExtractClipsStep rich-asset write.
// Front 4 replaces the prior 4-field asset.Asset literal (ID +
// Name + Source + MediaType) with a 10-field rich write so
// downstream consumers (Qdrant indexer, asset search, media_assets
// projection) see Title/Description/Round/Tags/Category/LocalPath/
// SHA256/StartSec/EndSec/SearchText at the asset-row write seam
// instead of only in metadata.json emitted later.
//
// godlike/07 fail-closed contracts verified:
//   - Name = "round-7" (slug derived from Title "Round 7")
//   - Filename = "round-7.mp4"
//   - Category = "Boxe" (direct field)
//   - Tags = ["boxing", "pacquiao"] (defensive copy, order preserved)
//   - Metadata["title"] = "Round 7" (raw title preserved verbatim)
//   - Metadata["description"] = "Pacquiao steps in with a quick left cross"
//   - Metadata["round"] = 7
//   - Metadata["start_sec"] = 32
//   - Metadata["end_sec"] = 51
//   - Metadata["slug"] = "round-7"
//   - Metadata["local_path"] = the cutter OutputPath
//   - Metadata["sha256"] non-empty + matches the fileHash argument
//     passed to writer.WriteAndEnqueue (godlike/06 SSOT: read-back
//     mirrors write-side; the fileHash param + the metadata
//     file_hash key carry the same SHA-256)
//   - SearchText contains the canonical segments: title, description,
//     round, category, tags, start_sec, end_sec.
package stockpipeline

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"os"
	"path/filepath"
	"testing"
)

func TestStockExtractClips_BatchPerSourceGroup(t *testing.T) {
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("fake-source-bytes"), 0o644); err != nil {
		t.Fatalf("seed source file: %v", err)
	}

	// 3 clips on the same source.
	plans := []ClipPlan{
		{
			SourceID:        "yt-same-source",
			OutputLogicalID: "planner:batch:0",
			StartSec:        0,
			EndSec:          10,
			PolicyVersion:   "test-policy-v1",
		},
		{
			SourceID:        "yt-same-source",
			OutputLogicalID: "planner:batch:1",
			StartSec:        10,
			EndSec:          20,
			PolicyVersion:   "test-policy-v1",
		},
		{
			SourceID:        "yt-same-source",
			OutputLogicalID: "planner:batch:2",
			StartSec:        20,
			EndSec:          30,
			PolicyVersion:   "test-policy-v1",
		},
	}

	cutter := &batchRecordingCutter{}
	writer := &recordingWriter{}

	state := &RunState{
		Plan: plans,
		StagedAssets: []*assets.StagedAsset{
			{SourceID: "yt-same-source", LocalPath: sourcePath, DurationSec: 60},
		},
	}

	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips: []ClipSpec{
				{URL: "https://www.youtube.com/watch?v=same-source", StartSec: 0, EndSec: 10},
				{URL: "https://www.youtube.com/watch?v=same-source", StartSec: 10, EndSec: 20},
				{URL: "https://www.youtube.com/watch?v=same-source", StartSec: 20, EndSec: 30},
			},
			ClipDuration: 10,
			TotalMinutes: 1,
		},
		cfg: OrchestratorConfig{
			PolicyVersion: "test-policy-v1",
		},
		state: state,
	}
	runner := &extractClipsFakeRunner{
		fakeStepRunner: base,
		writer:         writer,
		cutter:         cutter,
	}

	step := StockExtractClipsStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("step.Run: unexpected error: %v", err)
	}

	// Assert: exactly one CutRequest was issued for the single source group.
	if len(cutter.requests) != 1 {
		t.Fatalf("expected 1 CutRequest for a single source group, got %d", len(cutter.requests))
	}

	req := cutter.requests[0]
	if req.SourcePath != sourcePath {
		t.Errorf("CutRequest.SourcePath = %q, want %q", req.SourcePath, sourcePath)
	}
	if len(req.Jobs) != len(plans) {
		t.Fatalf("CutRequest.Jobs length = %d, want %d", len(req.Jobs), len(plans))
	}

	// Assert: CutJobs are in stable order and match the plans timestamps.
	for i, plan := range plans {
		job := req.Jobs[i]
		if job.StartSec != plan.StartSec {
			t.Errorf("job[%d].StartSec = %v, want %v", i, job.StartSec, plan.StartSec)
		}
		if job.EndSec != plan.EndSec {
			t.Errorf("job[%d].EndSec = %v, want %v", i, job.EndSec, plan.EndSec)
		}
		if job.OutputPath == "" {
			t.Errorf("job[%d].OutputPath is empty", i)
		}
	}

	// Assert: writer was called once per produced clip.
	if writer.calls != len(plans) {
		t.Errorf("writer.calls = %d, want %d", writer.calls, len(plans))
	}
}

// TestStockExtractClips_BatchPerMultipleSourceGroups asserts that the
// step emits one CutRequest per distinct SourceID and that each
// CutRequest carries the correct subset of jobs plus the propagated
// SourceDuration and NoAudio values.
func TestStockExtractClips_BatchPerMultipleSourceGroups(t *testing.T) {
	tmpDir := t.TempDir()

	sourceA := filepath.Join(tmpDir, "sourceA.mp4")
	sourceB := filepath.Join(tmpDir, "sourceB.mp4")
	for _, p := range []string{sourceA, sourceB} {
		if err := os.WriteFile(p, []byte("fake-source-"+p), 0o644); err != nil {
			t.Fatalf("seed source file: %v", err)
		}
	}

	plans := []ClipPlan{
		{SourceID: "yt-source-a", OutputLogicalID: "planner:a:0", StartSec: 0, EndSec: 5, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-source-b", OutputLogicalID: "planner:b:0", StartSec: 5, EndSec: 10, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-source-a", OutputLogicalID: "planner:a:1", StartSec: 10, EndSec: 15, PolicyVersion: "test-policy-v1"},
	}

	cutter := &batchRecordingCutter{}
	writer := &recordingWriter{}

	state := &RunState{
		Plan: plans,
		StagedAssets: []*assets.StagedAsset{
			{SourceID: "yt-source-a", LocalPath: sourceA, DurationSec: 60},
			{SourceID: "yt-source-b", LocalPath: sourceB, DurationSec: 120},
		},
	}

	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips: []ClipSpec{
				{URL: "https://www.youtube.com/watch?v=source-a", StartSec: 0, EndSec: 5},
				{URL: "https://www.youtube.com/watch?v=source-b", StartSec: 5, EndSec: 10},
				{URL: "https://www.youtube.com/watch?v=source-a", StartSec: 10, EndSec: 15},
			},
			ClipDuration: 5,
			TotalMinutes: 1,
			NoAudio:      true,
		},
		cfg: OrchestratorConfig{
			PolicyVersion: "test-policy-v1",
		},
		state: state,
	}
	runner := &extractClipsFakeRunner{
		fakeStepRunner: base,
		writer:         writer,
		cutter:         cutter,
	}

	step := StockExtractClipsStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("step.Run: unexpected error: %v", err)
	}

	// Assert: exactly two CutRequests (one per source group).
	if len(cutter.requests) != 2 {
		t.Fatalf("expected 2 CutRequests for 2 source groups, got %d", len(cutter.requests))
	}

	// Map source path -> request for stable assertions.
	reqBySource := make(map[string]*CutRequest)
	for i := range cutter.requests {
		reqBySource[cutter.requests[i].SourcePath] = &cutter.requests[i]
	}

	// Source A request.
	reqA, ok := reqBySource[sourceA]
	if !ok {
		t.Fatalf("missing CutRequest for source A")
	}
	if len(reqA.Jobs) != 2 {
		t.Errorf("source A CutRequest.Jobs length = %d, want 2", len(reqA.Jobs))
	}
	if reqA.SourceDuration != 60 {
		t.Errorf("source A CutRequest.SourceDuration = %v, want 60", reqA.SourceDuration)
	}
	if !reqA.NoAudio {
		t.Errorf("source A CutRequest.NoAudio = %v, want true", reqA.NoAudio)
	}

	// Source B request.
	reqB, ok := reqBySource[sourceB]
	if !ok {
		t.Fatalf("missing CutRequest for source B")
	}
	if len(reqB.Jobs) != 1 {
		t.Errorf("source B CutRequest.Jobs length = %d, want 1", len(reqB.Jobs))
	}
	if reqB.SourceDuration != 120 {
		t.Errorf("source B CutRequest.SourceDuration = %v, want 120", reqB.SourceDuration)
	}
	if !reqB.NoAudio {
		t.Errorf("source B CutRequest.NoAudio = %v, want true", reqB.NoAudio)
	}

	// Writer called once per produced clip.
	if writer.calls != len(plans) {
		t.Errorf("writer.calls = %d, want %d", writer.calls, len(plans))
	}
}
