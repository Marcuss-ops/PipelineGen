package assets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestStockPublishStep_CapturesDrivePathFromPublishResult pins the
// post-publish sequence: PublishedArtifact.Location.WebViewLink
// captured by PublishedArtifact → ChunkState.DrivePath set on
// the same line. The expected drive path mirrors the
// recordingArtifactPreparation contract (artifactID-based URL).
func TestStockPublishStep_CapturesDrivePathFromPublishResult(t *testing.T) {
	runner := makePR004LegacyRunner(t, &RunInput{
		FolderID:      "wf-pr4-1",
		ClipDuration:  10,
		ChunkDuration: 10,
		PolicyVersion: "policy-v1",
	}, "policy-v1")
	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}
	published := runner.State().Published
	if len(published) != 1 {
		t.Fatalf("expected 1 published chunk, got %d", len(published))
	}
	wantDrivePath := publishedDrivePathFor(published[0].ArtifactID)
	if got := published[0].DrivePath; got != wantDrivePath {
		t.Errorf("chunk[0].DrivePath = %q, want %q (captured from PublishedArtifact.Location.WebViewLink)",
			got, wantDrivePath)
	}
	// godlike/06 SSOT lockstep: DrivePath is the SAME source as
	// RemoteWebViewLink. A future refactor that derives them from
	// distinct sources (e.g. Location.DownloadLink) will surface
	// here as a single test failure.
	if published[0].DrivePath != published[0].RemoteWebViewLink {
		t.Errorf("DrivePath = %q, RemoteWebViewLink = %q (SSOT requires byte-equivalent)",
			published[0].DrivePath, published[0].RemoteWebViewLink)
	}
}

// TestStockPublishStep_CapturesPolicyVersionFromRunInput pins
// the post-publish sequence: RunInput.PolicyVersion
// (operator-supplied) → ChunkState.PolicyVersion stamped on every
// chunk (byte-equivalent across the run for traceability).
func TestStockPublishStep_CapturesPolicyVersionFromRunInput(t *testing.T) {
	const want = "policy-v9-explicit"
	runner := makePR004LegacyRunner(t, &RunInput{
		FolderID:      "wf-pr4-2",
		ClipDuration:  10,
		ChunkDuration: 10,
		PolicyVersion: want,
	}, want)
	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}
	published := runner.State().Published
	if len(published) != 1 {
		t.Fatalf("expected 1 published chunk, got %d", len(published))
	}
	if got := published[0].PolicyVersion; got != want {
		t.Errorf("chunk[0].PolicyVersion = %q, want %q (RunInput.PolicyVersion must propagate to ChunkState)",
			got, want)
	}
}

// TestStockPublishStep_PolicyVersionFallsBackToV1WhenEmpty pins
// the godlike/07 NO-FAKE-AVAILABILITY fallback contract: when
// RunInput.PolicyVersion is empty (or whitespace-only) the
// canonical StockTimestampPolicyVersionV1 = "stock_timestamp_v1"
// literal is stamped on every chunk. A future refactor that lets
// an empty PolicyVersion silently propagate would surface here
// as a single test failure.
func TestStockPublishStep_PolicyVersionFallsBackToV1WhenEmpty(t *testing.T) {
	cases := []struct {
		name          string
		policyVersion string
		wantPolicy    string
	}{
		{"empty string falls back to v1", "", StockTimestampPolicyVersionV1},
		{"whitespace-only falls back to v1", "   \t\n", StockTimestampPolicyVersionV1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := makePR004LegacyRunner(t, &RunInput{
				FolderID:      "wf-pr4-3",
				ClipDuration:  10,
				ChunkDuration: 10,
				PolicyVersion: tc.policyVersion,
			}, tc.policyVersion)
			if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
				t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
			}
			published := runner.State().Published
			if len(published) != 1 {
				t.Fatalf("expected 1 published chunk, got %d", len(published))
			}
			if got := published[0].PolicyVersion; got != tc.wantPolicy {
				t.Errorf("chunk[0].PolicyVersion = %q, want %q (empty RunInput.PolicyVersion must fall back to StockTimestampPolicyVersionV1)",
					got, tc.wantPolicy)
			}
		})
	}
}

// TestStockPublishStep_BothFieldsPropagatedPerChunk pins the
// post-publish sequence across a 3-chunk legacy run: BOTH
// DrivePath and PolicyVersion are stamped on every chunk. The
// per-chunk DriftPath reflects each chunk's own ArtifactID (the
// recordingArtifactPreparation contract derives WebViewLink from
// ArtifactID). PolicyVersion is byte-equivalent across the 3
// chunks (single per-run value).
func TestStockPublishStep_BothFieldsPropagatedPerChunk(t *testing.T) {
	tmpDir := t.TempDir()
	const chunkCount = 3
	paths := make([]string, chunkCount)
	plans := make([]ClipPlan, chunkCount)
	for i := 0; i < chunkCount; i++ {
		p := filepath.Join(tmpDir, fmt.Sprintf("chunk-%d.mp4", i))
		if err := os.WriteFile(p, []byte("chunk-"+strconv.Itoa(i)), 0o644); err != nil {
			t.Fatalf("write chunk %d: %v", i, err)
		}
		paths[i] = p
		plans[i] = ClipPlan{
			SourceID: "https://youtu.be/source-" + strconv.Itoa(i),
			StartSec: float64(10 * i),
			EndSec:   float64(10*i + 10),
		}
	}
	const wantPolicy = "policy-v3-perchunk"
	prep := &recordingArtifactPreparation{}
	runner := &publishFakeRunner{
		runInput: &RunInput{
			FolderID:      "wf-pr4-4",
			ClipDuration:  10,
			ChunkDuration: 10,
			PolicyVersion: wantPolicy,
		},
		cfg: OrchestratorConfig{PolicyVersion: wantPolicy},
		state: &RunState{
			Plan:          plans,
			ComposedPaths: paths,
		},
		artifactPrep: prep,
	}
	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}
	published := runner.State().Published
	if len(published) != chunkCount {
		t.Fatalf("expected %d published chunks, got %d", chunkCount, len(published))
	}
	for i := 0; i < chunkCount; i++ {
		// Per-chunk DrivePath: derived from THIS chunk's ArtifactID
		// (recordingArtifactPreparation contract: WebViewLink uses
		// the artifact's own ArtifactID as the file id segment).
		wantDrivePath := publishedDrivePathFor(published[i].ArtifactID)
		if got := published[i].DrivePath; got != wantDrivePath {
			t.Errorf("chunk[%d].DrivePath = %q, want %q", i, got, wantDrivePath)
		}
		// Per-run PolicyVersion: byte-equivalent across all chunks
		// (locks the pre-compute-once-outside-loop contract).
		if got := published[i].PolicyVersion; got != wantPolicy {
			t.Errorf("chunk[%d].PolicyVersion = %q, want %q (per-run, byte-equivalent across chunks)",
				i, got, wantPolicy)
		}
	}
}

// TestStockPublishStep_PlanDescriptionSync (PR-PLAN-DESCRIPTION-SYNC,
// July 2026): same bug class as the Title MUST-FIX. When
// Plan[i].Description is empty (e.g. a planner that doesn't
// thread Description through to ClipPlan, or a future
// implicit-planner path) but in.Clips[i].Description is
// populated, the canonical per-clip description must reach
// ChunkState.Description — which is then carried into both the
// VerifiedArtifact.Description (write-side seam) and the
// metadata.json chunks[i].description (read-side seam, via
// buildStockRunMetadata). Without this sync, an explicit-clips
// run that lands through a non-front-2 planner path silently
// drops the per-timestamp narration (godlike/07
// NO-FAKE-AVAILABILITY: a silent description drop in
// metadata.json hides Qdrant search-text input from downstream
// consumers).
//
// This test mirrors the canonical Title MUST-FIX pattern
// (TestStockPublishStep_ExplicitClips_PublishesTimestampMetadata
// asserts cs.Title, this test asserts cs.Description). The
// pre-PR bug: a chunk whose Plan.Description="" + Clips[].Description="..."
// surfaced with empty cs.Description, an empty per-clip
// metadata.json chunks[0].description, and an empty run-level
// metadata.json chunks[0].description — all 3 seams lost the
// canonical description. The fix mirrors the Title fix exactly:
// (a) prefer Plan.Description, (b) fall through to
// in.Clips[i].Description, (c) sync back plan.Description so
// perClipLeafName + downstream consumers read the SAME
// source-of-truth.
//
// godlike/07 NO-FAKE-AVAILABILITY contract:
//   - Plan.Description == "" AND in.Clips[i].Description == "X"
//     → cs.Description == "X" (fallback to canonical source)
//   - Plan.Description == "Y" AND in.Clips[i].Description == "X"
//     → cs.Description == "Y" (Plan wins, X is shadowed)
//   - Plan.Description == "" AND in.Clips[i].Description == ""
//     → cs.Description == "" (no fabrication — no fake-availability
//     placeholder literal like "n/a" or "unknown")
func TestStockPublishStep_PlanDescriptionSync(t *testing.T) {
	tmpDir := t.TempDir()
	const want = "Pacquiao fires the first clean left jab and resets the guard."
	clip0 := filepath.Join(tmpDir, "clip0.mp4")
	clip1 := filepath.Join(tmpDir, "clip1.mp4")
	if err := os.WriteFile(clip0, []byte("clip-0"), 0o644); err != nil {
		t.Fatalf("write clip0: %v", err)
	}
	if err := os.WriteFile(clip1, []byte("clip-1"), 0o644); err != nil {
		t.Fatalf("write clip1: %v", err)
	}

	prep := &recordingArtifactPreparation{}
	runner := &publishFakeRunner{
		runInput: &RunInput{
			FolderName: "Round_7_Broner_barcolla",
			Subfolder:  "Pacquiao_Vs_Broner/Round_7_Broner_barcolla/00-00-32_to_00-01-27",
			FolderID:   "wf-desc-sync",
			// In.Clips[i].Description is populated (canonical source)
			// but Plan[i].Description is empty (the MUST-FIX case).
			Clips: []ClipSpec{
				{URL: "https://youtu.be/a", Title: "Round 1", Description: want},
				{URL: "https://youtu.be/a", Title: "Round 2"}, // no description
			},
			ClipDuration:  10,
			ChunkDuration: 10,
			NoEffects:     true,
			NoTransitions: true,
		},
		cfg: OrchestratorConfig{PolicyVersion: "policy-v1"},
		state: &RunState{
			// Both Plan entries have Description = "" (empty) — the
			// MUST-FIX precondition. The fix must populate
			// cs.Description from in.Clips[i].Description for chunk 0
			// and leave it empty for chunk 1 (no canonical source).
			Plan: []ClipPlan{
				{SourceID: "https://youtu.be/a", StartSec: 32, EndSec: 51, Title: "Round 1"},
				{SourceID: "https://youtu.be/a", StartSec: 67, EndSec: 91, Title: "Round 2"},
			},
			ComposedPaths: []string{clip0, clip1},
		},
		artifactPrep: prep,
	}
	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}

	// For explicit-timestamps: N+1 = 2 videos + 1 run-level metadata = 3.
	if got, want := len(prep.artifacts), 3; got != want {
		t.Fatalf("expected %d prepare calls (2 videos + 1 run-level metadata), got %d", want, got)
	}
	// ── PR-PLAN-DESCRIPTION-SYNC contract ──
	// Artifact 0 = video0 (with Description from in.Clips[0].Description).
	if got := prep.artifacts[0].Description; got != want {
		t.Errorf("artifact[0] (video0) Description = %q, want %q (Plan.Description was empty, must fall through to in.Clips[0].Description)",
			got, want)
	}
	// Artifact 1 = video1 (with NO description — no canonical source).
	if got := prep.artifacts[1].Description; got != "" {
		t.Errorf("artifact[1] (video1) Description = %q, want %q (no canonical source; must NOT fabricate)",
			got, "")
	}
	// Sync-back invariant: plan.Description == cs.Description after
	// the explicit-clips block. Read from the ChunkState
	// (runner.State().Published) which carries the post-fix value.
	published := runner.State().Published
	if len(published) != 2 {
		t.Fatalf("expected 2 published chunks, got %d", len(published))
	}
	if got := published[0].Description; got != want {
		t.Errorf("published[0].Description = %q, want %q (sync-back must populate plan.Description from in.Clips[i].Description)",
			got, want)
	}
	if got := published[1].Description; got != "" {
		t.Errorf("published[1].Description = %q, want %q (no canonical source; must stay empty post-sync-back)",
			got, "")
	}
}

// TestStockPublishStep_PlanDescriptionWinsOverClipsSpec pins the
// precedence rule: when BOTH Plan.Description and
// in.Clips[i].Description are populated, Plan.Description wins.
// The fall-through to in.Clips[i].Description is ONLY the
// MUST-FIX case (Plan empty). Mirrors the Title precedence
// rule (TestPerClipLeafName_SlugFromTitle has a Slug-over-Title
// variant — same pattern for the Description pair).
//
// godlike/07 NO-FAKE-AVAILABILITY: the Plan source is the
// canonical write-side seam (the planner's output is the
// authoritative per-clip metadata); the ClipsSpec source is
// the operator-supplied fallback (the original brief that the
// planner could overwrite via front-2 threading). When the
// planner explicitly stamps a description, that wins — we
// don't shadow an authoritative value with a stale ClipsSpec.
func TestStockPublishStep_PlanDescriptionWinsOverClipsSpec(t *testing.T) {
	tmpDir := t.TempDir()
	const planDesc = "Planner-authoritative description for Round 1."
	const clipDesc = "Stale ClipsSpec description that should be SHADOWED."
	clip0 := filepath.Join(tmpDir, "clip0.mp4")
	if err := os.WriteFile(clip0, []byte("clip-0"), 0o644); err != nil {
		t.Fatalf("write clip0: %v", err)
	}
	prep := &recordingArtifactPreparation{}
	runner := &publishFakeRunner{
		runInput: &RunInput{
			FolderName: "Round_7",
			Subfolder:  "Pacquiao_Vs_Broner/Round_7",
			FolderID:   "wf-plan-wins",
			Clips: []ClipSpec{
				{URL: "https://youtu.be/a", Title: "Round 1", Description: clipDesc},
			},
			ClipDuration:  10,
			ChunkDuration: 10,
		},
		cfg: OrchestratorConfig{PolicyVersion: "policy-v1"},
		state: &RunState{
			Plan: []ClipPlan{
				{SourceID: "https://youtu.be/a", StartSec: 32, EndSec: 51, Title: "Round 1", Description: planDesc},
			},
			ComposedPaths: []string{clip0},
		},
		artifactPrep: prep,
	}
	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}
	// For explicit-timestamps: N+1 = 1 video + 1 run-level metadata = 2.
	if got, want := len(prep.artifacts), 2; got != want {
		t.Fatalf("expected %d prepare calls, got %d", want, got)
	}
	if got := prep.artifacts[0].Description; got != planDesc {
		t.Errorf("artifact[0] (video0) Description = %q, want %q (Plan.Description must win over in.Clips[i].Description)",
			got, planDesc)
	}
	// Run-level metadata at index 1
	if got := prep.artifacts[1].ArtifactID; got != "stock:run-fingerprint-123:metadata" {
		t.Errorf("artifact[1] (runMeta) ArtifactID = %q, want %q", got, "stock:run-fingerprint-123:metadata")
	}
	if got := runner.State().Published[0].Description; got != planDesc {
		t.Errorf("published[0].Description = %q, want %q (sync-back must keep Plan value, not ClipsSpec shadow)",
			got, planDesc)
	}
}

// TestStockPublishStep_ExplicitClips_DrivePathOnTimestampChunks
// locks the per-chunk DrivePath capture for the explicit-clips
// (timestamp-mode) path too. The PathLeafName + ArtifactID
// differ from the legacy path (TimestampArtifactID format) but
// the WebViewLink capture contract is the same — the
// recordingArtifactPreparation mock derives the URL from each
// chunk's own ArtifactID, so the expected drive path mirrors
// that contract per-chunk.
func TestStockPublishStep_ExplicitClips_DrivePathOnTimestampChunks(t *testing.T) {
	tmpDir := t.TempDir()
	clip0 := filepath.Join(tmpDir, "clip0.mp4")
	clip1 := filepath.Join(tmpDir, "clip1.mp4")
	if err := os.WriteFile(clip0, []byte("clip-0"), 0o644); err != nil {
		t.Fatalf("write clip0: %v", err)
	}
	if err := os.WriteFile(clip1, []byte("clip-1"), 0o644); err != nil {
		t.Fatalf("write clip1: %v", err)
	}
	prep := &recordingArtifactPreparation{}
	runner := &publishFakeRunner{
		runInput: &RunInput{
			FolderName:    "Round_7",
			Subfolder:     "Pacquiao_Vs_Broner/Round_7/00-00-32_to_00-01-27",
			FolderID:      "wf-pr4-5",
			Clips:         []ClipSpec{{URL: "https://youtu.be/a", Title: "Round 1"}, {URL: "https://youtu.be/a", Title: "Round 2"}},
			ClipDuration:  10,
			ChunkDuration: 10,
			PolicyVersion: "policy-explicit-v1",
		},
		cfg: OrchestratorConfig{PolicyVersion: "policy-explicit-v1"},
		state: &RunState{
			Plan: []ClipPlan{
				{SourceID: "https://youtu.be/a", StartSec: 32, EndSec: 51},
				{SourceID: "https://youtu.be/a", StartSec: 67, EndSec: 91},
			},
			ComposedPaths: []string{clip0, clip1},
		},
		artifactPrep: prep,
	}
	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}
	published := runner.State().Published
	if len(published) != 2 {
		t.Fatalf("expected 2 published timestamp chunks, got %d", len(published))
	}
	for i := 0; i < 2; i++ {
		wantDrivePath := publishedDrivePathFor(published[i].ArtifactID)
		if got := published[i].DrivePath; got != wantDrivePath {
			t.Errorf("chunk[%d].DrivePath = %q, want %q (explicit-clips path captures per-chunk DrivePath)",
				i, got, wantDrivePath)
		}
		if got := published[i].PolicyVersion; got != "policy-explicit-v1" {
			t.Errorf("chunk[%d].PolicyVersion = %q, want %q (explicit-clips path uses RunInput.PolicyVersion)",
				i, got, "policy-explicit-v1")
		}
	}
}
