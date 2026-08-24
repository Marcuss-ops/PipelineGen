package assets

import (
	"context"
	"errors"
	"strings"
	"testing"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"go.uber.org/zap"
)

// TestSafeCheckChannel_PanicAlwaysCallsRecordCheckOutcome is the
// regression pin for P1 #9. The pre-fix in-goroutine `defer recover()`
// logged the panic but the recover itself was AFTER the recordCheckOutcome
// call site, so the channel stayed leased until expiry. Post-fix
// safeCheckChannel converts the panic into a typed error so the caller's
// recordCheckOutcome path always fires.
//
// Test rig: a structured ytdlp port that panics on ListChannel. We
// drive safeCheckChannel directly + capture the synthetic err,
// then assert that recordCheckOutcome correctly persists the
// synthesized error to the recording repo via Success=false +
// LastError containing the panic Value + channel id.
func TestSafeCheckChannel_PanicAlwaysCallsRecordCheckOutcome(t *testing.T) {
	repo := &recordingRepo{}
	svc := channels.NewService(repo, zap.NewNop())

	panicErr := errors.New("intentional panic for test")
	panicDL := &panicLister{panicValue: panicErr}
	m := &ChannelMonitor{
		channelsSvc: svc,
		log:         zap.NewNop(),
		ytdlp:       panicDL,
	}

	ch := channels.Channel{
		ID:            "panic-chan",
		ChannelURL:    "https://www.youtube.com/@Panic",
		CheckInterval: "1h",
	}

	result, err := m.safeCheckChannel(context.Background(), ch)
	if err == nil {
		t.Fatal("safeCheckChannel must convert panic to err")
	}
	if !errors.Is(err, panicErr) {
		t.Errorf("safeCheckChannel err = %v, want errors.Is wrap of %v", err, panicErr)
	}
	if result.VideosDiscovered != 0 {
		t.Errorf("ChannelCheckResult on panic should be zero, got %+v", result)
	}

	// Now feed the recovered err into recordCheckOutcome — this is
	// exactly what checkDueChannels' post-fix goroutine does (per
	// scheduler.go comment "the caller's recordCheckOutcome always
	// fires"). MarkChecked must fire with Success=false (the panic
	// is treated as a transient check failure → exponential backoff).
	if recErr := m.recordCheckOutcome(context.Background(), ch, err); recErr != nil {
		t.Fatalf("recordCheckOutcome returned: %v", recErr)
	}
	if !repo.marked {
		t.Fatal("MarkChecked was not called after panic recovery (P1 #9 regression)")
	}
	if repo.lastMarkCheckedCmd.Success {
		t.Fatal("Success must be false on panic (backoff path)")
	}
	if repo.lastMarkCheckedCmd.LastError == "" {
		t.Fatal("LastError must carry the synthesized panic message")
	}
	// LastError should mention the channel id so operators can
	// correlate with logs.
	if !strings.Contains(repo.lastMarkCheckedCmd.LastError, "panic-chan") {
		t.Errorf("LastError should mention channel id 'panic-chan', got %q",
			repo.lastMarkCheckedCmd.LastError)
	}
}

// TestDefaultMonitorRuntimePolicy_ReturnsCanonicalDefaults pins the
// P1 #10 extraction: the canonical defaults must match the previous
// hardcoded values exactly. Future drift would be visible here.
func TestDefaultMonitorRuntimePolicy_ReturnsCanonicalDefaults(t *testing.T) {
	p := DefaultMonitorRuntimePolicy()
	if p.TickInterval != 30*1000*1000*1000 { // 30s in ns
		t.Errorf("TickInterval = %v, want 30s", p.TickInterval)
	}
	if p.LeaseDuration != 30*60*1000*1000*1000 { // 30min in ns
		t.Errorf("LeaseDuration = %v, want 30m", p.LeaseDuration)
	}
	if p.ClaimLimit != 10 {
		t.Errorf("ClaimLimit = %d, want 10", p.ClaimLimit)
	}
	if p.MaxConcurrentChannels != 1 {
		t.Errorf("MaxConcurrentChannels = %d, want 1", p.MaxConcurrentChannels)
	}
	if p.MaxConcurrentVideos != 5 {
		t.Errorf("MaxConcurrentVideos = %d, want 5", p.MaxConcurrentVideos)
	}
	if p.PerChannelTimeout != 30*60*1000*1000*1000 { // 30min in ns
		t.Errorf("PerChannelTimeout = %v, want 30m", p.PerChannelTimeout)
	}
	if p.BackoffInitial != 5*60*1000*1000*1000 { // 5min in ns
		t.Errorf("BackoffInitial = %v, want 5m", p.BackoffInitial)
	}
	if p.BackoffCap != 24*60*60*1000*1000*1000 { // 24h in ns
		t.Errorf("BackoffCap = %v, want 24h", p.BackoffCap)
	}
}

// TestPolicyOrDefault_NilPolicyReturnsDefault verifies the nil-fallback
// path: existing tests that construct &ChannelMonitor{...} literal
// without setting policy must continue to compile + run.
func TestPolicyOrDefault_NilPolicyReturnsDefault(t *testing.T) {
	m := &ChannelMonitor{
		channelsSvc: channels.NewService(&recordingRepo{}, zap.NewNop()),
		log:         zap.NewNop(),
	}
	got := m.policyOrDefault()
	if got != DefaultMonitorRuntimePolicy() {
		t.Errorf("policyOrDefault should return DefaultMonitorRuntimePolicy when m.policy is nil")
	}
}

// TestRecordCheckOutcome_PropagatesLeaseOwnerAsLeaseToken is the
// regression pin for P1 #8 at the application layer: ch.LeaseOwner
// must reach cmd.LeaseToken so the SQLite repository's fenced UPDATE
// has the right token. Empty ch.LeaseOwner is the back-compat path
// (empty cmd.LeaseToken → un-fenced UPDATE).
func TestRecordCheckOutcome_PropagatesLeaseOwnerAsLeaseToken(t *testing.T) {
	repo := &recordingRepo{}
	svc := channels.NewService(repo, zap.NewNop())
	m := &ChannelMonitor{
		channelsSvc: svc,
		log:         zap.NewNop(),
	}

	t.Run("non-empty LeaseOwner propagates", func(t *testing.T) {
		repo.markCheckedCalls = 0
		repo.marked = false
		ch := channels.Channel{
			ID:            "leased-chan",
			LeaseOwner:    "worker-A",
			CheckInterval: "1h",
		}
		if err := m.recordCheckOutcome(context.Background(), ch, nil); err != nil {
			t.Fatalf("recordCheckOutcome err: %v", err)
		}
		if repo.lastMarkCheckedCmd.LeaseToken != "worker-A" {
			t.Errorf("LeaseToken = %q, want worker-A", repo.lastMarkCheckedCmd.LeaseToken)
		}
	})

	t.Run("empty LeaseOwner produces empty LeaseToken (back-compat)", func(t *testing.T) {
		repo.markCheckedCalls = 0
		repo.marked = false
		ch := channels.Channel{
			ID:            "no-lease-chan",
			LeaseOwner:    "",
			CheckInterval: "1h",
		}
		if err := m.recordCheckOutcome(context.Background(), ch, nil); err != nil {
			t.Fatalf("recordCheckOutcome err: %v", err)
		}
		if repo.lastMarkCheckedCmd.LeaseToken != "" {
			t.Errorf("LeaseToken = %q, want empty (back-compat path)", repo.lastMarkCheckedCmd.LeaseToken)
		}
	})
}

// panicLister is a MonitorDownloaderPort double that panics on
// ListChannelVideos. Drives the P1 #9 safeCheckChannel test path.
// Commit 3/6 (PR-4 DateAfter): surface switched from ListChannel
// (3-arg) to ListChannelVideos (1-arg struct).
//
// FASE 3.7 Commit 1b (2026-07-04): the request type switched from
// `downloader.ListChannelVideosRequest` (infra) to the
// monitor-owned `ListChannelVideosQuery` (declared in types_dto.go).
// No `internal/infrastructure` import remains in this file.
type panicLister struct {
	panicValue any
}

func (p *panicLister) ListChannelVideos(_ context.Context, _ ListChannelVideosQuery) ([]VideoInfo, error) {
	panic(p.panicValue)
}

func (p *panicLister) Path() string { return "/dev/null" }

// Compile-time assertion.
var _ MonitorDownloaderPort = (*panicLister)(nil)
