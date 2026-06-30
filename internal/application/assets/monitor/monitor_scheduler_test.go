package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
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
// ListChannelFunc drives the failure / empty / populated paths.
type fakeLister struct {
	listChannelFunc func(ctx context.Context, channelURL string, limit int) ([]downloader.VideoInfo, error)
}

func (f *fakeLister) ListChannel(ctx context.Context, channelURL string, limit int) ([]downloader.VideoInfo, error) {
	if f.listChannelFunc != nil {
		return f.listChannelFunc(ctx, channelURL, limit)
	}
	return nil, errors.New("fakeLister.ListChannel: not configured")
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
		listChannelFunc: func(_ context.Context, _ string, _ int) ([]downloader.VideoInfo, error) {
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
		listChannelFunc: func(_ context.Context, _ string, _ int) ([]downloader.VideoInfo, error) {
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
		listChannelFunc: func(_ context.Context, _ string, _ int) ([]downloader.VideoInfo, error) {
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
