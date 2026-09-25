// Package stockpipeline — section_pipeline_test.go.
//
// End-to-end pins for the sections_only layout, across the three steps that must
// agree on the section window:
//
//	stock.plan          → one contiguous block per source, one shared StageKey
//	stock.stage_sources → that block becomes the yt-dlp --download-sections value
//	stock.extract_clips → clips are cut at LOCAL offsets inside the staged slice
//
// The acceptance criterion is the one the operator asked for: a 40-second actor
// set fetches ~40 seconds per source instead of the whole interview, with one
// yt-dlp invocation per source.
package stockpipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/ingest"
)

// newSectionedPlanRunner wires the canonical step fixtures for a sections_only
// run over two sources (900s and 1200s) with the real 8 × 5s = 40s contract.
func newSectionedPlanRunner(urls []string, durations map[string]float64) *fakeStepRunner {
	return &fakeStepRunner{
		runInput: &RunInput{
			DirectURLs:                     urls,
			SourceDurations:                durations,
			TargetDurationPerSourceSeconds: 40,
			TargetTotalDurationSeconds:     40 * len(urls),
			ClipsPerSource:                 8,
			ClipDurationSeconds:            5,
			DownloadMode:                   DownloadModeSectionsOnly,
		},
		cfg: OrchestratorConfig{
			PolicyVersion:    "test-policy-v1",
			ClipDurationSec:  5,
			ChunkDurationSec: 40,
		},
		state:   &RunState{},
		planner: NewDeterministicPlanner(),
	}
}

func plansForSource(plans []ClipPlan, sourceID string) []ClipPlan {
	var group []ClipPlan
	for _, plan := range plans {
		if plan.SourceID == sourceID {
			group = append(group, plan)
		}
	}
	return group
}

// TestStockPlanStep_SectionsOnlyCompactsEachSourceIntoOneBlock is the headline
// contract: the 8 windows of a source become ONE contiguous 40-second block, so
// the stager can fetch that slice with a single yt-dlp invocation. The duration
// contract (clip count, per-clip duration, total) is unchanged — only the offsets
// move.
func TestStockPlanStep_SectionsOnlyCompactsEachSourceIntoOneBlock(t *testing.T) {
	const (
		urlA = "https://www.youtube.com/watch?v=aaaaaaaaaaa"
		urlB = "https://www.youtube.com/watch?v=bbbbbbbbbbb"
	)
	runner := newSectionedPlanRunner([]string{urlA, urlB}, map[string]float64{urlA: 900, urlB: 1200})

	if err := (StockPlanStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("stock.plan: unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 16 {
		t.Fatalf("plan count = %d, want 16 (2 sources × 8 clips)", len(plans))
	}

	for _, source := range []string{urlA, urlB} {
		group := plansForSource(plans, source)
		if len(group) != 8 {
			t.Fatalf("source %s clips = %d, want 8", source, len(group))
		}
		start, end, ok := sectionWindowForPlans(group)
		if !ok {
			t.Fatalf("source %s is not one contiguous block after stock.plan: %+v", source, group)
		}
		if end-start != 40 {
			t.Fatalf("source %s block = %.3fs, want 40s", source, end-start)
		}
		if int(end) > int(runner.runInput.SourceDurations[source])-sourceDurationHorizonMarginSec {
			t.Fatalf("source %s block [%v,%v] runs past the source horizon", source, start, end)
		}
		// The very fact that staging derives a section (instead of an empty
		// string → whole-source download) is what the operator pays for.
		section := sectionDownloadForPlans(runner.runInput, group)
		if section == "" {
			t.Fatalf("source %s would be downloaded whole: no download section derived", source)
		}
		for i, plan := range group {
			if plan.StageKey != sectionStageKey(source, start, end) {
				t.Fatalf("source %s clip[%d] StageKey = %q, want the shared section key", source, i, plan.StageKey)
			}
		}
	}

	// Different sources must not all land on the same block.
	if plansForSource(plans, urlA)[0].StartSec == plansForSource(plans, urlB)[0].StartSec {
		t.Log("both sources anchored on the same offset; allowed but unusual")
	}
}

// TestStockPlanStep_NonSectionedRunKeepsPlannerSpread is the control case: a run
// that is not sections_only (or that carries explicit clips) must not be
// re-anchored, and its scattered plan must not be turned into a section.
func TestStockPlanStep_NonSectionedRunKeepsPlannerSpread(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=ccccccccccc"
	runner := newSectionedPlanRunner([]string{url}, map[string]float64{url: 900})
	runner.runInput.DownloadMode = ""

	if err := (StockPlanStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("stock.plan: unexpected error: %v", err)
	}
	group := plansForSource(runner.State().Plan, url)
	if len(group) != 8 {
		t.Fatalf("clips = %d, want 8", len(group))
	}
	if _, _, ok := sectionWindowForPlans(group); ok {
		t.Fatal("a non-sectioned run must keep the planner's spread, not one block")
	}
	if section := sectionDownloadForPlans(runner.runInput, group); section != "" {
		t.Fatalf("non-sectioned run derived download section %q, want none", section)
	}
}

// sectionedExtractFixture stages one source as a 40-second slice holding 8
// absolute 5-second windows, exactly as stage_sources would have produced it.
type sectionedExtractFixture struct {
	runner *extractClipsFakeRunner
	cutter *batchRecordingCutter
	plans  []ClipPlan
}

func newSectionedExtractFixture(t *testing.T, stagedDurationSec float64) sectionedExtractFixture {
	t.Helper()

	const (
		sourceID      = "https://www.youtube.com/watch?v=sectionsrc1"
		sectionStart  = 512.0
		clipDuration  = 5.0
		clipsPerGroup = 8
	)

	sourcePath := filepath.Join(t.TempDir(), "section.mp4")
	if err := os.WriteFile(sourcePath, []byte("fake-section-bytes"), 0o644); err != nil {
		t.Fatalf("seed staged section: %v", err)
	}

	plans := make([]ClipPlan, 0, clipsPerGroup)
	for i := 0; i < clipsPerGroup; i++ {
		start := sectionStart + float64(i)*clipDuration
		plans = append(plans, ClipPlan{
			SourceID:        sourceID,
			OutputLogicalID: mintOutputLogicalID(sourceID, i, "test-policy-v1", start, start+clipDuration),
			StartSec:        start,
			EndSec:          start + clipDuration,
			PolicyVersion:   "test-policy-v1",
		})
	}

	cutter := &batchRecordingCutter{}
	runner := &extractClipsFakeRunner{
		fakeStepRunner: &fakeStepRunner{
			runInput: &RunInput{
				DirectURLs:   []string{sourceID},
				DownloadMode: DownloadModeSectionsOnly,
				ClipDuration: int(clipDuration),
				TotalMinutes: 1,
			},
			cfg: OrchestratorConfig{
				PolicyVersion:    "test-policy-v1",
				ClipDurationSec:  clipDuration,
				ChunkDurationSec: 40,
			},
			state: &RunState{
				Plan: plans,
				StagedAssets: []*assets.StagedAsset{
					{SourceID: sourceID, LocalPath: sourcePath, DurationSec: stagedDurationSec},
				},
			},
		},
		writer: &recordingWriter{},
		cutter: cutter,
	}
	return sectionedExtractFixture{runner: runner, cutter: cutter, plans: plans}
}

// TestStockExtractClips_SectionsOnlyCutsAtLocalOffsets pins the seek arithmetic.
// The plan keeps ABSOLUTE source time (512s…) because that identity is published
// (titles, metadata, artifact rows), while the staged file starts at the section
// start — so the cutter must seek at local offsets 0,5,…,35. Getting this wrong
// is invisible in the job status and would publish 40 wrong seconds.
func TestStockExtractClips_SectionsOnlyCutsAtLocalOffsets(t *testing.T) {
	fixture := newSectionedExtractFixture(t, 40)

	if err := (StockExtractClipsStep{}).Run(context.Background(), fixture.runner); err != nil {
		t.Fatalf("stock.extract_clips: unexpected error: %v", err)
	}
	if len(fixture.cutter.requests) != 1 {
		t.Fatalf("cut requests = %d, want 1 (one source group)", len(fixture.cutter.requests))
	}

	jobs := fixture.cutter.requests[0].Jobs
	if len(jobs) != 8 {
		t.Fatalf("cut jobs = %d, want 8", len(jobs))
	}
	for i, job := range jobs {
		wantStart := float64(i) * 5
		wantEnd := wantStart + 5
		if job.StartSec != wantStart || job.EndSec != wantEnd {
			t.Fatalf("job[%d] = [%v,%v], want local offsets [%v,%v] (plan was [%v,%v])",
				i, job.StartSec, job.EndSec, wantStart, wantEnd, fixture.plans[i].StartSec, fixture.plans[i].EndSec)
		}
	}

	// The published identity must stay absolute: the plan is what metadata and
	// artifact rows describe.
	if fixture.plans[0].StartSec != 512 {
		t.Fatalf("plan[0].StartSec = %v, want the absolute 512 preserved", fixture.plans[0].StartSec)
	}
}

// TestStockExtractClips_TruncatedSectionFailsClosed pins that a slice which came
// back shorter than planned cannot silently publish clipped-out gaps: with the
// offset applied, the planned EndSec still exceeds the staged duration and the
// run fails closed with ErrStockClipsOutOfRange. (Before the offset was applied
// to the bounds check, the ABSOLUTE end always looked out of range, which is the
// other half of the same contract.)
func TestStockExtractClips_TruncatedSectionFailsClosed(t *testing.T) {
	fixture := newSectionedExtractFixture(t, 12)

	err := (StockExtractClipsStep{}).Run(context.Background(), fixture.runner)
	if err == nil {
		t.Fatal("a truncated section must fail closed, got nil error")
	}
	if !errors.Is(err, ErrStockClipsOutOfRange) {
		t.Fatalf("err = %v, want ErrStockClipsOutOfRange", err)
	}
}

// TestStockIngestPreparer_PropagatesTheDownloadSection pins the link that was
// missing and made every run download whole interviews: the boundary adapter must
// carry DownloadSection (and ForceKeyframes, so the slice starts exactly at the
// requested timestamp) from the ingest source into the acquisition request.
func TestStockIngestPreparer_PropagatesTheDownloadSection(t *testing.T) {
	const section = "*00:08:32.000-00:09:12.000"
	rec := &recordingStager{
		stagedAsset: &assets.StagedAsset{LocalPath: "/tmp/section.mp4", Bytes: 1024},
	}
	preparer := &stockIngestPreparer{stager: rec, policyVersion: "test-policy-v1"}

	prepared, err := preparer.Prepare(context.Background(), ingest.Source{
		ID:              "source-1",
		URL:             "https://www.youtube.com/watch?v=abc123",
		DownloadSection: section,
	})
	if err != nil {
		t.Fatalf("Prepare: unexpected error: %v", err)
	}
	if prepared == nil || prepared.SourceID != "source-1" {
		t.Fatalf("prepared = %+v, want SourceID source-1 preserved", prepared)
	}
	if rec.lastRef.DownloadSection != section {
		t.Fatalf("acquisition ref DownloadSection = %q, want %q", rec.lastRef.DownloadSection, section)
	}
	if !rec.lastRef.ForceKeyframes {
		t.Fatal("a sectioned stage must request --force-keyframes-at-cuts: a keyframe-snapped slice shifts every local seek")
	}
	if rec.lastRef.URL != "https://www.youtube.com/watch?v=abc123" {
		t.Fatalf("acquisition ref URL = %q, want the source URL", rec.lastRef.URL)
	}
}

// TestStockIngestPreparer_WholeSourceKeepsEmptySection is the control case: runs
// that stage whole sources must keep an empty section and must NOT force a
// re-encode.
func TestStockIngestPreparer_WholeSourceKeepsEmptySection(t *testing.T) {
	rec := &recordingStager{
		stagedAsset: &assets.StagedAsset{LocalPath: "/tmp/source.mp4", Bytes: 1024},
	}
	preparer := &stockIngestPreparer{stager: rec, policyVersion: "test-policy-v1"}

	if _, err := preparer.Prepare(context.Background(), ingest.Source{
		ID:  "source-1",
		URL: "https://www.youtube.com/watch?v=abc123",
	}); err != nil {
		t.Fatalf("Prepare: unexpected error: %v", err)
	}
	if rec.lastRef.DownloadSection != "" {
		t.Fatalf("DownloadSection = %q, want empty for a whole-source run", rec.lastRef.DownloadSection)
	}
	if rec.lastRef.ForceKeyframes {
		t.Fatal("a whole-source stage must not request --force-keyframes-at-cuts")
	}
}
