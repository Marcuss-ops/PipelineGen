package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"go.uber.org/zap"
)

const (
	schedulerTick        = 30 * time.Second
	defaultLeaseDuration = 30 * time.Minute
	maxBackoff           = 24 * time.Hour
	initialBackoff       = 5 * time.Minute
)

// Start begins the channel monitoring process.
// PR 5 (June 2026): job-based sync with ClaimDue/MarkChecked scheduler.
// PR 7 (June 2026): uses channels.Channel DTO directly; loadConfig/fromChannelDTO/MonitorConfig removed.
// PR (June 2026, Blocco 1 of channel-monitor hardening): the previous
// ListEnabled "initial setup" shortcut is REMOVED so the cold-start
// cycle is identical to every subsequent cycle — first tick goes
// through runSchedulerCycle → ClaimDue → lease → checkChannel →
// MarkChecked. The previous shortcut ran checks OUTSIDE that path
// (no worker_id, no lease, no consecutive_failures increment) which
// is exactly what sabotaged the exponential backoff math in
// nextCheckTime: the failure-counter never moved on cold start.
func (m *ChannelMonitor) Start(ctx context.Context) {
	m.log.Info("Starting channel monitor (PR 7: typed Channel DTO)")

	if m.channelsSvc == nil {
		m.log.Error("Channel monitor: channels service not wired, cannot start")
		return
	}

	m.log.Info("Channel monitor entering scheduling loop (first check via runSchedulerCycle)",
		zap.Duration("tick", schedulerTick))

	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.runSchedulerCycle(ctx)
		case <-m.stopCh:
			m.log.Info("Channel monitor stopped")
			return
		case <-ctx.Done():
			m.log.Info("Channel monitor context cancelled")
			return
		}
	}
}

func (m *ChannelMonitor) runSchedulerCycle(ctx context.Context) {
	now := time.Now()
	nowStr := now.Format(time.RFC3339)
	leaseUntil := now.Add(defaultLeaseDuration).Format(time.RFC3339)
	workerID := "monitor-" + fmt.Sprintf("%d", time.Now().UnixNano()%10000)

	result, err := m.channelsSvc.ClaimDue(ctx, channels.ClaimDueCommand{
		Now:        nowStr,
		WorkerID:   workerID,
		LeaseUntil: leaseUntil,
		Limit:      10,
	})
	if err != nil {
		m.log.Error("Failed to claim due channels", zap.Error(err))
		return
	}

	if len(result.Channels) == 0 {
		return
	}

	m.log.Info("Claimed due channels for checking", zap.Int("count", len(result.Channels)))

	m.checkDueChannels(ctx, result.Channels)
}

func (m *ChannelMonitor) checkDueChannels(ctx context.Context, chs []channels.Channel) {
	for _, ch := range chs {
		ch := ch

		m.globalSem <- struct{}{}
		go func() {
			defer func() {
				<-m.globalSem
				if r := recover(); r != nil {
					m.log.Error("panic in channel check goroutine",
						zap.Any("recover", r),
						zap.String("channel_id", ch.ID))
				}
			}()

			checkCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()

			result, checkErr := m.checkChannel(checkCtx, ch)
			m.log.Info("channel check completed",
				zap.String("channel_id", ch.ID),
				zap.Bool("success", checkErr == nil),
				zap.Int("videos_discovered", result.VideosDiscovered),
				zap.Int("videos_enqueued", result.VideosEnqueued),
				zap.Int("videos_skipped", result.VideosSkipped))

			if recErr := m.recordCheckOutcome(ctx, ch, checkErr); recErr != nil {
				m.log.Error("Failed to mark channel as checked",
					zap.String("channel_id", ch.ID),
					zap.Error(recErr))
			}
		}()
	}
}

// recordCheckOutcome translates a checkChannel error into the
// MarkChecked success/failure contract and persists the outcome.
//
// Blocco 1 of channel-monitor hardening: extracted from
// checkDueChannels so the success=false backoff propagation path
// can be unit-tested without spinning up a real yt-dlp subprocess
// (use a fake MonitorDownloaderPort; see monitor_scheduler_test.go).
//
// On checkErr != nil:
//   - Success = false
//   - LastError = checkErr.Error()
//   - nextCheckTime follows the exponential backoff curve
//     (5min → 10min → 20min → … → 24h cap).
// On checkErr == nil:
//   - Success = true
//   - LastError = ""
//   - nextCheckTime = channel.CheckInterval ahead of now
//     (fallback to 24h on parse error).
//
// This helper uses the PARENT ctx (not checkCtx) by design: the
// MarkChecked write must persist even after the per-check 30-min
// deadline trips, so a long yt-dlp run that times out still records
// Success=false + the timeout error + backoff-driven NextCheckAt.
// Detaching from checkCtx is intentional; detaching from `ctx` would
// break the outcome write on workspace shutdown (we'd rather log
// "MarkChecked failed" than lose the outcome).
func (m *ChannelMonitor) recordCheckOutcome(ctx context.Context, ch channels.Channel, checkErr error) error {
	success := checkErr == nil
	lastErr := ""
	if checkErr != nil {
		lastErr = checkErr.Error()
	}
	nextCheckAt := m.nextCheckTime(ch, success)
	return m.channelsSvc.MarkChecked(ctx, channels.MarkCheckedCommand{
		ID:          ch.ID,
		NextCheckAt: nextCheckAt,
		Success:     success,
		LastError:   lastErr,
	})
}

func (m *ChannelMonitor) nextCheckTime(ch channels.Channel, success bool) string {
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
	backoff := initialBackoff
	for i := 1; i < failures && backoff < maxBackoff; i++ {
		backoff *= 2
	}
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	return time.Now().Add(backoff).Format(time.RFC3339)
}
