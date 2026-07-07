package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

type recordingArtifactPreparation struct {
	artifacts []finalization.VerifiedArtifact
}

func (r *recordingArtifactPreparation) Prepare(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	r.artifacts = append(r.artifacts, artifact)
	return finalization.PublishedArtifact{
		ArtifactID: artifact.ArtifactID,
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       artifact.ArtifactID + "-file",
			WebViewLink:  "https://drive.google.com/file/d/" + artifact.ArtifactID + "/view",
			DownloadLink: "https://drive.google.com/uc?id=" + artifact.ArtifactID,
			FolderID:     "folder-123",
			FolderPath:   "stock/test",
		},
	}, nil
}

type publishFakeRunner struct {
	runInput     *RunInput
	cfg          OrchestratorConfig
	state        *runState
	artifactPrep finalization.ArtifactPreparationService
}

func (f *publishFakeRunner) Cfg() OrchestratorConfig           { return f.cfg }
func (f *publishFakeRunner) RunInput() *RunInput               { return f.runInput }
func (f *publishFakeRunner) JobID() string                     { return "publish-test-job" }
func (f *publishFakeRunner) PolicyVersion() string             { return f.cfg.PolicyVersion }
func (f *publishFakeRunner) Planner() ClipPlanner              { return nil }
func (f *publishFakeRunner) SourceStager() assets.SourceStager { return nil }
func (f *publishFakeRunner) Cutter() VideoCutter               { return nil }
func (f *publishFakeRunner) Renderer() StockRenderer           { return nil }
func (f *publishFakeRunner) Builder() ManifestBuilder          { return nil }
func (f *publishFakeRunner) Writer() TransactionalAssetWriter  { return nil }
func (f *publishFakeRunner) Projection() ProjectionPort        { return nil }
func (f *publishFakeRunner) SourceDurationProbe() SourceDurationProbe {
	return nil
}
func (f *publishFakeRunner) ArtifactPreparation() finalization.ArtifactPreparationService {
	return f.artifactPrep
}
func (f *publishFakeRunner) JobFinalizer() finalization.JobFinalizer { return nil }
func (f *publishFakeRunner) RunFingerprint() string                  { return "run-fingerprint-123" }
func (f *publishFakeRunner) Log() *zap.Logger                        { return zap.NewNop() }
func (f *publishFakeRunner) State() *runState                        { return f.state }

var _ StepRunner = (*publishFakeRunner)(nil)

// TestStockPublishStep_PerClipPathLeafName locks the per-clip
// PathLeafName contract for explicit-clips runs as a HARD regression
// guard. The pre-PR bug stamped the SAME PathLeafName
// (= stockTimestampGroupName) on every chunk in an explicit-clips
// run, so 8 Pacquiao/Broner clips landed in 1 Drive subdir instead
// of 8 per-clip subdirs.
//
// User spec (Front 3, July 2026): "8 explicit clips → 8 unique leaf
// names, NO shared folder". This test HARD-FAILS on any duplicate
// leaf (godlike/07 NO-FAKE-AVAILABILITY: a silent duplicate shadow
// folder on Drive is the regression we just fixed). The sibling
// TestStockPublishStep_ExplicitClips_PerClipPathLeafName logs
// duplicate leaves as a soft warning (operator visibility); this
// test is the canonical regression guard for the per-clip contract
// itself.
//
// 8 titles picked so that the slugifyTitle cascade produces 8
// distinct slugs — Round 1, Round 2, Round 3, Round 4, Round 5,
// Round 6, Round 7, Round 8 → round-1, round-2, ..., round-8.
// The fix to perClipLeafName's gate on explicitTimestamps is
// load-bearing: a regression that drops the gate (e.g. falls back
// to stockTimestampGroupName for every chunk) would collapse all 8
// leaves to a single value and HARD-FAIL this test.
func TestStockPublishStep_PerClipPathLeafName(t *testing.T) {
	tmpDir := t.TempDir()
	const clipCount = 8
	paths := make([]string, clipCount)
	plans := make([]ClipPlan, clipCount)
	wantLeaves := []string{
		"round-1", "round-2", "round-3", "round-4",
		"round-5", "round-6", "round-7", "round-8",
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
			Title:    "Round " + strconv.Itoa(i+1),
		}
	}
	prep := &recordingArtifactPreparation{}
	runner := &publishFakeRunner{
		runInput: &RunInput{
			FolderName:    "Manny Pacquiao vs Adrien Broner",
			FolderID:      "wf-8clips-front3",
			Clips:         make([]ClipSpec, clipCount), // explicit-clips trigger
			ClipDuration:  25,
			ChunkDuration: 25,
		},
		cfg: OrchestratorConfig{PolicyVersion: "policy-v1"},
		state: &runState{
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
	// 2026, DoD 5). For 8 clips: 8 + 8 + 1 = 17.
	want := 2*clipCount + 1
	if got := len(prep.artifacts); got != want {
		t.Fatalf("expected %d prepare calls (%d videos + %d per-clip metadata + 1 run-level metadata), got %d", want, clipCount, clipCount, got)
	}
	// Per-clip PathLeafName contract: 8 unique leaves, no shared
	// folder. This is the HARD regression guard — godlike/07
	// NO-FAKE-AVAILABILITY: a silent duplicate would shadow real
	// Drive folders without operator visibility, so the test fails
	// immediately on any collision (vs the sibling test which only
	// logs a soft warning).
	//
	// The VerifiedArtifact.PathLeafName is the write-side seam (the
	// value passed to ArtifactPreparation.Prepare). The ChunkState
	// captures RemoteFileID + RemoteWebViewLink + DrivePath +
	// RemoteDownloadLink from the PublishedArtifact.Location but
	// does NOT carry PathLeafName today (a future PR could add it
	// for SSOT round-trip verification — see forward-pointer
	// PR-CHUNKSTATE-PATHLEAFNAME-MIRROR).
	//
	// PR-PER-CLIP-METADATA: the 2N+1 contract means artifacts
	// are interleaved [video0, clipMeta0, video1, clipMeta1, ...,
	// runMeta]. The video at index 2*i and its sibling per-clip
	// metadata at index 2*i+1 share the same PathLeafName.
	seenLeaves := make(map[string]int, clipCount)
	for i := 0; i < clipCount; i++ {
		videoIdx := 2 * i
		clipMetaIdx := 2*i + 1
		// Video at 2*i. Use a separate declaration (not the
		// `if got := ...; got != ...` short-decl form) so the
		// outer `got` stays in scope for the `seenLeaves[got]`
		// collision check below.
		got := prep.artifacts[videoIdx].PathLeafName
		if got != wantLeaves[i] {
			t.Errorf("video[%d] (artifact[%d]).PathLeafName = %q, want %q (per-clip leaf from Plan[%d].Title=%q)",
				i, videoIdx, got, wantLeaves[i], i, plans[i].Title)
		}
		if prev, dup := seenLeaves[got]; dup {
			t.Errorf("SHARED FOLDER REGRESSION: video[%d] (artifact[%d]).PathLeafName = %q, but video[%d] already used this leaf — 8 clips must produce 8 unique leaves, not 1 shared folder",
				i, videoIdx, got, prev)
		}
		seenLeaves[got] = i
		// Per-clip metadata at 2*i+1 MUST share the same PathLeafName
		// as the sibling video (PR-PER-CLIP-METADATA DoD 5: per-clip
		// metadata.json lands in the SAME per-clip subdir as the video).
		if gotMeta := prep.artifacts[clipMetaIdx].PathLeafName; gotMeta != wantLeaves[i] {
			t.Errorf("clipMeta[%d] (artifact[%d]).PathLeafName = %q, want %q (per-clip metadata sidecar MUST live in the same subdir as the sibling video)",
				i, clipMetaIdx, gotMeta, wantLeaves[i])
		}
		if gotKind := prep.artifacts[clipMetaIdx].Kind; gotKind != finalization.KindMetadata {
			t.Errorf("clipMeta[%d] (artifact[%d]).Kind = %q, want %q (KindMetadata)", i, clipMetaIdx, gotKind, finalization.KindMetadata)
		}
	}
	// godlike/07 fail-closed: 8 unique leaves is the structural
	// invariant for explicit-clips runs. If the test data + the
	// production code both work, we MUST see 8 unique leaves.
	if len(seenLeaves) != clipCount {
		t.Fatalf("expected %d unique PathLeafName values, got %d (collisions: %v) — perClipLeafName must produce distinct leaves per clip",
			clipCount, len(seenLeaves), seenLeaves)
	}
	// Run-level metadata is exactly ONE, with leaf "metadata"
	// (run-root in explicit-clips mode — sits alongside the
	// per-clip video subdirs, NOT inside one of them).
	metaIdx := 2 * clipCount
	if got := prep.artifacts[metaIdx].PathLeafName; got != "metadata" {
		t.Errorf("runMeta PathLeafName = %q, want %q (run-root level in explicit-clips mode)", got, "metadata")
	}
	if got := prep.artifacts[metaIdx].ArtifactID; got != "stock:run-fingerprint-123:metadata" {
		t.Errorf("runMeta ArtifactID = %q, want %q", got, "stock:run-fingerprint-123:metadata")
	}
	// Legacy MUST be untouched: with no `clips[]` the chunks
	// share a single PathLeafName (stockTimestampGroupName). This
	// half of the contract is enforced by
	// TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName
	// (3 sub-cases) — flagged here for cross-reference.
}

// ── PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026) — per-clip PathLeafName ──
//
// The pre-PR bug: StockPublishStep stamped the SAME PathLeafName
// (= stockTimestampGroupName) on every chunk in an explicit-clips
// run, so 8 Pacquiao/Broner clips landed in 1 Drive subdir instead
// of 8 per-clip subdirs. The fix gates perClipLeafName(plan) on
// explicitTimestamps; legacy (no clips[]) stays on the shared
// timestampGroupName. Tests below pin the per-clip contract at
// the unit (perClipLeafName) and integration (StockPublishStep.Run)
// levels.

// TestPerClipLeafName_SlugFromTitle locks the canonical slug
// derivation for explicit-clips runs. The slug convention matches
// pkg/stockparser (SafeFolderName → ToLower → space-to-hyphen) so
// the parser and the publisher produce byte-equivalent slugs for
// the same Title input.
//
// godlike/07 NO-FAKE-AVAILABILITY: the slug is NEVER "untitled"
// (the pathutil.SafeFolderName all-whitespace fallback); such
// inputs fall through to the start-end literal.
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

// TestStockPublishStep_ExplicitClips_PerClipPathLeafName locks the
// integration contract: when explicitTimestamps is true AND the
// Plan has per-clip Title set, each chunk lands in its own Drive
// subdir (slug from Title). The metadata.json sits at the run-root
// "metadata" leaf. This is the user diagnostic's "non sta creando
// la subdir correttamente" regression fix — 8 Pacquiao/Broner clips
// now produce 8 unique leaf folders + 1 run-root metadata subdir.
func TestStockPublishStep_ExplicitClips_PerClipPathLeafName(t *testing.T) {
	tmpDir := t.TempDir()
	const clipCount = 8
	paths := make([]string, clipCount)
	plans := make([]ClipPlan, clipCount)
	expectedLeaves := make([]string, clipCount)
	// Realistic Pacquiao/Broner round titles (subset of the user
	// diagnostic). Each slug should be unique.
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
		expectedLeaves[i] = perClipLeafName(plans[i])
	}
	prep := &recordingArtifactPreparation{}
	runner := &publishFakeRunner{
		runInput: &RunInput{
			FolderName:    "Manny Pacquiao vs Adrien Broner",
			FolderID:      "wf-8clips",
			Clips:         make([]ClipSpec, clipCount), // explicit-clips
			ClipDuration:  25,
			ChunkDuration: 25,
		},
		cfg: OrchestratorConfig{PolicyVersion: "policy-v1"},
		state: &runState{
			Plan:          plans,
			ComposedPaths: paths,
		},
		artifactPrep: prep,
	}
	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	} // Structural invariant: 2N+1 artifacts = N videos + N per-clip
	// metadata + 1 run-level metadata (PR-PER-CLIP-METADATA, July
	// 2026, DoD 5). For 8 clips: 8 + 8 + 1 = 17.
	want := 2*clipCount + 1
	if got := len(prep.artifacts); got != want {
		t.Fatalf("expected %d prepare calls (%d videos + %d per-clip metadata + 1 run-level metadata), got %d", want, clipCount, clipCount, got)
	}
	// Per-clip PathLeafName contract: each chunk's leaf matches
	// perClipLeafName(plan). The pre-PR bug asserted all chunks
	// shared the SAME leaf — this test fails immediately if a
	// future refactor re-introduces that regression. PR-PER-CLIP-METADATA:
	// the 2N+1 contract means artifacts are interleaved
	// [video0, clipMeta0, video1, clipMeta1, ..., runMeta]. The video
	// at index 2*i and its sibling per-clip metadata at index 2*i+1
	// share the same PathLeafName.
	seenLeaves := make(map[string]int)
	for i := 0; i < clipCount; i++ {
		videoIdx := 2 * i
		clipMetaIdx := 2*i + 1
		got := prep.artifacts[videoIdx].PathLeafName
		if got != expectedLeaves[i] {
			t.Errorf("video[%d] (artifact[%d]).PathLeafName = %q, want %q (per-clip leaf from Plan[%d].Title=%q)",
				i, videoIdx, got, expectedLeaves[i], i, plans[i].Title)
		}
		seenLeaves[got]++
		// Per-clip metadata MUST share the same PathLeafName as the
		// sibling video (DoD 5).
		if got := prep.artifacts[clipMetaIdx].PathLeafName; got != expectedLeaves[i] {
			t.Errorf("clipMeta[%d] (artifact[%d]).PathLeafName = %q, want %q (per-clip metadata sidecar MUST live in the same subdir as the sibling video)",
				i, clipMetaIdx, got, expectedLeaves[i])
		}
	}
	// godlike/07 minimum-blast-radius: 8 unique leaves for 8 clips.
	// If two clips happen to slugify to the same leaf (e.g. two
	// "Round 7" titles in the same run), the test still passes
	// the leaf-correctness assertion but flags the duplicate so
	// the operator sees the collision.
	if len(seenLeaves) < clipCount {
		t.Logf("WARNING: %d clips produced %d unique leaves (some slugs collide: %v)",
			clipCount, len(seenLeaves), seenLeaves)
	}
	// Run-level metadata is exactly ONE, with leaf "metadata" (run-root).
	metaIdx := 2 * clipCount
	if got := prep.artifacts[metaIdx].PathLeafName; got != "metadata" {
		t.Errorf("runMeta PathLeafName = %q, want %q (run-root level in explicit-clips mode)", got, "metadata")
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
		state: &runState{
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

	// PR-PER-CLIP-METADATA (July 2026, DoD 5): 2N+1 artifacts =
	// N videos + N per-clip metadata + 1 run-level metadata.
	// For 2 clips: 2 + 2 + 1 = 5 artifacts. The per-clip
	// metadata.json lands in the SAME per-clip subdir as the
	// video (PathLeafName = perClipLeafName(plan)) so each clip
	// has its own metadata sidecar alongside the video. The
	// run-level metadata.json sits at the run-root "metadata"
	// leaf (alongside the per-clip video subdirs).
	if got, want := len(prep.artifacts), 5; got != want {
		t.Fatalf("expected %d prepare calls (2 videos + 2 per-clip metadata + 1 run-level metadata), got %d", want, got)
	}
	// ── Per-clip ordering invariants: [video0, clipMeta0, video1, clipMeta1, runMeta] ──
	// Index 0: video0
	if got := prep.artifacts[0].ArtifactID; got != "stock:run-fingerprint-123:timestamp:0:video" {
		t.Fatalf("unexpected artifact[0] (video0) id: %q", got)
	}
	if got := prep.artifacts[0].Description; got != "Pacquiao fires the first clean left." {
		t.Fatalf("unexpected artifact[0] (video0) description: %q", got)
	}
	// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): per-clip
	// PathLeafName for explicit-clips runs (was the pre-PR bug:
	// every chunk shared "00-00-32_to_00-01-27" so all clips
	// landed in the same folder). New behavior derives per-clip
	// leaf from Plan.Title via the perClipLeafName helper.
	if got := prep.artifacts[0].PathLeafName; got != "round-1" {
		t.Fatalf("unexpected artifact[0] (video0) path leaf: %q, want %q (per-clip leaf from Plan.Title slug)", got, "round-1")
	}
	// Index 1: clipMeta0 (per-clip metadata sidecar inside round-1/ subdir)
	if got := prep.artifacts[1].ArtifactID; got != "stock:run-fingerprint-123:timestamp:0:metadata" {
		t.Fatalf("unexpected artifact[1] (clipMeta0) id: %q, want %q (per-clip metadata ArtifactID mirrors video format with :metadata suffix)", got, "stock:run-fingerprint-123:timestamp:0:metadata")
	}
	if got := prep.artifacts[1].Kind; got != finalization.KindMetadata {
		t.Fatalf("unexpected artifact[1] (clipMeta0) kind: %q, want %q (KindMetadata)", got, finalization.KindMetadata)
	}
	if got := prep.artifacts[1].Filename; got != "metadata.json" {
		t.Fatalf("unexpected artifact[1] (clipMeta0) filename: %q, want %q", got, "metadata.json")
	}
	// Per-clip metadata.json lands in the SAME per-clip subdir
	// as the sibling video (round-1/), not at the run-root
	// "metadata" leaf. The 2N+1 contract requires per-clip
	// PathLeafName = perClipLeafName(plan) for the metadata
	// sidecar.
	if got := prep.artifacts[1].PathLeafName; got != "round-1" {
		t.Fatalf("unexpected artifact[1] (clipMeta0) path leaf: %q, want %q (per-clip metadata sidecar MUST live in the same subdir as the video)", got, "round-1")
	}
	// Index 2: video1
	if got := prep.artifacts[2].ArtifactID; got != "stock:run-fingerprint-123:timestamp:1:video" {
		t.Fatalf("unexpected artifact[2] (video1) id: %q", got)
	}
	if got := prep.artifacts[2].Description; got != "Broner tries to reset and circle out." {
		t.Fatalf("unexpected artifact[2] (video1) description: %q", got)
	}
	if got := prep.artifacts[2].PathLeafName; got != "round-2" {
		t.Fatalf("unexpected artifact[2] (video1) path leaf: %q, want %q (per-clip leaf from Plan.Title slug)", got, "round-2")
	}
	// Index 3: clipMeta1 (per-clip metadata sidecar inside round-2/ subdir)
	if got := prep.artifacts[3].ArtifactID; got != "stock:run-fingerprint-123:timestamp:1:metadata" {
		t.Fatalf("unexpected artifact[3] (clipMeta1) id: %q, want %q (per-clip metadata ArtifactID mirrors video format with :metadata suffix)", got, "stock:run-fingerprint-123:timestamp:1:metadata")
	}
	if got := prep.artifacts[3].Kind; got != finalization.KindMetadata {
		t.Fatalf("unexpected artifact[3] (clipMeta1) kind: %q, want %q (KindMetadata)", got, finalization.KindMetadata)
	}
	if got := prep.artifacts[3].PathLeafName; got != "round-2" {
		t.Fatalf("unexpected artifact[3] (clipMeta1) path leaf: %q, want %q (per-clip metadata sidecar MUST live in the same subdir as the video)", got, "round-2")
	}
	// Index 4: runMeta (run-level metadata.json at the run-root "metadata" leaf)
	if got := prep.artifacts[4].ArtifactID; got != "stock:run-fingerprint-123:metadata" {
		t.Fatalf("unexpected artifact[4] (runMeta) id: %q", got)
	}
	// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): run-level
	// metadata.json sits at the run-root "metadata" leaf in
	// explicit-clips mode (alongside the per-clip video
	// subdirs). Legacy stays on the shared timestampGroupName
	// (see TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName).
	if got := prep.artifacts[4].PathLeafName; got != "metadata" {
		t.Fatalf("unexpected artifact[4] (runMeta) path leaf: %q, want %q (run-root level in explicit-clips mode)", got, "metadata")
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
		state: &runState{
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
				state: &runState{
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
func publishedDrivePathFor(artifactID string) string {
	return "https://drive.google.com/file/d/" + artifactID + "/view"
}

func makePR004LegacyRunner(t *testing.T, runInput *RunInput, policyVersion string) *publishFakeRunner {
	t.Helper()
	tmpDir := t.TempDir()
	chunk := filepath.Join(tmpDir, "chunk.mp4")
	if err := os.WriteFile(chunk, []byte("chunk"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	prep := &recordingArtifactPreparation{}
	return &publishFakeRunner{
		runInput: runInput,
		cfg:      OrchestratorConfig{PolicyVersion: policyVersion},
		state: &runState{
			Plan:          []ClipPlan{{SourceID: "https://youtu.be/abc", StartSec: 10, EndSec: 20}},
			ComposedPaths: []string{chunk},
		},
		artifactPrep: prep,
	}
}

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
		state: &runState{
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
		state: &runState{
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

	// Structural invariant: 2N+1 artifacts = 2 videos + 2 per-clip
	// metadata + 1 run-level metadata.
	if got, want := len(prep.artifacts), 5; got != want {
		t.Fatalf("expected %d prepare calls (2 videos + 2 per-clip metadata + 1 run-level metadata), got %d", want, got)
	}
	// ── PR-PLAN-DESCRIPTION-SYNC contract ──
	// Artifact 0 = video0 (with Description from in.Clips[0].Description).
	// The recordingArtifactPreparation mock captures the
	// VerifiedArtifact.Description verbatim — the per-clip metadata
	// emit AND the run-level metadata.json both use the SAME
	// cs.Description (canonical StockRunMetadata chunks[] entry),
	// so asserting on the video artifact's Description proves the
	// metadata.json carries the same content (godlike/06 SSOT
	// one-canonical-owner-per-fact: ChunkState.Description is the
	// SOLE source of metadata chunks[].description).
	if got := prep.artifacts[0].Description; got != want {
		t.Errorf("artifact[0] (video0) Description = %q, want %q (Plan.Description was empty, must fall through to in.Clips[0].Description)",
			got, want)
	}
	// Artifact 2 = video1 (with NO description — chunk 1 had no
	// in.Clips[1].Description and Plan[1].Description is empty).
	// The fix must NOT fabricate a description here (godlike/07
	// NO-FAKE-AVAILABILITY: no "n/a" / "unknown" placeholder).
	if got := prep.artifacts[2].Description; got != "" {
		t.Errorf("artifact[2] (video1) Description = %q, want %q (no canonical source on either side; must NOT fabricate)",
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
		state: &runState{
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
	// 2N+1 = 1 video + 1 per-clip metadata + 1 run-level metadata = 3.
	if got, want := len(prep.artifacts), 3; got != want {
		t.Fatalf("expected %d prepare calls, got %d", want, got)
	}
	if got := prep.artifacts[0].Description; got != planDesc {
		t.Errorf("artifact[0] (video0) Description = %q, want %q (Plan.Description must win over in.Clips[i].Description)",
			got, planDesc)
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
		state: &runState{
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
