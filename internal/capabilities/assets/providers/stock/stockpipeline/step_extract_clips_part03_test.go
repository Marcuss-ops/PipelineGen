package stockpipeline

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
//     SetLegacyFileMD5 accessor populates BOTH the explicit "sha256"
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

	state := &RunState{
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

	// Assert 14: Metadata["file_hash"] ALSO populated by SetLegacyFileMD5
	// (godlike/06 SSOT: SetLegacyFileMD5 accessor populates BOTH the
	// explicit "sha256" key + the typed-accessor "file_hash" key
	// for legacy/canonical compat).
	if got := writer.clip.Metadata["file_hash"]; got != sha256 {
		t.Errorf("Metadata[file_hash] = %q, want %q (must mirror sha256 — SetLegacyFileMD5 populates both)", got, sha256)
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
