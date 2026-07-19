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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// recordingWriter captures the asset + fileHash arguments to
// WriteAndEnqueue for the test assertion surface. It is the
// canonical "write-side observed" seam (godlike/06 SSOT one
// canonical observer per fact).
type recordingWriter struct {
	calls    int
	clip     *asset.Asset
	fileHash string
}

func (w *recordingWriter) WriteAndEnqueue(_ context.Context, clip *asset.Asset, fileHash string) error {
	w.calls++
	w.clip = clip
	w.fileHash = fileHash
	return nil
}

// extractClipsFakeRunner is the canonical "extract-clips-isolated"
// test fixture. It embeds fakeStepRunner from step_plan_clips_test.go
// (proven pattern) and overrides ONLY the accessors the
// StockExtractClipsStep actually calls (Cutter, Writer, State via
// the embedded state field).
type extractClipsFakeRunner struct {
	*fakeStepRunner
	writer       TransactionalAssetWriter
	cutter       VideoCutter
	artifactPrep finalization.ArtifactPreparationService
}

func (r *extractClipsFakeRunner) Cutter() VideoCutter { return r.cutter }
func (r *extractClipsFakeRunner) Writer() TransactionalAssetWriter {
	return r.writer
}
func (r *extractClipsFakeRunner) ArtifactPreparation() finalization.ArtifactPreparationService {
	return r.artifactPrep
}

// deterministicRichCutter is a focused VideoCutter stub that
// returns a single Succeeded item with the configured output path.
// Front 4 needs a real file on disk at the returned path (the
// step's job.ComputeSHA256 call would otherwise fail-closed), so
// the test pre-creates the file before invoking step.Run.
type deterministicRichCutter struct {
	outputPath string
}

func (d *deterministicRichCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	items := make([]CutItemResult, len(req.Jobs))
	for i, j := range req.Jobs {
		items[i] = CutItemResult{
			JobID:      j.OutputPath,
			OutputPath: d.outputPath,
			Status:     CutItemStatusSucceeded,
			SizeBytes:  1024,
		}
	}
	return CutBatchResult{
		SourcePath: req.SourcePath,
		Items:      items,
	}, nil
}

// recordingCutter is a focused VideoCutter stub that records the
// number of Cut() invocations. PR-STOCK-TIMESTAMP-CLIPS Front 5
// uses it to assert the probe-before-cut contract: when the
// pre-cut duration validation fails, the cutter must NOT be
// invoked (the step aborts before reaching cutter.Cut). The
// returned CutBatchResult is empty (no items) so any code path
// that erroneously reaches the cutter surface immediately
// surfaces a "zero cut files" production gate (terminal error
// per the existing stock.extract_clips zero-cut-files gate).
type recordingCutter struct {
	calls int
}

func (r *recordingCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	r.calls++
	return CutBatchResult{
		SourcePath: req.SourcePath,
		Items:      nil,
	}, nil
}

// batchRecordingCutter records every CutRequest it receives so
// tests can assert batch-cutting behaviour: one CutRequest per
// source group containing all CutJobs for that group.
type batchRecordingCutter struct {
	requests []CutRequest
}

func (b *batchRecordingCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	b.requests = append(b.requests, req)
	items := make([]CutItemResult, len(req.Jobs))
	for i, j := range req.Jobs {
		// Create the output file so the step's SHA256 compute succeeds.
		if err := os.WriteFile(j.OutputPath, []byte("fake-clip-"+j.OutputPath), 0o644); err != nil {
			return CutBatchResult{}, fmt.Errorf("batchRecordingCutter: create output file %q: %w", j.OutputPath, err)
		}
		items[i] = CutItemResult{
			JobID:      j.OutputPath,
			OutputPath: j.OutputPath,
			Status:     CutItemStatusSucceeded,
			SizeBytes:  1024,
		}
	}
	return CutBatchResult{
		SourcePath: req.SourcePath,
		Items:      items,
	}, nil
}

// concurrencyTrackingArtifactPrep is a fake ArtifactPreparationService
// that records the maximum number of concurrent Prepare calls. It is
// used to verify that the stock.extract_clips upload worker pool
// respects its concurrency limit.
type concurrencyTrackingArtifactPrep struct {
	mu            sync.Mutex
	current       int
	maxConcurrent int
	calls         int
	delay         time.Duration
}

func (p *concurrencyTrackingArtifactPrep) Prepare(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	p.mu.Lock()
	p.current++
	if p.current > p.maxConcurrent {
		p.maxConcurrent = p.current
	}
	p.calls++
	p.mu.Unlock()

	if p.delay > 0 {
		time.Sleep(p.delay)
	}

	p.mu.Lock()
	p.current--
	p.mu.Unlock()

	return finalization.PublishedArtifact{
		ArtifactID: artifact.ArtifactID,
		Filename:   artifact.Filename,
		MIMEType:   artifact.MIMEType,
		SizeBytes:  artifact.SizeBytes,
		SHA256:     artifact.SHA256,
		Location: finalization.AssetLocation{
			Provider:    "drive",
			FileID:      "file-" + artifact.ArtifactID,
			WebViewLink: "https://drive.google.com/file/d/file-" + artifact.ArtifactID,
		},
	}, nil
}

// TestStockExtractClips_BatchPerSourceGroup asserts the batch-cutting
// contract: all ClipPlan entries sharing the same SourceID are folded
// into a single CutRequest with multiple CutJobs, and the cutter is
// invoked exactly once per source group (not once per clip).
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

	state := &runState{
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

	state := &runState{
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

// TestStockExtractClips_OutOfRange_FailsClosed is the Front 5
// regression guard. It asserts the pre-cut duration validation
// contract per user spec literal "fallire subito con errore
// leggibile":
//
//  1. StagedAsset.DurationSec is the canonical SSOT for the
//     source duration (60s in this test).
//  2. 1 plan with EndSec=999 (way out of range).
//  3. step.Run returns an error wrapping ErrStockClipsOutOfRange
//     (errors.Is must succeed).
//  4. The cutter is NOT invoked (probe-before-cut contract:
//     the step aborts before reaching cutter.Cut).
//  5. The writer is NOT invoked (no asset write on step failure).
//
// This closes the silent-success class where a clip with EndSec
// > source duration was being cut by ffmpeg (which silently
// truncates or produces a half-broken artifact) and shipped to
// Drive. Per user spec, no auto-clamp; the operator must fix the
// input.
func TestStockExtractClips_OutOfRange_FailsClosed(t *testing.T) {
	// Arrange: source + output paths (real files so the test
	// never reaches the SHA256-compute step — the out-of-range
	// check fires BEFORE the cutter.Cut call).
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("fake-source-bytes"), 0o644); err != nil {
		t.Fatalf("seed source file: %v", err)
	}

	// Arrange: 1 plan with EndSec=999 (way out of range).
	plan := ClipPlan{
		SourceID:        "yt-bad-timestamp",
		OutputLogicalID: "planner:oor:0",
		StartSec:        0,
		EndSec:          999, // >>>> source duration 60
		PolicyVersion:   "test-policy-v1",
	}

	// Arrange: writer + cutter (both must NOT be invoked).
	writer := &recordingWriter{}
	cutter := &recordingCutter{}

	state := &runState{
		Plan: []ClipPlan{plan},
		StagedAssets: []*assets.StagedAsset{
			{
				SourceID:    plan.SourceID,
				LocalPath:   sourcePath,
				DurationSec: 60, // canonical SSOT for the test
			},
		},
	}

	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips: []ClipSpec{{
				URL:      "https://www.youtube.com/watch?v=bad-timestamp",
				StartSec: 0,
				EndSec:   999,
			}},
			ClipDuration: 19,
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

	// Act.
	step := StockExtractClipsStep{}
	err := step.Run(context.Background(), runner)

	// Assert 1: error is non-nil.
	if err == nil {
		t.Fatal("step.Run returned nil error — expected ErrStockClipsOutOfRange, got nil")
	}

	// Assert 2: errors.Is(err, ErrStockClipsOutOfRange) (godlike/07
	// typed-error contract: callers probe via errors.Is, not by
	// parsing the human-readable prefix).
	if !errors.Is(err, ErrStockClipsOutOfRange) {
		t.Errorf("err = %v, want errors.Is(err, ErrStockClipsOutOfRange) == true", err)
	}

	// Assert 2b: error message carries the operator-readable
	// diagnostic context per user spec "errore leggibile"
	// (EndSec=999.00, source duration=60.00, overrun=939.00s,
	// clip index 0, artifact id). Without these, a future
	// refactor that returns the bare sentinel loses the
	// operator-readable context the user spec explicitly demands.
	for _, sub := range []string{
		"clip[0]",
		"EndSec=999.00",
		"duration=60.00",
		"overrun=939.00s",
		"planner:oor:0",
	} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("err.Error() missing %q\ngot: %v", sub, err)
		}
	}

	// Assert 3: cutter NOT invoked (probe-before-cut contract).
	if cutter.calls != 0 {
		t.Errorf("cutter.calls = %d, want 0 (probe-before-cut contract: step must abort before cutter.Cut)", cutter.calls)
	}

	// Assert 4: writer NOT invoked (no asset write on step failure).
	if writer.calls != 0 {
		t.Errorf("writer.calls = %d, want 0 (no asset write when step fails pre-cut)", writer.calls)
	}
}

// TestStockExtractClipsStep_RichAssetWrite is the Front 4
// regression guard. It asserts that the rich-asset write emits
// ALL 10 fields correctly on a Round 7 Pacquiao/Broner clip.
//
// Setup:
//   - 1 staged source at <tmpDir>/source.mp4
//   - 1 plan for Round 7 (Title="Round 7", Description="...",
//     Round=7, Category="Boxe", Tags=["boxing","pacquiao"],
//     StartSec=32, EndSec=51)
//   - cutter returns 1 Succeeded item with OutputPath = real file
//     on disk (so job.ComputeSHA256 succeeds)
//   - writer is the recordingWriter (captures asset + fileHash)
//
// Assertions (godlike/07 typed-error contract: every assertion
// tests a single field — failure pinpoints exactly which field
// regressed):
//  1. writer.calls == 1 (the step reached the write seam for
//     this clip — pre-Front-4 with broken asset literal, the
//     step would have called writer too, but with 4 fields only)
//  2. Name == "round-7" (slug derived via perClipLeafName cascade)
//  3. Filename == "round-7.mp4"
//  4. Category == "Boxe" (direct field)
//  5. Tags == ["boxing", "pacquiao"] (defensive copy preserved)
//  6. Metadata["title"] == "Round 7"
//  7. Metadata["description"] == "Pacquiao steps in with a quick left cross"
//  8. Metadata["round"] == 7 (int cast-safe through interface{})
//  9. Metadata["start_sec"] == 32.0 (float64 cast-safe)
//  10. Metadata["end_sec"] == 51.0
//  11. Metadata["slug"] == "round-7"
//  12. Metadata["local_path"] == cutter OutputPath
//  13. Metadata["sha256"] non-empty + matches writer.fileHash
//  14. Metadata["file_hash"] also populated (godlike/06 SSOT
//     SetFileHash accessor populates BOTH the explicit "sha256"
//     key + the typed-accessor "file_hash" key)
//  15. SearchText contains all 7 expected segments
func TestStockExtractClipsStep_RichAssetWrite(t *testing.T) {
	// Arrange: real output file on disk so job.ComputeSHA256
	// succeeds (the step's fail-closed contract aborts the run
	// on hash-compute failure; we must give it a real file).
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.mp4")
	outputPath := filepath.Join(tmpDir, "round-7.mp4")
	// Deterministic file content so SHA256 is reproducible.
	if err := os.WriteFile(sourcePath, []byte("fake-source-bytes-for-Round-7"), 0o644); err != nil {
		t.Fatalf("seed source file: %v", err)
	}
	if err := os.WriteFile(outputPath, []byte("fake-clip-bytes-for-Round-7"), 0o644); err != nil {
		t.Fatalf("seed output file: %v", err)
	}

	// Arrange: 1 explicit plan for Round 7.
	want := ClipPlan{
		SourceID:        "yt-pacquiao-broner-2019",
		OutputLogicalID: "planner:r7:0",
		StartSec:        32,
		EndSec:          51,
		Title:           "Round 7",
		Description:     "Pacquiao steps in with a quick left cross",
		Round:           7,
		Category:        "Boxe",
		Tags:            []string{"boxing", "pacquiao"},
		PolicyVersion:   "test-policy-v1",
	}
	wantSlug := "round-7"
	wantDescription := "Pacquiao steps in with a quick left cross"

	cutter := &deterministicRichCutter{outputPath: outputPath}
	writer := &recordingWriter{}

	state := &runState{
		Plan: []ClipPlan{want},
		StagedAssets: []*assets.StagedAsset{
			{SourceID: want.SourceID, LocalPath: sourcePath},
		},
	}

	// Use a base fakeStepRunner via the canonical pattern from
	// step_plan_clips_test.go, then layer the writer + cutter
	// overrides via the embedded pointer.
	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips: []ClipSpec{{
				URL:         "https://www.youtube.com/watch?v=pacquiao-broner",
				StartSec:    32,
				EndSec:      51,
				Title:       want.Title,
				Description: want.Description,
				Round:       want.Round,
				Category:    want.Category,
				Tags:        append([]string(nil), want.Tags...),
			}},
			ClipDuration: 19,
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

	// Act.
	step := StockExtractClipsStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("step.Run: unexpected error: %v", err)
	}

	// Assert 1: writer reached for this clip.
	if writer.calls != 1 {
		t.Fatalf("writer.calls = %d, want 1 (step must reach the write seam once per cut clip)", writer.calls)
	}
	if writer.clip == nil {
		t.Fatal("writer.clip is nil — step did not pass an asset to WriteAndEnqueue")
	}

	// Assert 2: Name = slug.
	if got := writer.clip.Name; got != wantSlug {
		t.Errorf("Name = %q, want %q (slug from Title %q)", got, wantSlug, want.Title)
	}

	// Assert 3: Filename = slug + ".mp4".
	if got := writer.clip.Filename; got != wantSlug+".mp4" {
		t.Errorf("Filename = %q, want %q", got, wantSlug+".mp4")
	}

	// Assert 4: Category direct field.
	if got := writer.clip.Category; got != want.Category {
		t.Errorf("Category = %q, want %q", got, want.Category)
	}

	// Assert 5: Tags defensive copy preserved order + content.
	if got := writer.clip.Tags; len(got) != 2 || got[0] != "boxing" || got[1] != "pacquiao" {
		t.Errorf("Tags = %v, want [boxing pacquiao] (defensive copy preserves order)", got)
	}

	// Assert 6: Metadata["title"] raw title preserved.
	if got := writer.clip.Metadata["title"]; got != want.Title {
		t.Errorf("Metadata[title] = %q, want %q (raw title preserved verbatim)", got, want.Title)
	}

	// Assert 7: Metadata["description"] = want description.
	if got := writer.clip.Metadata["description"]; got != wantDescription {
		t.Errorf("Metadata[description] = %q, want %q", got, wantDescription)
	}

	// Assert 8: Metadata["round"] = 7 (int via interface{} = the
	// original Go type is preserved when assigned via map literal,
	// so cast to int is safe).
	if got, ok := writer.clip.Metadata["round"].(int); !ok || got != 7 {
		t.Errorf("Metadata[round] = %v (%T), want 7 (int)", writer.clip.Metadata["round"], writer.clip.Metadata["round"])
	}

	// Assert 9: Metadata["start_sec"] = 32.0.
	if got, ok := writer.clip.Metadata["start_sec"].(float64); !ok || got != 32.0 {
		t.Errorf("Metadata[start_sec] = %v (%T), want 32.0 (float64)", writer.clip.Metadata["start_sec"], writer.clip.Metadata["start_sec"])
	}

	// Assert 10: Metadata["end_sec"] = 51.0.
	if got, ok := writer.clip.Metadata["end_sec"].(float64); !ok || got != 51.0 {
		t.Errorf("Metadata[end_sec] = %v (%T), want 51.0 (float64)", writer.clip.Metadata["end_sec"], writer.clip.Metadata["end_sec"])
	}

	// Assert 11: Metadata["slug"] = "round-7".
	if got := writer.clip.Metadata["slug"]; got != wantSlug {
		t.Errorf("Metadata[slug] = %q, want %q", got, wantSlug)
	}

	// Assert 12: Metadata["local_path"] = cutter OutputPath.
	if got := writer.clip.Metadata["local_path"]; got != outputPath {
		t.Errorf("Metadata[local_path] = %q, want %q", got, outputPath)
	}

	// Assert 13: Metadata["sha256"] non-empty + matches writer.fileHash.
	sha256, ok := writer.clip.Metadata["sha256"].(string)
	if !ok || sha256 == "" {
		t.Fatalf("Metadata[sha256] = %v (%T), want non-empty string", writer.clip.Metadata["sha256"], writer.clip.Metadata["sha256"])
	}
	if writer.fileHash != sha256 {
		t.Errorf("writer.fileHash = %q, Metadata[sha256] = %q — godlike/06 SSOT: read-back must mirror write-side", writer.fileHash, sha256)
	}

	// Assert 14: Metadata["file_hash"] ALSO populated by SetFileHash
	// (godlike/06 SSOT: SetFileHash accessor populates BOTH the
	// explicit "sha256" key + the typed-accessor "file_hash" key
	// for legacy/canonical compat).
	if got := writer.clip.Metadata["file_hash"]; got != sha256 {
		t.Errorf("Metadata[file_hash] = %q, want %q (must mirror sha256 — SetFileHash populates both)", got, sha256)
	}

	// Assert 15: SearchText contains all 7 expected segments.
	st := writer.clip.SearchText
	wantSubstrings := []string{
		"Stock video clip",
		"title: Round 7",
		"description: Pacquiao steps in with a quick left cross",
		"round 7",
		"category: Boxe",
		"tags: boxing, pacquiao",
		"start_sec: 32",
		"end_sec: 51",
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(st, sub) {
			t.Errorf("SearchText missing %q\ngot: %s", sub, st)
		}
	}
}

// TestStockExtractClips_UploadWorkerPoolLimitsConcurrency asserts that
// artifact uploads are performed by a bounded worker pool (max 2
// concurrent Prepare calls per source group) and that the resulting
// chunks remain ordered by clip index with deterministic clip_001.mp4
// filenames.
func TestStockExtractClips_UploadWorkerPoolLimitsConcurrency(t *testing.T) {
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("fake-source-bytes"), 0o644); err != nil {
		t.Fatalf("seed source file: %v", err)
	}

	// 5 clips on the same source so the worker pool has work to do.
	plans := []ClipPlan{
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:0", StartSec: 0, EndSec: 5, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:1", StartSec: 5, EndSec: 10, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:2", StartSec: 10, EndSec: 15, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:3", StartSec: 15, EndSec: 20, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:4", StartSec: 20, EndSec: 25, PolicyVersion: "test-policy-v1"},
	}

	prep := &concurrencyTrackingArtifactPrep{delay: 50 * time.Millisecond}
	cutter := &batchRecordingCutter{}
	writer := &recordingWriter{}

	state := &runState{
		Plan: plans,
		StagedAssets: []*assets.StagedAsset{
			{SourceID: "yt-upload-pool", LocalPath: sourcePath, DurationSec: 60},
		},
	}

	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips: []ClipSpec{
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 0, EndSec: 5},
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 5, EndSec: 10},
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 10, EndSec: 15},
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 15, EndSec: 20},
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 20, EndSec: 25},
			},
			ClipDuration: 5,
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
		artifactPrep:   prep,
	}

	step := StockExtractClipsStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("step.Run: unexpected error: %v", err)
	}

	// Assert: all clips were uploaded plus one metadata.json for the
	// single timestamp group (5 clips + 1 metadata = 6 Prepare calls).
	wantCalls := len(plans) + 1
	if prep.calls != wantCalls {
		t.Errorf("Prepare calls = %d, want %d", prep.calls, wantCalls)
	}

	// Assert: concurrency never exceeded 2 and the pool actually
	// parallelized work (max concurrent must be exactly 2 when there
	// are more than 2 clips to upload).
	if prep.maxConcurrent > 2 {
		t.Errorf("max concurrent uploads = %d, want <= 2", prep.maxConcurrent)
	}
	if prep.maxConcurrent < 2 {
		t.Errorf("max concurrent uploads = %d, want 2 (pool did not parallelize)", prep.maxConcurrent)
	}

	// Assert: published chunks are in clip-index order and use the
	// deterministic clip_001.mp4, clip_002.mp4, ... filenames.
	published := runner.State().Published
	if len(published) != len(plans) {
		t.Fatalf("published chunks = %d, want %d", len(published), len(plans))
	}
	for i, chunk := range published {
		wantFilename := fmt.Sprintf("clip_%03d.mp4", i+1)
		if chunk.Filename != wantFilename {
			t.Errorf("published[%d].Filename = %q, want %q", i, chunk.Filename, wantFilename)
		}
		if chunk.Index != i {
			t.Errorf("published[%d].Index = %d, want %d", i, chunk.Index, i)
		}
	}
}
