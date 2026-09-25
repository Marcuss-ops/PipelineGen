// Package stockpipeline — downloader_port.go (PR-REFACTOR-P0-IO-BINDER, July 2026).
//
// SourceDownloader is the Pattern 0 typed port for source video
// downloading. StockStager routes source downloads through this
// port so the application layer never imports infrastructure types
// (godlike/07). The concrete implementation lives in
// internal/platform/downloader/stock_adapter.go and is
// injected via WithDownloader at composition time.
//
// godlike/06 SSOT:
//   - This file is the SOLE owner of the SourceDownloader interface
//     and the SourceDownloadRequest / DownloadedSource DTOs.
//   - Tests inject fakes via WithDownloader(fake).
//
// godlike/07 fail-closed:
//   - When StockStager.s.downloader is nil and a download path is
//     required, StageSource surfaces a typed error.
//
// ─── sections_only download window (September 2026) ──────────────────────────
//
// This file is also the SOLE owner of "how many seconds of a source must we
// fetch, and how is that expressed to yt-dlp?" — the question the
// DownloadSections field above raises. The window is DERIVED from the plan,
// never stored, and consumed twice:
//
//	stock.stage_sources  → sectionDownloadForPlans  → --download-sections
//	stock.extract_clips  → sectionCutOffsetSec      → local seek offset
//
// so the two can never disagree. A section that is not exactly
// [min(StartSec), max(EndSec)] of a source's contiguous plan group would
// silently shift every cut, which is invisible in the job status and would
// publish the wrong seconds.
//
// Why this matters: a sections_only run publishes clips_per_source ×
// clip_duration_seconds per source (8 × 5s = 40s) out of a source that can be an
// hour long. Before the window was wired, stockIngestPreparer.Prepare dropped
// DownloadSection, so yt-dlp ran with an empty --download-sections and every run
// pulled whole interviews — gigabytes of egress for 40 published seconds, and a
// killed yt-dlp process on the longest sources.
//
// The plan side of the layout (re-anchoring each source's clips into ONE
// contiguous block, deterministically) lives in step_plan_clips.go:
// compactPlansIntoSections / sectionAnchorSec / assignSectionStageKeys. The
// consumer-side helpers live next to their callers (sectionDownloadForPlans in
// naming.go with the ingest bridge, sectionCutOffsetSec in
// step_extract_clips_cut.go with the seek it feeds).
package stockpipeline

import (
	"context"
	"fmt"
	"math"
)

const (
	// DownloadModeSectionsOnly is the canonical download_mode value. Only this
	// mode downloads a time slice instead of the whole source, so every layout
	// and seek decision is gated on it.
	DownloadModeSectionsOnly = "sections_only"

	// sectionInteriorMarginRatio keeps a section out of the first/last quarter
	// of a source. Interviews open with channel branding and close with
	// credits/outros; a uniform [0, duration] anchor would land there for a
	// predictable fraction of sources. The published clips are the whole point
	// of the run, so the block is drawn from the interior half instead.
	sectionInteriorMarginRatio = 0.25

	// sectionBoundaryEpsilonSec absorbs float rounding when checking that two
	// consecutive clip windows share a boundary (both sides are computed as
	// anchor + i*clipDur, so they are equal up to representation error).
	sectionBoundaryEpsilonSec = 1e-6
)

// SourceDownloadRequest is the application-layer DTO for configuring
// a source video download. It mirrors the fields StockStager needs
// without importing infrastructure types.
type SourceDownloadRequest struct {
	URL              string
	OutputPath       string
	DownloadSections []string
	ForceKeyframes   bool
	MergeFormat      string
	NoPlaylist       bool
	UseCookies       bool
}

// DownloadedSource is the application-layer DTO returned by a
// successful download. The ResolvedPath is the actual file path
// after the infrastructure adapter resolves yt-dlp's %(ext)s
// template internally.
type DownloadedSource struct {
	ResolvedPath string
	SizeBytes    int64
}

// SourceDownloader is the Pattern 0 typed port for source video
// downloading. The single method mirrors the canonical download
// contract without exposing infrastructure types.
//
// The infrastructure adapter (stock_adapter.go) translates
// SourceDownloadRequest → downloader.DownloadRequest, calls
// yt-dlp, resolves the output path, and returns DownloadedSource.
type SourceDownloader interface {
	Download(ctx context.Context, req *SourceDownloadRequest) (*DownloadedSource, error)
}

// isSectionedRun reports whether this run downloads time slices.
//
// Explicit-clip runs (len(in.Clips) > 0) are deliberately EXCLUDED: those
// timestamp ranges are operator-authored absolute positions, and re-anchoring
// them — or collapsing them into one downloaded span from the first to the last
// operator range — would change what the operator asked for. They keep the
// whole-source behaviour.
func isSectionedRun(in *RunInput) bool {
	return in != nil && in.DownloadMode == DownloadModeSectionsOnly && len(in.Clips) == 0
}

// sectionWindowForPlans derives the absolute [start, end) window a sections_only
// run must download for one source's plan group.
//
// ok is false — meaning "download the whole source", i.e. the pre-existing
// behaviour — whenever the plan does not describe one contiguous slice:
//   - empty group, or a non-positive/inverted clip window;
//   - a gap or overlap between consecutive clips (the invariant the layout pass
//     establishes is that the published clips are adjacent windows);
//   - a negative start.
//
// Every `ok == false` case degrades to the previous behaviour instead of guessing
// a window, so this helper can never truncate a source the plan did not ask to
// truncate.
func sectionWindowForPlans(plans []ClipPlan) (start, end float64, ok bool) {
	if len(plans) == 0 {
		return 0, 0, false
	}
	start = plans[0].StartSec
	end = plans[0].EndSec
	if end <= start || start < 0 {
		return 0, 0, false
	}
	for i, plan := range plans {
		if plan.EndSec <= plan.StartSec || plan.StartSec < 0 {
			return 0, 0, false
		}
		if i > 0 && math.Abs(plan.StartSec-plans[i-1].EndSec) > sectionBoundaryEpsilonSec {
			return 0, 0, false
		}
		if plan.StartSec < start {
			start = plan.StartSec
		}
		if plan.EndSec > end {
			end = plan.EndSec
		}
	}
	return start, end, true
}

// sectionDownloadString renders the yt-dlp `--download-sections` value:
// `*HH:MM:SS.mmm-HH:MM:SS.mmm` (the `*` prefix selects an absolute time range).
func sectionDownloadString(start, end float64) string {
	return fmt.Sprintf("*%s-%s", sectionTimestamp(start), sectionTimestamp(end))
}

// sectionTimestamp formats seconds as yt-dlp's HH:MM:SS.mmm.
func sectionTimestamp(sec float64) string {
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	total := int(sec)
	ms := int(math.Round((sec - float64(total)) * 1000))
	if ms == 1000 {
		// 59.9996s must render as 00:01:00.000, never 00:00:60.000.
		total++
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d.%03d", total/3600, (total%3600)/60, total%60, ms)
}
