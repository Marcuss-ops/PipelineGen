// Package monitor — check_outcome.go: MarkChecked persistence +
// exponential backoff math + utility functions.
//
// God-object decomposition (PR-GODOBJ-2, July 2026): extracted from
// scheduler.go per the action-plan split topology. This file owns:
//   - recordCheckOutcome: translates checkChannel error → MarkChecked.
//   - nextCheckTime: exponential backoff curve (5min → 24h cap).
//   - parseCheckInterval: duration string parser (lives here because
//     it's a time-utility with no VTT / Ollama / exec coupling).
//   - extractChannelHandle: @-prefix handle extraction from YouTube URL.
package assets

import (
	"context"
	"fmt"
	"strings"
	"time"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// recordCheckOutcome translates a checkChannel error into the
// MarkChecked success/failure contract and persists the outcome.
//
// Blocco 1 (channel-monitor hardening): extracted from checkDueChannels
// so the success=false backoff propagation path can be unit-tested
// without spinning up a real yt-dlp subprocess (use a fake
// MonitorDownloaderPort; see monitor_scheduler_test.go).
//
// On checkErr != nil:
//   - Success = false
//   - LastError = checkErr.Error()
//   - nextCheckTime follows the exponential backoff curve
//     (5min → 10min → 20min → … → 24h cap).
//
// On checkErr == nil:
//   - Success = true
//   - LastError = ""
//   - nextCheckTime = channel.CheckInterval ahead of now
//     (fallback to 24h on parse error).
//
// Commit A (P1 #8): forwards ch.LeaseOwner as cmd.LeaseToken so the
// SQLite MarkChecked UPDATE is fenced on lease_owner (see
// channels_repository.go MarkChecked). Empty ch.LeaseOwner falls
// back to an un-fenced UPDATE — but the monitor always writes
// lease_owner=workerID via ClaimDue, so the fence is always active
// in production.
func (m *ChannelMonitor) recordCheckOutcome(ctx context.Context, ch channels.Channel, checkErr error) error {
	success := checkErr == nil
	lastErr := ""
	if checkErr != nil {
		lastErr = checkErr.Error()
	}
	nextCheckAt := m.nextCheckTime(ch, success)
	return m.channelsSvc.MarkChecked(ctx, channels.MarkCheckedCommand{
		ID:          ch.ID,
		LeaseToken:  ch.LeaseOwner,
		NextCheckAt: nextCheckAt,
		Success:     success,
		LastError:   lastErr,
	})
}

// nextCheckTime returns the RFC3339-format string for when
// the channel should be checked next, following the exponential
// backoff curve on failure. Commit A (P1 #10): reads backoff
// initial/cap from MonitorRuntimePolicy (was previously hardcoded
// to 5min / 24h in scheduler.go const block).
func (m *ChannelMonitor) nextCheckTime(ch channels.Channel, success bool) string {
	policy := m.policyOrDefault()
	if success {
		interval, err := parseCheckInterval(ch.CheckInterval)
		if err != nil {
			interval = 24 * time.Hour
		}
		return time.Now().Add(interval).Format(time.RFC3339)
	}

	failures := ch.ConsecutiveFailures + 1
	if failures < 1 {
		failures = 1
	}
	// Backoff math now routes through pkg/retry.BackoffFor — the
	// canonical owner of "compute exponential backoff" (godlike/06
	// SSOT, see pkg/retry/options.go godlike/06 block). failures-1
	// is the 0-based attempt count: failures=1 → InitialBackoff
	// (single iteration skipped), failures=2 → 2×InitialBackoff,
	// failures=N → 2^(N-1) × InitialBackoff saturated at MaxBackoff.
	// Byte-equivalent with the pre-migration for-loop + post-clamp.
	// JitterFraction defaults to 0 so the persisted `available_at`
	// timestamp is deterministic (a schedule, not a sleep duration).
	backoff := retry.BackoffFor(failures-1, retry.Options{
		InitialBackoff: policy.BackoffInitial,
		BackoffFactor:  2.0,
		MaxBackoff:     policy.BackoffCap,
	})
	return time.Now().Add(backoff).Format(time.RFC3339)
}

// parseCheckInterval parses a duration string like "1h" / "30m" / "7d" /
// "5s" into time.Duration. Lives here (not in vtt_helpers.go, which was
// deleted) because it's a time-utility with no VTT / Ollama / exec
// coupling, so check_outcome.go is the right owner.
func parseCheckInterval(s string) (time.Duration, error) {
	if len(s) == 0 {
		return 7 * 24 * time.Hour, nil // default 7 days
	}
	switch s[len(s)-1] {
	case 'd':
		days := 0
		if _, err := fmt.Sscanf(s, "%dd", &days); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	case 'h':
		hours := 0
		if _, err := fmt.Sscanf(s, "%dh", &hours); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(hours) * time.Hour, nil
	case 'm':
		mins := 0
		if _, err := fmt.Sscanf(s, "%dm", &mins); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(mins) * time.Minute, nil
	default:
		return time.ParseDuration(s)
	}
}

// extractChannelHandle derives the @-prefixed handle from a YouTube
// channel URL. Used by analyzer.go + enqueue.go for Prometheus labels.
// Lives here (not in enqueue.go, which would force analyzer.go to
// re-implement the regex) because both files need it.
func extractChannelHandle(url string) string {
	if url == "" {
		return ""
	}
	if idx := strings.LastIndex(url, "@"); idx >= 0 {
		handle := url[idx+1:]
		handle = strings.TrimRight(handle, "/")
		return handle
	}
	return ""
}
