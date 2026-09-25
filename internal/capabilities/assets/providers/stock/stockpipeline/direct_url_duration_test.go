package stockpipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEnrichDirectURLDurations_PopulatesUnknownYouTubeDuration pins the fix
// for the bare direct-URL over-planning failure: a YouTube direct URL with no
// provider duration gets the real source length before the planner runs, so
// the windows stay inside the file.
func TestEnrichDirectURLDurations_PopulatesUnknownYouTubeDuration(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=jNQXAC9IVRw"
	lister := &queryResolutionLister{
		results: map[string][]VideoInfo{url: {{ID: "jNQXAC9IVRw", Title: "me at the zoo", Duration: 19.06}}},
		errors:  make(map[string]error),
	}
	svc := newQueryResolutionService(lister)

	input := &RunInput{DirectURLs: []string{url}}
	svc.enrichDirectURLDurations(context.Background(), input)

	require.Equal(t, 19.06, input.SourceDurations[url])
	_, calls, _ := lister.snapshot()
	require.Equal(t, []string{url}, calls)
}

// TestEnrichDirectURLDurations_SkipsExplicitClipRuns pins that a run carrying
// explicit clips does not pay for a metadata probe: the explicit planner
// consumes the operator windows verbatim and ignores the deterministic
// horizon, so the duration is irrelevant.
func TestEnrichDirectURLDurations_SkipsExplicitClipRuns(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=jNQXAC9IVRw"
	lister := &queryResolutionLister{
		results: map[string][]VideoInfo{url: {{ID: "jNQXAC9IVRw", Duration: 19.06}}},
		errors:  make(map[string]error),
	}
	svc := newQueryResolutionService(lister)

	input := &RunInput{
		DirectURLs: []string{url},
		Clips:      []ClipSpec{{StartSec: 0, EndSec: 5}},
	}
	svc.enrichDirectURLDurations(context.Background(), input)

	require.Empty(t, input.SourceDurations)
	_, calls, _ := lister.snapshot()
	require.Empty(t, calls)
}

// TestEnrichDirectURLDurations_SkipsNonYouTubeSources pins that a non-YouTube
// direct URL (direct mp4 blob) is never probed: the duration-native metadata
// path here is YouTube-only.
func TestEnrichDirectURLDurations_SkipsNonYouTubeSources(t *testing.T) {
	const url = "https://cdn.example.test/video.mp4"
	lister := &queryResolutionLister{results: map[string][]VideoInfo{url: {{ID: "x", Duration: 42}}}}
	svc := newQueryResolutionService(lister)

	input := &RunInput{DirectURLs: []string{url}}
	svc.enrichDirectURLDurations(context.Background(), input)

	require.Empty(t, input.SourceDurations)
	_, calls, _ := lister.snapshot()
	require.Empty(t, calls)
}

// TestEnrichDirectURLDurations_PreservesKnownDuration pins that a duration
// already resolved earlier in the run is authoritative and is not re-probed.
func TestEnrichDirectURLDurations_PreservesKnownDuration(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=jNQXAC9IVRw"
	lister := &queryResolutionLister{
		results: map[string][]VideoInfo{url: {{ID: "jNQXAC9IVRw", Duration: 999}}},
		errors:  make(map[string]error),
	}
	svc := newQueryResolutionService(lister)

	input := &RunInput{
		DirectURLs:      []string{url},
		SourceDurations: map[string]float64{url: 12.5},
	}
	svc.enrichDirectURLDurations(context.Background(), input)

	require.Equal(t, 12.5, input.SourceDurations[url])
	_, calls, _ := lister.snapshot()
	require.Empty(t, calls)
}

// TestEnrichDirectURLDurations_ProbeFailureIsNonFatal pins the godlike/07
// contract: a provider error or an empty result leaves the plan on the
// planner's conservative fallback instead of fabricating a duration.
func TestEnrichDirectURLDurations_ProbeFailureIsNonFatal(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=jNQXAC9IVRw"
	lister := &queryResolutionLister{
		results: map[string][]VideoInfo{url: nil},
		errors:  map[string]error{url: errors.New("yt-dlp failed")},
	}
	svc := newQueryResolutionService(lister)

	input := &RunInput{DirectURLs: []string{url}}
	require.NotPanics(t, func() {
		svc.enrichDirectURLDurations(context.Background(), input)
	})

	require.Empty(t, input.SourceDurations)
	_, calls, _ := lister.snapshot()
	require.Equal(t, []string{url}, calls)

	// A nil-duration result is equally non-fatal.
	emptyLister := &queryResolutionLister{
		results: map[string][]VideoInfo{url: {{ID: "jNQXAC9IVRw", Duration: 0}}},
		errors:  make(map[string]error),
	}
	svc = newQueryResolutionService(emptyLister)
	emptyInput := &RunInput{DirectURLs: []string{url}}
	svc.enrichDirectURLDurations(context.Background(), emptyInput)
	require.Empty(t, emptyInput.SourceDurations)
}

// TestEnrichDirectURLDurations_NilAndEmptyGuards pins the no-op guards.
func TestEnrichDirectURLDurations_NilAndEmptyGuards(t *testing.T) {
	svc := newQueryResolutionService(nil)
	require.NotPanics(t, func() { svc.enrichDirectURLDurations(context.Background(), &RunInput{}) })

	svc = newQueryResolutionService(&queryResolutionLister{})
	require.NotPanics(t, func() { svc.enrichDirectURLDurations(context.Background(), nil) })

	// No direct URLs → no probe.
	input := &RunInput{SearchQueries: []string{"boxing"}}
	svc.enrichDirectURLDurations(context.Background(), input)
	require.Empty(t, input.SourceDurations)
}

// TestEnrichDirectURLDurations_ProbesInParallel pins the bounded-pool
// contract: independent source URLs are probed concurrently instead of
// serially, so a multi-source run does not pay N sequential provider
// round-trips before planning can start. Every URL must still land its
// duration regardless of completion order.
func TestEnrichDirectURLDurations_ProbesInParallel(t *testing.T) {
	urls := []string{
		"https://www.youtube.com/watch?v=aaaaaaaaaaa",
		"https://www.youtube.com/watch?v=bbbbbbbbbbb",
		"https://www.youtube.com/watch?v=ccccccccccc",
		"https://www.youtube.com/watch?v=ddddddddddd",
		"https://www.youtube.com/watch?v=eeeeeeeeeee",
		"https://www.youtube.com/watch?v=fffffffffff",
	}
	results := make(map[string][]VideoInfo, len(urls))
	for i, url := range urls {
		results[url] = []VideoInfo{{ID: url, Title: url, Duration: float64(100 + i)}}
	}
	lister := &queryResolutionLister{
		results: results,
		errors:  make(map[string]error),
		delay:   200 * time.Millisecond,
	}
	svc := newQueryResolutionService(lister)

	input := &RunInput{DirectURLs: append([]string(nil), urls...)}
	svc.enrichDirectURLDurations(context.Background(), input)

	for i, url := range urls {
		require.Equal(t, float64(100+i), input.SourceDurations[url], "duration for %s", url)
	}
	maxActive, calls, _ := lister.snapshot()
	require.Len(t, calls, len(urls))
	require.GreaterOrEqual(t, maxActive, 2,
		"independent probes must overlap; maxActive=%d means the pool ran serially", maxActive)
}

// TestEnrichDirectURLDurations_SuppliedDurationsSkipTheProbe pins the
// source_durations payload contract at the enrichment layer: when the caller
// already described every direct URL, no provider probe is issued at all.
func TestEnrichDirectURLDurations_SuppliedDurationsSkipTheProbe(t *testing.T) {
	urls := []string{
		"https://www.youtube.com/watch?v=aaaaaaaaaaa",
		"https://www.youtube.com/watch?v=bbbbbbbbbbb",
	}
	lister := &queryResolutionLister{
		results: map[string][]VideoInfo{urls[0]: {{ID: urls[0], Duration: 9999}}},
		errors:  make(map[string]error),
	}
	svc := newQueryResolutionService(lister)

	input := &RunInput{
		DirectURLs:      append([]string(nil), urls...),
		SourceDurations: map[string]float64{urls[0]: 640, urls[1]: 900},
	}
	svc.enrichDirectURLDurations(context.Background(), input)

	require.Equal(t, map[string]float64{urls[0]: 640, urls[1]: 900}, input.SourceDurations)
	_, calls, _ := lister.snapshot()
	require.Empty(t, calls, "caller-supplied durations must not trigger a probe")
}

// deadlineLister records the deadline the enrichment attaches to each probe
// so the test can pin the bounded-probe contract without waiting it out.
type deadlineLister struct {
	hadDeadline bool
	deadline    time.Time
}

func (l *deadlineLister) ListChannel(ctx context.Context, _ string, _ int) ([]VideoInfo, error) {
	l.deadline, l.hadDeadline = ctx.Deadline()
	return nil, nil
}

// TestEnrichDirectURLDurations_BoundsProbeWithDeadline pins that each probe
// is bounded by a deadline, so a hung provider cannot stall the planning
// phase indefinitely.
func TestEnrichDirectURLDurations_BoundsProbeWithDeadline(t *testing.T) {
	lister := &deadlineLister{}
	svc := newQueryResolutionService(lister)

	input := &RunInput{DirectURLs: []string{"https://www.youtube.com/watch?v=jNQXAC9IVRw"}}
	svc.enrichDirectURLDurations(context.Background(), input)

	require.True(t, lister.hadDeadline, "probe must carry a deadline")
	require.WithinDuration(t, time.Now().Add(directURLDurationProbeTimeout), lister.deadline, 5*time.Second)
}
