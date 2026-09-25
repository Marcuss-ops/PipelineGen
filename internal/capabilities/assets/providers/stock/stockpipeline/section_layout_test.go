// Package stockpipeline — section_layout_test.go.
//
// The sections_only download window is DERIVED from the plan (never stored) and
// consumed twice: stage_sources turns it into the yt-dlp --download-sections
// value, extract_clips turns it into the local seek offset. A drift between the
// two silently publishes the wrong seconds, so the derivation, the yt-dlp string
// and the re-anchoring pass are pinned here.
//
// Production homes of the code under test (this package is a frozen hotspot, so
// no new production file was added): the window derivation and yt-dlp range in
// downloader_port.go, the re-anchoring pass in step_plan_clips.go, the consumer
// helpers in naming.go and step_extract_clips_cut.go.
//
// The failure this file exists to prevent: before it, a 15-source stock run
// downloaded all 15 interviews in full (a whole-source yt-dlp invocation) to
// publish 8 × 5s per source — gigabytes of egress, and a killed yt-dlp process
// on the longest sources.
package stockpipeline

import (
	"testing"
)

func TestIsSectionedRun(t *testing.T) {
	tests := []struct {
		name string
		in   *RunInput
		want bool
	}{
		{name: "nil input", in: nil, want: false},
		{name: "no download mode", in: &RunInput{}, want: false},
		{name: "other download mode", in: &RunInput{DownloadMode: "whole_source"}, want: false},
		{name: "sections_only", in: &RunInput{DownloadMode: DownloadModeSectionsOnly}, want: true},
		{
			// Explicit clips are operator-authored absolute windows: they keep
			// the whole-source stage so the operator gets exactly what was asked.
			name: "sections_only with explicit clips",
			in:   &RunInput{DownloadMode: DownloadModeSectionsOnly, Clips: []ClipSpec{{StartSec: 1, EndSec: 2}}},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSectionedRun(tc.in); got != tc.want {
				t.Fatalf("isSectionedRun(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSectionWindowForPlans_ContiguousGroupIsOneSection(t *testing.T) {
	plans := []ClipPlan{
		{SourceID: "src", StartSec: 512, EndSec: 517},
		{SourceID: "src", StartSec: 517, EndSec: 522},
		{SourceID: "src", StartSec: 522, EndSec: 527},
	}

	start, end, ok := sectionWindowForPlans(plans)
	if !ok {
		t.Fatal("contiguous group must yield a section")
	}
	if start != 512 || end != 527 {
		t.Fatalf("window = [%v,%v], want [512,527]", start, end)
	}
}

func TestSectionWindowForPlans_SingleClip(t *testing.T) {
	start, end, ok := sectionWindowForPlans([]ClipPlan{{StartSec: 7.5, EndSec: 12.5}})
	if !ok || start != 7.5 || end != 12.5 {
		t.Fatalf("single clip window = [%v,%v] ok=%v, want [7.5,12.5] true", start, end, ok)
	}
}

// TestSectionWindowForPlans_RejectsNonContiguousGroups pins the fail-safe: every
// case that is not exactly one contiguous slice must fall back to staging the
// whole source rather than guessing a window (a guessed window truncates a source
// the plan never asked to truncate).
func TestSectionWindowForPlans_RejectsNonContiguousGroups(t *testing.T) {
	tests := []struct {
		name  string
		plans []ClipPlan
	}{
		{name: "empty", plans: nil},
		{
			name: "gap between clips",
			plans: []ClipPlan{
				{StartSec: 0, EndSec: 5},
				{StartSec: 100, EndSec: 105},
			},
		},
		{
			name: "overlap",
			plans: []ClipPlan{
				{StartSec: 0, EndSec: 10},
				{StartSec: 5, EndSec: 15},
			},
		},
		{name: "inverted window", plans: []ClipPlan{{StartSec: 10, EndSec: 10}}},
		{name: "negative start", plans: []ClipPlan{{StartSec: -1, EndSec: 4}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if start, end, ok := sectionWindowForPlans(tc.plans); ok {
				t.Fatalf("window = [%v,%v] ok=true, want ok=false (whole-source fallback)", start, end)
			}
		})
	}
}

func TestSectionDownloadString_UsesYtDlpAbsoluteRange(t *testing.T) {
	tests := []struct {
		name       string
		start, end float64
		want       string
	}{
		{name: "whole minutes", start: 32, end: 42, want: "*00:00:32.000-00:00:42.000"},
		{name: "sub-second", start: 1.5, end: 6.25, want: "*00:00:01.500-00:00:06.250"},
		{name: "hours", start: 3661.125, end: 3701.125, want: "*01:01:01.125-01:01:41.125"},
		{
			// 59.9996s must carry into the minute, never render as 00:00:60.000.
			name: "millisecond carry", start: 59.9996, end: 64.9996, want: "*00:01:00.000-00:01:05.000",
		},
		{name: "negative clamps to zero", start: -3, end: 2, want: "*00:00:00.000-00:00:02.000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sectionDownloadString(tc.start, tc.end); got != tc.want {
				t.Fatalf("sectionDownloadString(%v,%v) = %q, want %q", tc.start, tc.end, got, tc.want)
			}
		})
	}
}

// TestSectionAnchorSec_IsDeterministicAndSpread pins the two properties the
// source cache depends on: the same source always lands on the same block (no
// rng, no clock → a re-run hits the cache instead of re-downloading), and the
// block is drawn from inside [lo, hi] rather than pinned to one end.
func TestSectionAnchorSec_IsDeterministicAndSpread(t *testing.T) {
	const lo, hi = 100, 400

	if a, b := sectionAnchorSec("source-a", "v1", lo, hi), sectionAnchorSec("source-a", "v1", lo, hi); a != b {
		t.Fatalf("anchor is not deterministic: %d != %d", a, b)
	}
	if a, b := sectionAnchorSec("source-a", "v1", lo, hi), sectionAnchorSec("source-a", "v2", lo, hi); a == b {
		t.Logf("policy-version salt produced the same anchor by chance (%d); not fatal", a)
	}

	distinct := make(map[int]struct{})
	for i := 0; i < 32; i++ {
		anchor := sectionAnchorSec("source-"+string(rune('a'+i)), "test-policy-v1", lo, hi)
		if anchor < lo || anchor > hi {
			t.Fatalf("anchor %d outside [%d,%d]", anchor, lo, hi)
		}
		distinct[anchor] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Fatalf("32 sources collapsed to %d anchor(s): the block is not spread", len(distinct))
	}

	// Degenerate ranges collapse instead of panicking on a zero span.
	if got := sectionAnchorSec("source-a", "v1", 7, 7); got != 7 {
		t.Fatalf("degenerate anchor = %d, want 7", got)
	}
	if got := sectionAnchorSec("source-a", "v1", 9, 3); got != 9 {
		t.Fatalf("inverted-range anchor = %d, want lo=9", got)
	}
}

// TestCompactPlansIntoSections_OneContiguousBlockPerSource pins the layout the
// whole change exists for: each source's clips become ONE block of adjacent
// windows, so one 40-second download replaces one whole interview. Clip count,
// per-clip duration and total published duration are untouched (the duration
// contract stays valid); only the offsets — and therefore the window-derived
// OutputLogicalIDs — move.
func TestCompactPlansIntoSections_OneContiguousBlockPerSource(t *testing.T) {
	const (
		sourceA = "https://www.youtube.com/watch?v=aaaaaaaaaaa"
		sourceB = "https://www.youtube.com/watch?v=bbbbbbbbbbb"
		durSec  = 600.0
	)
	plans := make([]ClipPlan, 0, 16)
	for _, source := range []string{sourceA, sourceB} {
		for i := 0; i < 8; i++ {
			plans = append(plans, ClipPlan{
				SourceID:        source,
				StartSec:        float64(i * 70), // the planner's spread, pre-compaction
				EndSec:          float64(i*70 + 5),
				OutputLogicalID: mintOutputLogicalID(source, i, "test-policy-v1", float64(i*70), float64(i*70+5)),
				PolicyVersion:   "test-policy-v1",
			})
		}
	}

	got := compactPlansIntoSections(plans, []VideoSource{
		{URL: sourceA, DurationSec: durSec},
		{URL: sourceB, DurationSec: durSec},
	}, "test-policy-v1")

	if len(got) != len(plans) {
		t.Fatalf("plan count changed: %d → %d", len(plans), len(got))
	}
	horizon := int(durSec) - sourceDurationHorizonMarginSec

	for _, source := range []string{sourceA, sourceB} {
		var group []ClipPlan
		for _, plan := range got {
			if plan.SourceID == source {
				group = append(group, plan)
			}
		}
		if len(group) != 8 {
			t.Fatalf("source %s has %d clips, want 8", source, len(group))
		}

		start, end, ok := sectionWindowForPlans(group)
		if !ok {
			t.Fatalf("source %s is not contiguous after compaction: %+v", source, group)
		}
		if end-start != 40 {
			t.Fatalf("source %s section = %.3fs, want 40s (8 × 5s)", source, end-start)
		}
		if start < 0 || int(end) > horizon {
			t.Fatalf("source %s section [%v,%v] outside [0,%d]", source, start, end, horizon)
		}
		for i, plan := range group {
			if plan.EndSec-plan.StartSec != 5 {
				t.Fatalf("source %s clip[%d] duration = %v, want 5", source, i, plan.EndSec-plan.StartSec)
			}
			if plan.StartSec != start+float64(i)*5 || plan.EndSec != end-float64(8-1-i)*5 {
				t.Fatalf("source %s clip[%d] = [%v,%v], want adjacent windows from %v", source, i, plan.StartSec, plan.EndSec, start)
			}
			// The ID hashes the window: a stale ID would make the asset claim a
			// timestamp range it no longer describes.
			want := mintOutputLogicalID(source, i, "test-policy-v1", plan.StartSec, plan.EndSec)
			if plan.OutputLogicalID != want {
				t.Fatalf("source %s clip[%d] ID = %q, want the re-minted %q", source, i, plan.OutputLogicalID, want)
			}
		}
	}
}

// TestCompactPlansIntoSections_LeavesUnknownDurationAlone pins the conservative
// path: without a real source length there is no safe anchor, so the plan is left
// as the planner produced it. The group is then non-contiguous, which makes
// staging fall back to the whole source — the pre-existing behaviour, not a
// truncation on a guess.
func TestCompactPlansIntoSections_LeavesUnknownDurationAlone(t *testing.T) {
	plans := []ClipPlan{
		{SourceID: "unknown-duration", StartSec: 0, EndSec: 5},
		{SourceID: "unknown-duration", StartSec: 400, EndSec: 405},
	}
	original := append([]ClipPlan(nil), plans...)

	got := compactPlansIntoSections(plans, []VideoSource{{URL: "unknown-duration"}}, "v1")

	for i := range got {
		if got[i].StartSec != original[i].StartSec || got[i].EndSec != original[i].EndSec {
			t.Fatalf("clip[%d] moved from [%v,%v] to [%v,%v] without a known duration",
				i, original[i].StartSec, original[i].EndSec, got[i].StartSec, got[i].EndSec)
		}
	}
	if _, _, ok := sectionWindowForPlans(got); ok {
		t.Fatal("scattered plan must not be treated as one section")
	}
}

// TestCompactPlansIntoSections_LeavesUnfittablePlansAlone covers a source shorter
// than the requested block: compaction must not move clips past the end of the
// source (that is what the extract-time fail-closed bounds check reports), so the
// plan is left untouched.
func TestCompactPlansIntoSections_LeavesUnfittablePlansAlone(t *testing.T) {
	plans := []ClipPlan{
		{SourceID: "short", StartSec: 0, EndSec: 5},
		{SourceID: "short", StartSec: 5, EndSec: 10},
		{SourceID: "short", StartSec: 10, EndSec: 15},
	}
	got := compactPlansIntoSections(plans, []VideoSource{{URL: "short", DurationSec: 12}}, "v1")
	for i := range got {
		if got[i].StartSec != plans[i].StartSec || got[i].EndSec != plans[i].EndSec {
			t.Fatalf("clip[%d] moved to [%v,%v] although the block does not fit the source", i, got[i].StartSec, got[i].EndSec)
		}
	}
}

func TestAssignSectionStageKeys_SharesOneKeyPerSource(t *testing.T) {
	plans := []ClipPlan{
		{SourceID: "src-a", StartSec: 100, EndSec: 105, OutputLogicalID: "planner:a:0"},
		{SourceID: "src-a", StartSec: 105, EndSec: 110, OutputLogicalID: "planner:a:1"},
		{SourceID: "src-b", StartSec: 0, EndSec: 5, OutputLogicalID: "planner:b:0"},
		{SourceID: "src-b", StartSec: 300, EndSec: 305, OutputLogicalID: "planner:b:1"},
	}

	assignSectionStageKeys(plans)

	wantA := sectionStageKey("src-a", 100, 110)
	if plans[0].StageKey != wantA || plans[1].StageKey != wantA {
		t.Fatalf("contiguous group keys = %q,%q, want both %q", plans[0].StageKey, plans[1].StageKey, wantA)
	}
	// A scattered group cannot be one section, so it keeps the per-clip key.
	if plans[2].StageKey != "planner:b:0" || plans[3].StageKey != "planner:b:1" {
		t.Fatalf("scattered group keys = %q,%q, want the per-clip OutputLogicalIDs", plans[2].StageKey, plans[3].StageKey)
	}
}

func TestSectionCutOffsetSec(t *testing.T) {
	contiguous := []ClipPlan{
		{StartSec: 512, EndSec: 517},
		{StartSec: 517, EndSec: 522},
	}
	scattered := []ClipPlan{
		{StartSec: 512, EndSec: 517},
		{StartSec: 900, EndSec: 905},
	}

	if got := sectionCutOffsetSec(&RunInput{DownloadMode: DownloadModeSectionsOnly}, contiguous); got != 512 {
		t.Fatalf("offset = %v, want the section start 512", got)
	}
	if got := sectionCutOffsetSec(&RunInput{DownloadMode: DownloadModeSectionsOnly}, scattered); got != 0 {
		t.Fatalf("offset = %v, want 0 for a whole-source group", got)
	}
	if got := sectionCutOffsetSec(&RunInput{DownloadMode: DownloadModeSectionsOnly, Clips: []ClipSpec{{StartSec: 0, EndSec: 5}}}, contiguous); got != 0 {
		t.Fatalf("offset = %v, want 0 for an explicit-clip run", got)
	}
	if got := sectionCutOffsetSec(&RunInput{}, contiguous); got != 0 {
		t.Fatalf("offset = %v, want 0 for a non-sectioned run", got)
	}
}

func TestSectionDownloadForPlans(t *testing.T) {
	plans := []ClipPlan{
		{StartSec: 32, EndSec: 37},
		{StartSec: 37, EndSec: 42},
	}

	if got := sectionDownloadForPlans(&RunInput{DownloadMode: DownloadModeSectionsOnly}, plans); got != "*00:00:32.000-00:00:42.000" {
		t.Fatalf("section = %q, want *00:00:32.000-00:00:42.000", got)
	}
	if got := sectionDownloadForPlans(&RunInput{}, plans); got != "" {
		t.Fatalf("section = %q, want empty for a non-sectioned run", got)
	}
	if got := sectionDownloadForPlans(&RunInput{DownloadMode: DownloadModeSectionsOnly}, nil); got != "" {
		t.Fatalf("section = %q, want empty for an empty group", got)
	}
}
