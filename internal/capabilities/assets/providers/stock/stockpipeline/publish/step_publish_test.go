package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

type recordingArtifactPreparation struct {
	artifacts []finalization.VerifiedArtifact
}

func TestPerClipLeafName_SlugFromTitle(t *testing.T) {
	cases := []struct {
		name string
		plan ClipPlan
		want string
	}{
		{
			name: "Pacquiao/Broner user diagnostic example — Italian title slugifies to canonical Drive folder",
			plan: ClipPlan{Title: "Round 7 - Broner barcolla", StartSec: 993, EndSec: 1048},
			want: "round-7-broner-barcolla",
		},
		{
			name: "title with accented chars — unicode letters preserved (à stays à), then collapsed into canonical slug",
			plan: ClipPlan{Title: "Round 1 - La fase di studio e la velocità di Pacquiao", StartSec: 32, EndSec: 231},
			want: "round-1-la-fase-di-studio-e-la-velocità-di-pacquiao",
		},
		{
			name: "title with colons — SafeFolderName strips them",
			plan: ClipPlan{Title: "Round 5: il miglior momento di Broner", StartSec: 628, EndSec: 767},
			want: "round-5-il-miglior-momento-di-broner",
		},
		{
			name: "title with parentheses — SafeFolderName strips them",
			plan: ClipPlan{Title: "Verdict (official)", StartSec: 1727, EndSec: 1769},
			want: "verdict-official",
		},
		{
			name: "title with multiple spaces — collapsed to single hyphens",
			plan: ClipPlan{Title: "Round  7   Broner", StartSec: 993, EndSec: 1048},
			want: "round-7-broner",
		},
		{
			name: "title with leading/trailing whitespace — trimmed before slugify",
			plan: ClipPlan{Title: "   Round 7   ", StartSec: 993, EndSec: 1048},
			want: "round-7",
		},
		{
			name: "single-word title",
			plan: ClipPlan{Title: "Highlight", StartSec: 0, EndSec: 30},
			want: "highlight",
		},
		{
			name: "title with mixed case — lowercased after SafeFolderName",
			plan: ClipPlan{Title: "Round 7 BRONER BARCOLLA", StartSec: 993, EndSec: 1048},
			want: "round-7-broner-barcolla",
		},
		{
			name: "all-whitespace title falls through to start-end literal (no 'untitled' leak)",
			plan: ClipPlan{Title: "   \t\n", StartSec: 32, EndSec: 231},
			want: "00-00-32_to_00-03-51",
		},
		{
			name: "empty title falls through to start-end literal",
			plan: ClipPlan{Title: "", StartSec: 0, EndSec: 60},
			want: "00-00-00_to_00-01-00",
		},
		{
			name: "title that SafeFolderName reduces to empty falls through to start-end literal",
			plan: ClipPlan{Title: "///", StartSec: 10, EndSec: 20},
			want: "00-00-10_to_00-00-20",
		},
		{
			name: "underscore in title is preserved (not stripped like unsafe chars) — distinguishes 'user_name' from 'username'",
			plan: ClipPlan{Title: "user_name highlight", StartSec: 0, EndSec: 30},
			want: "user_name-highlight",
		},
		{
			name: "underscore between round number and title is preserved as a word boundary (avoids 'round_7-broner' becoming 'round7-broner')",
			plan: ClipPlan{Title: "round_7_broner_barcolla", StartSec: 993, EndSec: 1048},
			want: "round_7_broner_barcolla",
		},
		// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): Slug
		// override cases. When ClipPlan.Slug is non-empty, it
		// wins over the Title-derived slug. Falls through to
		// title cascade only when Slug is empty/whitespace or
		// sanitizes to "untitled".
		{
			name: "Slug override wins over title-derived slug",
			plan: ClipPlan{Title: "Round 7 - Broner barcolla", Slug: "veredito-ufficiale", StartSec: 993, EndSec: 1048},
			want: "veredito-ufficiale",
		},
		{
			name: "Slug wins when title is empty (no fallback to start-end)",
			plan: ClipPlan{Slug: "round-7", StartSec: 993, EndSec: 1048},
			want: "round-7",
		},
		{
			name: "Slug with whitespace falls through to title cascade",
			plan: ClipPlan{Title: "Round 7 - Broner barcolla", Slug: "   \t\n", StartSec: 993, EndSec: 1048},
			want: "round-7-broner-barcolla",
		},
		{
			name: "Slug with unsafe chars is sanitized via SafeFolderName",
			plan: ClipPlan{Title: "Round 7 - Broner barcolla", Slug: "Round/7:Broner?barcolla", StartSec: 993, EndSec: 1048},
			want: "Round_7_Broner_barcolla",
		},
		{
			name: "Slug that sanitizes to empty falls through to title cascade",
			plan: ClipPlan{Title: "Round 7 - Broner barcolla", Slug: "///", StartSec: 993, EndSec: 1048},
			want: "round-7-broner-barcolla",
		},
		{
			name: "Slug that sanitizes to pure-punctuation falls through to title cascade (no '___' shadow folder)",
			plan: ClipPlan{Title: "Round 7 - Broner barcolla", Slug: "!!!", StartSec: 993, EndSec: 1048},
			want: "round-7-broner-barcolla",
		},
		{
			name: "empty Slug + empty Title falls through to start-end literal (regression guard: no empty leaf)",
			plan: ClipPlan{Slug: "", Title: "", StartSec: 32, EndSec: 51},
			want: "00-00-32_to_00-00-51",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := perClipLeafName(tc.plan)
			if got != tc.want {
				t.Fatalf("perClipLeafName(%+v) = %q, want %q", tc.plan, got, tc.want)
			}
		})
	}
}

// TestStockPublishStep_ExplicitClips_SharedTimestampPathLeafName locks
// the integration contract: when explicitTimestamps is true, every
// 5-second child from the same parent timestamp lands in the SAME Drive
// folder leaf. The files differ by filename (clip_001.mp4, clip_002.mp4,
// etc.); the folder leaf is the timestamp block from the run subfolder.
func TestStockPublishStep_ExplicitClips_SharedTimestampPathLeafName(t *testing.T) {
	tmpDir := t.TempDir()
	const clipCount = 8
	paths := make([]string, clipCount)
	plans := make([]ClipPlan, clipCount)
	// Realistic Pacquiao/Broner round titles (subset of the user
	// diagnostic. The folder leaf comes from the run subfolder, not
	// from these child titles.
	titles := []string{
		"Round 1 - La fase di studio",
		"Round 2 - Primi scambi",
		"Round 5 - Miglior momento di Broner",
		"Round 7 - Broner barcolla",
		"Round 9 - Pacquiao attacco",
		"Round 10-11 - Controllo Pacquiao",
		"Round 12 - Finale",
		"Verdetto ufficiale",
	}
	for i := 0; i < clipCount; i++ {
		p := filepath.Join(tmpDir, fmt.Sprintf("clip-%d.mp4", i))
		if err := os.WriteFile(p, []byte("clip-"+strconv.Itoa(i)), 0o644); err != nil {
			t.Fatalf("write clip %d: %v", i, err)
		}
		paths[i] = p
		plans[i] = ClipPlan{
			SourceID: "https://youtu.be/pacquiao-broner",
			StartSec: float64(30 * i),
			EndSec:   float64(30*i + 25),
			Title:    titles[i],
		}
	}
	prep := &recordingArtifactPreparation{}
	runInput := &RunInput{
		FolderName:    "Manny Pacquiao vs Adrien Broner",
		FolderID:      "wf-8clips",
		Subfolder:     "Pacquiao_Vs_Broner/Round_7_Broner_barcolla/00-00-32_to_00-01-27",
		Clips:         make([]ClipSpec, clipCount), // explicit-clips
		ClipDuration:  25,
		ChunkDuration: 25,
	}
	runner := &publishFakeRunner{
		runInput: runInput,
		cfg:      OrchestratorConfig{PolicyVersion: "policy-v1"},
		state: &RunState{
			Plan:          plans,
			ComposedPaths: paths,
		},
		artifactPrep: prep,
	}
	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}
	// Structural invariant for explicit-timestamps: N+1 artifacts =
	// N videos + 1 run-level metadata. Per-clip metadata is published
	// by step_extract_clips. For 8 clips: 8 + 1 = 9.
	want := clipCount + 1
	if got := len(prep.artifacts); got != want {
		t.Fatalf("expected %d prepare calls (%d videos + 1 run-level metadata), got %d", want, clipCount, got)
	}
	// Shared PathLeafName contract: each chunk's leaf matches the
	// parent timestamp label derived from the run subfolder.
	wantLeaf := stockTimestampParentGroupName(runInput)
	for i := 0; i < clipCount; i++ {
		got := prep.artifacts[i].PathLeafName
		if got != wantLeaf {
			t.Errorf("video[%d] (artifact[%d]).PathLeafName = %q, want %q (shared timestamp leaf)",
				i, i, got, wantLeaf)
		}
	}
	// Run-level metadata lives in the SAME explicit timestamp folder.
	metaIdx := clipCount
	if got := prep.artifacts[metaIdx].PathLeafName; got != wantLeaf {
		t.Errorf("runMeta PathLeafName = %q, want %q (shared explicit timestamp leaf)", got, wantLeaf)
	}
	if got := prep.artifacts[metaIdx].ArtifactID; got != "stock:run-fingerprint-123:metadata" {
		t.Errorf("runMeta ArtifactID = %q, want %q", got, "stock:run-fingerprint-123:metadata")
	}
}

func TestStockPublishStep_ExplicitClips_PublishesTimestampMetadata(t *testing.T) {
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
			FolderName:    "Round_7_Broner_barcolla",
			Subfolder:     "Pacquiao_Vs_Broner/Round_7_Broner_barcolla/00-00-32_to_00-01-27",
			FolderID:      "wf-123",
			Clips:         []ClipSpec{{URL: "https://youtu.be/a", Title: "Round 1", Description: "Pacquiao fires the first clean left."}, {URL: "https://youtu.be/a", Title: "Round 2", Description: "Broner tries to reset and circle out."}},
			ClipDuration:  10,
			ChunkDuration: 10,
			NoEffects:     true,
			NoTransitions: true,
		},
		cfg: OrchestratorConfig{
			PolicyVersion: "policy-v1",
		},
		state: &RunState{
			Plan: []ClipPlan{
				{SourceID: "https://youtu.be/a", StartSec: 32, EndSec: 51, Title: "Round 1", Description: "Pacquiao fires the first clean left."},
				{SourceID: "https://youtu.be/a", StartSec: 67, EndSec: 91, Title: "Round 2", Description: "Broner tries to reset and circle out."},
			},
			ComposedPaths: []string{clip0, clip1},
		},
		artifactPrep: prep,
	}

	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}

	// For explicit-timestamps: N+1 artifacts = N videos + 1 run-level
	// metadata. Per-clip metadata is published by step_extract_clips.
	// For 2 clips: 2 + 1 = 3 artifacts. Artifacts are [video0, video1, runMeta].
	if got, want := len(prep.artifacts), 3; got != want {
		t.Fatalf("expected %d prepare calls (2 videos + 1 run-level metadata), got %d", want, got)
	}
	// Index 0: video0
	if got := prep.artifacts[0].ArtifactID; got != "stock:run-fingerprint-123:timestamp:0:video" {
		t.Fatalf("unexpected artifact[0] (video0) id: %q", got)
	}
	if got := prep.artifacts[0].Description; got != "Pacquiao fires the first clean left." {
		t.Fatalf("unexpected artifact[0] (video0) description: %q", got)
	}
	if got := prep.artifacts[0].PathLeafName; got != "Round_7_Broner_barcolla" {
		t.Fatalf("unexpected artifact[0] (video0) path leaf: %q, want %q (shared timestamp leaf from Subfolder parent)", got, "Round_7_Broner_barcolla")
	}
	// Index 1: video1
	if got := prep.artifacts[1].ArtifactID; got != "stock:run-fingerprint-123:timestamp:1:video" {
		t.Fatalf("unexpected artifact[1] (video1) id: %q", got)
	}
	if got := prep.artifacts[1].Description; got != "Broner tries to reset and circle out." {
		t.Fatalf("unexpected artifact[1] (video1) description: %q", got)
	}
	if got := prep.artifacts[1].PathLeafName; got != "Round_7_Broner_barcolla" {
		t.Fatalf("unexpected artifact[1] (video1) path leaf: %q, want %q (shared timestamp leaf from Subfolder parent)", got, "Round_7_Broner_barcolla")
	}
	// Index 2: runMeta (run-level metadata.json at the run-root "metadata" leaf)
	if got := prep.artifacts[2].ArtifactID; got != "stock:run-fingerprint-123:metadata" {
		t.Fatalf("unexpected artifact[2] (runMeta) id: %q", got)
	}
	if got := prep.artifacts[2].PathLeafName; got != "Round_7_Broner_barcolla" {
		t.Fatalf("unexpected artifact[2] (runMeta) path leaf: %q, want %q (shared explicit timestamp leaf)", got, "Round_7_Broner_barcolla")
	}
	if runner.State().MetadataPublished.LocalPath == "" {
		t.Fatal("expected metadata published state to be populated")
	}
	if runner.State().MetadataPublished.RemoteFileID == "" {
		t.Fatal("expected metadata published remote file id to be populated")
	}
	if got := runner.State().Published; len(got) != 2 {
		t.Fatalf("expected 2 published timestamp videos, got %d", len(got))
	}
}

// TestStockRootFolderName_LegacyFallbackChain locks the contract that
// stockRootFolderName follows a deterministic 5-rule fallback chain
// when neither folder_name nor clips[] is supplied by the caller (the
// legacy "stock search-only" / "stock direct-url" path). Each sub-test
// pins ONE fallback rule in isolation so a future regression surfaces
// as a single failure rather than a chain ambiguity.
//
// godlike/07 typed-error contract: pre-change the function returned
// "stock" or "untitled" for empty inputs (illegible on Drive). The
// new chain ends with a UTC date stamp so an empty-input run still
// gets a per-day distinguishable folder.
func TestStockRootFolderName_LegacyFallbackChain(t *testing.T) {
	// Stable per run: capture the date the test RUN started so all
	// date-fallback cases compare identically (Go's time.Now is
	// monotonic within a single test execution window).
	expectedDate := "stock_" + time.Now().UTC().Format("2006-01-02")
	cases := []struct {
		name string
		in   *RunInput
		want string
	}{
		{
			name: "FolderName wins over every other source (no behavior change)",
			in: &RunInput{
				FolderName:    "Round_7_Broner_barcolla",
				Subfolder:     "PacquiaoRound_7",
				SearchQueries: []string{"boxing training gym"},
				DirectURLs:    []string{"https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/BigBuckBunny.mp4"},
			},
			want: "Round_7_Broner_barcolla",
		},
		{
			name: "Subfolder wins when FolderName empty (no behavior change)",
			in: &RunInput{
				Subfolder:     "PacquiaoRound_7",
				SearchQueries: []string{"boxing training gym"},
			},
			want: "PacquiaoRound_7",
		},
		{
			name: "whitespace-only FolderName falls through to Subfolder (regression guard for pkg/pathutil.SafeFolderName 'untitled' default)",
			in: &RunInput{
				FolderName: "   ",
				Subfolder:  "PacquiaoRound_7",
			},
			want: "PacquiaoRound_7",
		},
		{
			name: "whitespace-only FolderName + empty Subfolder falls through to legacy fallback chain",
			in: &RunInput{
				FolderName: "\t\n",
				Subfolder:  "",
			},
			want: expectedDate, // legacy date fallback (universal)
		},
		{
			name: "first SearchQuery (sanitized) wins for legacy search-only run",
			in: &RunInput{
				SearchQueries: []string{"boxing training gym", "mike tyson knockout"},
			},
			want: "boxing training gym",
		},
		{
			name: "first DirectURL basename (sans extension) wins for legacy direct-url run",
			in: &RunInput{
				DirectURLs: []string{"https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/BigBuckBunny.mp4"},
			},
			want: "BigBuckBunny",
		},
		{
			name: "DirectURL strips query and fragment layers before basenaming",
			in: &RunInput{
				DirectURLs: []string{"https://x.com/file.mp4?v=1&t=2#anchor"},
			},
			want: "file",
		},
		{
			name: "whitespace-only SearchQueries fall through to DirectURLs",
			in: &RunInput{
				SearchQueries: []string{"   ", "\t\n"},
				DirectURLs:    []string{"https://example.com/path/Some_Movie.mp4"},
			},
			want: "Some_Movie", // underscores preserved by SafeFolderName
		},
		{
			name: "all-empty inputs land on UTC date fallback (universal)",
			in:   &RunInput{},
			want: expectedDate,
		},
		{
			name: "nil RunInput returns stock (preserved pre-change behavior)",
			in:   nil,
			want: "stock",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stockRootFolderName(tc.in)
			if got != tc.want {
				t.Fatalf("stockRootFolderName(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStockPublishStep_LegacyRunMetadata_RemainsSingleArtifact(t *testing.T) {
	tmpDir := t.TempDir()
	chunk := filepath.Join(tmpDir, "chunk.mp4")
	if err := os.WriteFile(chunk, []byte("chunk"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}

	prep := &recordingArtifactPreparation{}
	runner := &publishFakeRunner{
		runInput: &RunInput{
			FolderID:      "wf-456",
			ClipDuration:  10,
			ChunkDuration: 10,
		},
		cfg: OrchestratorConfig{
			PolicyVersion: "policy-v1",
		},
		state: &RunState{
			Plan:          []ClipPlan{{SourceID: "https://youtu.be/a", StartSec: 10, EndSec: 20}},
			ComposedPaths: []string{chunk},
		},
		artifactPrep: prep,
	}

	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}

	// PR-PER-CLIP-METADATA (July 2026, DoD 5): 2N+1 artifacts =
	// 1 video + 1 per-clip metadata + 1 run-level metadata = 3.
	if got, want := len(prep.artifacts), 3; got != want {
		t.Fatalf("expected %d prepare calls (1 video + 1 per-clip metadata + 1 run metadata), got %d", want, got)
	}
	// Index 0: video
	if got := prep.artifacts[0].ArtifactID; got != "stock:run-fingerprint-123:chunk:0" {
		t.Fatalf("unexpected video artifact id: %q, want %q", got, "stock:run-fingerprint-123:chunk:0")
	}
	// Index 1: per-clip metadata sidecar
	if got := prep.artifacts[1].ArtifactID; got != "stock:run-fingerprint-123:chunk:0:metadata" {
		t.Fatalf("unexpected per-clip metadata artifact id: %q, want %q", got, "stock:run-fingerprint-123:chunk:0:metadata")
	}
	if got := prep.artifacts[1].Kind; got != finalization.KindMetadata {
		t.Fatalf("unexpected per-clip metadata kind: %q, want %q (KindMetadata)", got, finalization.KindMetadata)
	}
	// Index 2: run-level metadata
	if got := prep.artifacts[2].ArtifactID; got != "stock:run-fingerprint-123:metadata" {
		t.Fatalf("unexpected run-level metadata artifact id: %q", got)
	}
}

// TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName locks
// the structural invariant for legacy runs (no `clips[]`): exactly
// ONE metadata.json per run regardless of how many chunks the
// pipeline produced, and all chunks in the run share a SINGLE
// PathLeafName (the stockTimestampGroupName fallback chain, NOT a
// per-chunk derivation). This mirrors the explicit-clips structural
// invariant consolidated in commit 61a7aba7 (one group folder +
// one metadata per group) so legacy runs stay consistent with the
// same Drive tree shape.
//
// godlike/06 SSOT: locks structural alignment between explicit-clips
// and legacy paths; a future refactor that re-introduces per-chunk
// metadata publishes (or per-chunk PathLeafName drift via a new
// stockTimestampLeafName call inside the chunk loop) will surface
// as a SINGLE test failure rather than an ambient Drive drift.
func TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName(t *testing.T) {
	cases := []struct {
		name           string
		runInput       *RunInput
		wantSharedLeaf string
	}{
		{
			name: "legacy Subfolder basename is shared by all chunks + single metadata",
			runInput: &RunInput{
				FolderName:    "Round_7_Broner_barcolla",
				Subfolder:     "Pacquiao_Vs_Broner/Round_7_Broner_barcolla/00-00-10_to_00-00-40",
				FolderID:      "wf-789",
				ClipDuration:  10,
				ChunkDuration: 10,
			},
			wantSharedLeaf: "00-00-10_to_00-00-40",
		},
		{
			name: "legacy FolderName alone (no Subfolder) is shared by all chunks + single metadata",
			runInput: &RunInput{
				FolderName:    "PacquiaoHighligts",
				FolderID:      "wf-790",
				ClipDuration:  10,
				ChunkDuration: 10,
			},
			wantSharedLeaf: "PacquiaoHighligts",
		},
		{
			name: "legacy empty FolderName + empty Subfolder: 'metadata' fallback shared by all",
			runInput: &RunInput{
				FolderID:      "wf-791",
				ClipDuration:  10,
				ChunkDuration: 10,
			},
			wantSharedLeaf: "metadata",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			prep := &recordingArtifactPreparation{}
			runner := &publishFakeRunner{
				runInput: tc.runInput,
				cfg:      OrchestratorConfig{PolicyVersion: "policy-v1"},
				state: &RunState{
					Plan:          plans,
					ComposedPaths: paths,
				},
				artifactPrep: prep,
			}
			if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
				t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
			}
			// Structural invariant: 2N+1 artifacts = N videos + N per-clip
			// metadata + 1 run-level metadata (PR-PER-CLIP-METADATA, July
			// 2026, DoD 5). For 3 legacy chunks: 3 + 3 + 1 = 7. Legacy mode
			// also emits per-clip metadata (one per video chunk) so the
			// 2N+1 contract applies symmetrically to legacy + explicit-clips
			// paths; the per-clip metadata.json lands in the SAME shared
			// leaf folder as the video chunks in legacy mode (vs the
			// per-clip subdir alignment in explicit-clips mode).
			want := 2*chunkCount + 1
			if got := len(prep.artifacts); got != want {
				t.Fatalf("expected %d prepare calls (%d videos + %d per-clip metadata + 1 run-level metadata), got %d",
					want, chunkCount, chunkCount, got)
			}
			// All video chunks share the SAME PathLeafName (NOT a
			// per-chunk derivation). This is the structural alignment
			// with the explicit-clips path's one-folder-per-group shape.
			// PR-PER-CLIP-METADATA: artifacts are interleaved
			// [video0, clipMeta0, video1, clipMeta1, video2, clipMeta2, runMeta].
			for i := 0; i < chunkCount; i++ {
				videoIdx := 2 * i
				if got := prep.artifacts[videoIdx].PathLeafName; got != tc.wantSharedLeaf {
					t.Errorf("video[%d] (artifact[%d]) PathLeafName = %q, want %q (shared across all legacy chunks)",
						i, videoIdx, got, tc.wantSharedLeaf)
				}
				// Per-clip metadata MUST share the same PathLeafName as the
				// sibling video (DoD 5: per-clip metadata sidecar in the
				// SAME subdir). For legacy, the shared leaf is the
				// timestampGroupName (e.g. "00-00-10_to_00-00-40" or
				// "PacquiaoHighligts" or "metadata").
				clipMetaIdx := 2*i + 1
				if got := prep.artifacts[clipMetaIdx].PathLeafName; got != tc.wantSharedLeaf {
					t.Errorf("clipMeta[%d] (artifact[%d]) PathLeafName = %q, want %q (per-clip metadata sidecar MUST live in the same subdir as the sibling video)",
						i, clipMetaIdx, got, tc.wantSharedLeaf)
				}
			}
			// Legacy chunk ArtifactID format invariant: stock:<fp>:chunk:<i>.
			// Locks the legacy chunk-naming AND prevents regression of
			// per-clip timestamp dirs in legacy (the explicit-clips bug
			// user just consolidated). A future refactor that introduces
			// TimestampArtifactID(fp, i, "video") inside the loop for the
			// LEGACY path would surface here as a single sub-test failure.
			// PR-PER-CLIP-METADATA: the per-clip metadata ArtifactID
			// format is stock:<fp>:chunk:<i>:metadata (mirrors the video
			// format with :metadata suffix for legacy mode).
			for i := 0; i < chunkCount; i++ {
				videoIdx := 2 * i
				clipMetaIdx := 2*i + 1
				wantVideoID := "stock:run-fingerprint-123:chunk:" + strconv.Itoa(i)
				if got := prep.artifacts[videoIdx].ArtifactID; got != wantVideoID {
					t.Errorf("video[%d] (artifact[%d]) ArtifactID = %q, want %q (legacy chunk-naming invariant)",
						i, videoIdx, got, wantVideoID)
				}
				wantClipMetaID := wantVideoID + ":metadata"
				if got := prep.artifacts[clipMetaIdx].ArtifactID; got != wantClipMetaID {
					t.Errorf("clipMeta[%d] (artifact[%d]) ArtifactID = %q, want %q (legacy per-clip metadata ArtifactID mirrors video format with :metadata suffix)",
						i, clipMetaIdx, got, wantClipMetaID)
				}
				if got := prep.artifacts[clipMetaIdx].Kind; got != finalization.KindMetadata {
					t.Errorf("clipMeta[%d] (artifact[%d]) Kind = %q, want %q (KindMetadata)",
						i, clipMetaIdx, got, finalization.KindMetadata)
				}
			}
			// Run-level metadata is exactly ONE, with matching PathLeafName.
			metaIdx := 2 * chunkCount
			wantMetaID := "stock:run-fingerprint-123:metadata"
			if got := prep.artifacts[metaIdx].ArtifactID; got != wantMetaID {
				t.Errorf("runMeta ArtifactID = %q, want %q", got, wantMetaID)
			}
			if got := prep.artifacts[metaIdx].PathLeafName; got != tc.wantSharedLeaf {
				t.Errorf("runMeta PathLeafName = %q, want %q (matches shared leaf across legacy chunks)",
					got, tc.wantSharedLeaf)
			}
		})
	}
}

// ── PR-004 (July 2026) — DrivePath + PolicyVersion on ChunkState ──
//
// Per user spec: capture DrivePath (from
// PublishedArtifact.Location.WebViewLink) + PolicyVersion (from
// RunInput.PolicyVersion with hardcoded fallback to
// StockTimestampPolicyVersionV1 = "stock_timestamp_v1") on the
// Phase 1 per-chunk loop. The tests below pin the post-publish
// sequence: prepare-call returns a PublishedArtifact → the next
// line writes its WebViewLink into ChunkState.DrivePath →
// the ChunkState propagates into runner.State().Published.

// publishedDrivePathFor mirrors the recordingArtifactPreparation's
// WebViewLink contract so the test assertions can compute the
// expected per-chunk drive path without coupling to the mock's
// internal state.
