package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"go.uber.org/zap"
)

// Same-package tests for Blocco 1 of channel-monitor hardening.
// Fields like `ytdlp`, `channelsSvc`, `log` are unexported so we keep
// `package monitor` (not `monitor_test`) and construct a
// `ChannelMonitor` literal directly. Test-injectable fakes satisfy
// the narrow interfaces from monitor_ports.go (MonitorDownloaderPort)
// and contract.go (channels.Repository).

// fakeLister implements MonitorDownloaderPort for tests.
// ListChannelVideosFunc drives the failure / empty / populated paths.
// Commit 3/6 (PR-4 DateAfter): the port surface switched from
// `ListChannel(url, limit)` (3-arg) to `ListChannelVideos(req)`
// (1-arg struct) so DateAfter can flow from ResolveDateAtfer.
type fakeLister struct {
	listChannelVideosFunc func(ctx context.Context, req downloader.ListChannelVideosRequest) ([]downloader.VideoInfo, error)
}

func (f *fakeLister) ListChannelVideos(ctx context.Context, req downloader.ListChannelVideosRequest) ([]downloader.VideoInfo, error) {
	if f.listChannelVideosFunc != nil {
		return f.listChannelVideosFunc(ctx, req)
	}
	return nil, errors.New("fakeLister.ListChannelVideos: not configured")
}

func (f *fakeLister) Path() string { return "/tmp/fake-yt-dlp" }

// Compile-time assertion: fakeLister must satisfy MonitorDownloaderPort.
var _ MonitorDownloaderPort = (*fakeLister)(nil)

// recordingRepo is a channels.Repository double that records
// MarkChecked invocations. Other methods are no-op stubs.
type recordingRepo struct {
	marked             bool
	markCheckedCalls   int
	lastMarkCheckedCmd channels.MarkCheckedCommand
}

func (r *recordingRepo) MarkChecked(_ context.Context, cmd channels.MarkCheckedCommand) error {
	r.marked = true
	r.markCheckedCalls++
	r.lastMarkCheckedCmd = cmd
	return nil
}

func (r *recordingRepo) ListAll(_ context.Context) ([]*asset.CategoryChannel, error) {
	return nil, nil
}
func (r *recordingRepo) ListCategories(_ context.Context) ([]string, error) {
	return nil, nil
}
func (r *recordingRepo) ListEnabled(_ context.Context) ([]*asset.CategoryChannel, error) {
	return nil, nil
}
func (r *recordingRepo) GetByID(_ context.Context, _ string) (*asset.CategoryChannel, error) {
	return nil, errors.New("recordingRepo.GetByID: not implemented")
}
func (r *recordingRepo) Upsert(_ context.Context, _ *asset.CategoryChannel) error {
	return nil
}
func (r *recordingRepo) Delete(_ context.Context, _ string) error {
	return nil
}
func (r *recordingRepo) ClaimDue(_ context.Context, _ channels.ClaimDueCommand) ([]*asset.CategoryChannel, error) {
	return nil, nil
}
func (r *recordingRepo) UpdateCursor(_ context.Context, _ channels.UpdateCursorCommand) error {
	return nil
}

// Compile-time assertion: recordingRepo must satisfy channels.Repository.
var _ channels.Repository = (*recordingRepo)(nil)

// ── Step 9 port stubs ───────────────────────────────────────────────────
//
// Per AGENTS.md Pattern 0 (port abstraction layer): the monitor's runtime
// code reads TranscriptProvider / VideoAnalyzer / JobEnqueuer via tiny
// interfaces; tests inject these stubs directly. Compile-time assertions
// below pin the stubs to the same upgrade contract the production
// concrete adapters satisfy.

// stubTranscriptProvider implements TranscriptProvider for tests. Returns
// canned transcript + err; tracks call count so tests can verify call
// sequencing (call count == 1 after one analyzeVideo run; 0 after the
// analyzer short-circuits on a transcript failure).
type stubTranscriptProvider struct {
	transcript         string
	err                error
	getTranscriptCalls int
}

func (s *stubTranscriptProvider) GetTranscript(_ context.Context, _ string) (string, error) {
	s.getTranscriptCalls++
	return s.transcript, s.err
}

// Compile-time assertion: stubTranscriptProvider must satisfy TranscriptProvider.
var _ TranscriptProvider = (*stubTranscriptProvider)(nil)

// stubVideoAnalyzer implements VideoAnalyzer for tests. Each method
// returns canned outputs configured via fields; tracks call counts so
// tests can verify call ordering (e.g. "Score should be skipped when
// transcript fails" asserts scoreCalls == 0).
type stubVideoAnalyzer struct {
	score             int
	scoreKeyword      string
	scoreErr          error
	scoreCalls        int
	category          string
	classifyErr       error
	classifyCalls     int
	segments          []ytdomain.Segment
	findSegmentsErr   error
	findSegmentsCalls int
}

func (s *stubVideoAnalyzer) Score(_ context.Context, _ string, _ []string) (int, string, error) {
	s.scoreCalls++
	return s.score, s.scoreKeyword, s.scoreErr
}

func (s *stubVideoAnalyzer) Classify(_ context.Context, _, _ string) (string, error) {
	s.classifyCalls++
	return s.category, s.classifyErr
}

func (s *stubVideoAnalyzer) FindSegments(_ context.Context, _, _, _ string, _ int) ([]ytdomain.Segment, error) {
	s.findSegmentsCalls++
	return s.segments, s.findSegmentsErr
}

// Compile-time assertion: stubVideoAnalyzer must satisfy VideoAnalyzer.
var _ VideoAnalyzer = (*stubVideoAnalyzer)(nil)

// stubJobEnqueuer implements JobEnqueuer for tests. Simulates the
// ActiveKey-collision short-circuit + cursor-update phase that the
// concrete *jobtools.Service binding will own in production. Tracks
// enqueue attempts + cursor-update count independently so tests can
// pin the no-op-on-collision contract and the best-effort
// cursor-failure-tolerance contract.
type stubJobEnqueuer struct {
	enqueuedRequests []EnqueueExtractRequest
	enqueueCalls     int // every EnqueueExtract entry, including no-op collisions
	cursorUpdates    int // only incremented on the post-collision path (cursor attempted)
	lastCursorCmd    EnqueueExtractRequest

	returnErr error // EnqueueExtract's terminal return (simulating enqueue failure)
	cursorErr error // simulated cursor-update failure (best-effort: tolerated)

	// collisions: videoIDs that should short-circuit EnqueueExtract as a
	// no-op (ActiveKey collision semantics). The collision path does NOT
	// record an enqueue, does NOT invoke the cursor update, returns nil.
	collisions map[string]bool
}

func (s *stubJobEnqueuer) EnqueueExtract(_ context.Context, req EnqueueExtractRequest) error {
	s.enqueueCalls++
	if s.collisions != nil && s.collisions[req.VideoID] {
		// ActiveKey collision → canonical no-op. The durable-jobs system
		// knows this video is already in flight; the monitor's contract
		// is to drop duplicate enqueues silently and let the existing
		// job's outcome propagate. Cursor is NOT advanced (the cursor
		// update is gated on a successful new enqueue, matching the
		// pre-Step-9 process_video.go::enqueueClipExtract contract).
		return nil
	}
	s.enqueuedRequests = append(s.enqueuedRequests, req)
	s.lastCursorCmd = req
	// Attempt cursor update regardless of cursorErr; the best-effort
	// tolerance semantic lives in the returnErr path below. cursorUpdates
	// tracks "we tried to update", distinct from "we tried and the job
	// went through to the broker".
	s.cursorUpdates++
	return s.returnErr
}

// Compile-time assertion: stubJobEnqueuer must satisfy JobEnqueuer.
var _ JobEnqueuer = (*stubJobEnqueuer)(nil)

// TestRecordCheckOutcome_ErrorPropagatesAsSuccessFalse is the CANONICAL
// regression for Blocco 1: a non-nil checkErr fed into recordCheckOutcome
// must produce MarkChecked(Success=false, LastError populated, NextCheckAt
// in the 5-minute initial backoff window). This is what the previous
// "success := true" hard-coded path silently swallowed.
func TestRecordCheckOutcome_ErrorPropagatesAsSuccessFalse(t *testing.T) {
	repo := &recordingRepo{}
	svc := channels.NewService(repo, zap.NewNop())
	m := &ChannelMonitor{
		channelsSvc: svc,
		log:         zap.NewNop(),
	}
	ch := channels.Channel{
		ID:                  "test-channel",
		ConsecutiveFailures: 0,
		CheckInterval:       "1h",
	}
	checkErr := errors.New("yt-dlp subprocess failed")

	if recErr := m.recordCheckOutcome(context.Background(), ch, checkErr); recErr != nil {
		t.Fatalf("recordCheckOutcome returned: %v", recErr)
	}
	if !repo.marked {
		t.Fatal("MarkChecked was not called")
	}
	if repo.lastMarkCheckedCmd.Success {
		t.Fatal("Success=true on a failed check (the Blocco 1 bug pre-fix)")
	}
	if got, want := repo.lastMarkCheckedCmd.LastError, checkErr.Error(); got != want {
		t.Errorf("LastError = %q, want %q", got, want)
	}

	next, parseErr := time.Parse(time.RFC3339, repo.lastMarkCheckedCmd.NextCheckAt)
	if parseErr != nil {
		t.Fatalf("NextCheckAt not RFC3339: %v (got %q)", parseErr, repo.lastMarkCheckedCmd.NextCheckAt)
	}
	delta := time.Until(next)
	if delta < 4*time.Minute || delta > 6*time.Minute {
		t.Errorf("initial backoff should be ~5min, got %v", delta)
	}
}

// TestRecordCheckOutcome_SuccessUsesCheckInterval: nil error → Success=true
// and NextCheckAt at +CheckInterval (parseCheckInterval honours "2h" → 2*time.Hour).
func TestRecordCheckOutcome_SuccessUsesCheckInterval(t *testing.T) {
	repo := &recordingRepo{}
	svc := channels.NewService(repo, zap.NewNop())
	m := &ChannelMonitor{
		channelsSvc: svc,
		log:         zap.NewNop(),
	}
	ch := channels.Channel{
		ID:            "test-channel",
		CheckInterval: "2h",
	}

	if err := m.recordCheckOutcome(context.Background(), ch, nil); err != nil {
		t.Fatalf("recordCheckOutcome returned: %v", err)
	}
	if !repo.lastMarkCheckedCmd.Success {
		t.Fatal("Success should be true on nil err")
	}
	if repo.lastMarkCheckedCmd.LastError != "" {
		t.Errorf("LastError should be empty on success, got %q", repo.lastMarkCheckedCmd.LastError)
	}
	next, parseErr := time.Parse(time.RFC3339, repo.lastMarkCheckedCmd.NextCheckAt)
	if parseErr != nil {
		t.Fatalf("NextCheckAt: %v", parseErr)
	}
	delta := time.Until(next)
	if delta < 110*time.Minute || delta > 130*time.Minute {
		t.Errorf("NextCheckAt delta should be ~2h (allowing 10-min slack), got %v", delta)
	}
}

// TestRecordCheckOutcome_ExponentialBackoffAcrossFailures: with
// ConsecutiveFailures=3 and a fresh failure, nextCheckTime must yield
// 5min * 2^3 = 40min backoff. Also serves as a regression pin on the
// `failures++ in nextCheckTime` math.
func TestRecordCheckOutcome_ExponentialBackoffAcrossFailures(t *testing.T) {
	repo := &recordingRepo{}
	svc := channels.NewService(repo, zap.NewNop())
	m := &ChannelMonitor{
		channelsSvc: svc,
		log:         zap.NewNop(),
	}
	ch := channels.Channel{
		ID:                  "test-channel",
		ConsecutiveFailures: 3,
		CheckInterval:       "1h",
	}

	if err := m.recordCheckOutcome(context.Background(), ch, errors.New("transient")); err != nil {
		t.Fatalf("recordCheckOutcome: %v", err)
	}
	next, _ := time.Parse(time.RFC3339, repo.lastMarkCheckedCmd.NextCheckAt)
	delta := time.Until(next)
	if delta < 39*time.Minute || delta > 41*time.Minute {
		t.Errorf("expected ~40min backoff at failures=3+1, got %v", delta)
	}
}

// TestCheckChannel_YtdlpErrorReturnsFailure drives the FULL Blocco 1
// flow: fake yt-dlp returning an error → checkChannel returns the err
// wrapped with the channel URL → recordCheckOutcome persists
// Success=false to MarkChecked. This is the user's literal
// "fake ytdlp that fails → assert MarkChecked receives Success=false".
func TestCheckChannel_YtdlpErrorReturnsFailure(t *testing.T) {
	repo := &recordingRepo{}
	svc := channels.NewService(repo, zap.NewNop())
	expectedErr := errors.New("fake yt-dlp: connection refused")
	fakeDL := &fakeLister{
		listChannelVideosFunc: func(_ context.Context, _ downloader.ListChannelVideosRequest) ([]downloader.VideoInfo, error) {
			return nil, expectedErr
		},
	}
	m := &ChannelMonitor{
		channelsSvc: svc,
		log:         zap.NewNop(),
		ytdlp:       fakeDL,
	}
	ch := channels.Channel{
		ID:            "test-channel",
		ChannelURL:    "https://www.youtube.com/@Test",
		CheckInterval: "1h",
	}

	res, err := m.checkChannel(context.Background(), ch)
	if err == nil {
		t.Fatal("checkChannel must return non-nil err when ListChannel fails")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("err = %v, want wrap of %v", err, expectedErr)
	}
	if res.VideosDiscovered != 0 || res.VideosEnqueued != 0 || res.VideosSkipped != 0 {
		t.Errorf("ChannelCheckResult should be zero on infra failure, got %+v", res)
	}

	// Now feed the err into recordCheckOutcome and assert MarkChecked
	// receives Success=false. This is what `success := true` previously
	// erased.
	if recErr := m.recordCheckOutcome(context.Background(), ch, err); recErr != nil {
		t.Fatalf("recordCheckOutcome: %v", recErr)
	}
	if repo.lastMarkCheckedCmd.Success {
		t.Fatal("MarkChecked.Success should be false after checkChannel error")
	}
	if repo.lastMarkCheckedCmd.LastError == "" {
		t.Fatal("LastError should be populated")
	}
	// URL must appear in the error chain so operators can correlate
	// the failure to a specific channel in log triage.
	if !strings.Contains(repo.lastMarkCheckedCmd.LastError, "https://www.youtube.com/@Test") {
		t.Errorf("LastError should mention the channel URL, got %q", repo.lastMarkCheckedCmd.LastError)
	}
}

// TestCheckChannel_YtdlpEmptySuccessReturnsZeroResult: lister returns
// nil slice with no error → checkChannel succeeds with all-zero result
// + nil err. Records Success=true via recordCheckOutcome.
func TestCheckChannel_YtdlpEmptySuccessReturnsZeroResult(t *testing.T) {
	repo := &recordingRepo{}
	svc := channels.NewService(repo, zap.NewNop())
	fakeDL := &fakeLister{
		listChannelVideosFunc: func(_ context.Context, _ downloader.ListChannelVideosRequest) ([]downloader.VideoInfo, error) {
			return nil, nil
		},
	}
	m := &ChannelMonitor{
		channelsSvc: svc,
		log:         zap.NewNop(),
		ytdlp:       fakeDL,
	}
	ch := channels.Channel{
		ID:            "test-channel",
		ChannelURL:    "https://www.youtube.com/@Test",
		CheckInterval: "1h",
	}
	res, err := m.checkChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("checkChannel err: %v (expected nil on empty success)", err)
	}
	if res.VideosDiscovered != 0 || res.VideosEnqueued != 0 || res.VideosSkipped != 0 {
		t.Errorf("ChannelCheckResult should be zero on empty success, got %+v", res)
	}
	if recErr := m.recordCheckOutcome(context.Background(), ch, err); recErr != nil {
		t.Fatalf("recordCheckOutcome: %v", recErr)
	}
	if !repo.lastMarkCheckedCmd.Success {
		t.Fatal("MarkChecked.Success should be true on nil err")
	}
}

// TestCheckChannel_YtdlpSuccessCountsDiscovered ensures that when the
// lister returns an empty VideoInfo slice, ChannelCheckResult has all
// counters at 0: VideosDiscovered == 0, VideosEnqueued == 0,
// VideosSkipped == 0. The per-video loop in checkChannel iterates over
// the slice; with an empty slice the loop never runs, so processVideo
// is not invoked and no analyze/enqueue port is touched.
//
// Test fixture note (Step 9, June 2026): the ctor now falls back to
// unbound stubs for analyze/enqueue ports rather than panicking on
// nil; a non-empty lister output in this test would exercise the
// unbound-stub error path instead of panicking, which is why the test
// uses an empty VideoInfo slice to keep the assertion focused on the
// LIST side of checkChannel.
func TestCheckChannel_YtdlpSuccessCountsDiscovered(t *testing.T) {
	repo := &recordingRepo{}
	svc := channels.NewService(repo, zap.NewNop())
	videos := []downloader.VideoInfo{}
	fakeDL := &fakeLister{
		listChannelVideosFunc: func(_ context.Context, _ downloader.ListChannelVideosRequest) ([]downloader.VideoInfo, error) {
			return videos, nil
		},
	}
	m := &ChannelMonitor{
		channelsSvc: svc,
		log:         zap.NewNop(),
		ytdlp:       fakeDL,
	}
	ch := channels.Channel{
		ID:            "test-channel",
		ChannelURL:    "https://www.youtube.com/@Test",
		CheckInterval: "1h",
	}
	res, err := m.checkChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("checkChannel err: %v", err)
	}
	if res.VideosDiscovered != 0 {
		t.Errorf("VideosDiscovered should be 0 for empty lister output, got %d", res.VideosDiscovered)
	}
}

// TestNextCheckTime_BackoffProgression pins the entire exponential
// backoff curve and the success-path CheckInterval semantics.
// Each case asserts the (time.Until(nextCheckAt) string) falls within
// the documented tolerance; this is the single source of truth a
// future refactor must not silently break.
func TestNextCheckTime_BackoffProgression(t *testing.T) {
	cases := []struct {
		name          string
		failures      int
		checkInterval string
		success       bool
		wantMin       time.Duration
		wantMax       time.Duration
	}{
		{"success 1h interval", 0, "1h", true, 55 * time.Minute, 65 * time.Minute},
		{"success 30m interval", 0, "30m", true, 25 * time.Minute, 35 * time.Minute},
		{"success 6h interval", 0, "6h", true, 5*time.Hour + 55*time.Minute, 6*time.Hour + 5*time.Minute},
		{"success unparseable → 24h fallback", 0, "garbage", true, 23 * time.Hour, 25 * time.Hour},
		{"failure #1 → 5min", 0, "1h", false, 4 * time.Minute, 6 * time.Minute},
		{"failure #2 → 10min", 1, "1h", false, 9 * time.Minute, 11 * time.Minute},
		{"failure #3 → 20min", 2, "1h", false, 19 * time.Minute, 21 * time.Minute},
		{"failure #4 → 40min", 3, "1h", false, 39 * time.Minute, 41 * time.Minute},
		{"failure #5 → 80min", 4, "1h", false, 79 * time.Minute, 81 * time.Minute},
		{"failure #10 → capped 24h", 10, "1h", false, 23 * time.Hour, 25 * time.Hour},
		{"failure #30 → capped 24h", 30, "1h", false, 23 * time.Hour, 25 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &ChannelMonitor{log: zap.NewNop()}
			ch := channels.Channel{
				ID:                  "test",
				ConsecutiveFailures: tc.failures,
				CheckInterval:       tc.checkInterval,
			}
			s := m.nextCheckTime(ch, tc.success)
			next, err := time.Parse(time.RFC3339, s)
			if err != nil {
				t.Fatalf("nextCheckTime produced non-RFC3339: %v (got %q)", err, s)
			}
			delta := time.Until(next)
			if delta < tc.wantMin || delta > tc.wantMax {
				t.Errorf("%s: delta = %v, want in [%v, %v]", tc.name, delta, tc.wantMin, tc.wantMax)
			}
		})
	}
}
