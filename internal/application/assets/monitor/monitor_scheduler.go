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
func (m *ChannelMonitor) Start(ctx context.Context) {
	m.log.Info("Starting channel monitor (PR 7: typed Channel DTO)")

	if m.channelsSvc == nil {
		m.log.Error("Channel monitor: channels service not wired, cannot start")
		return
	}

	// Initial check: run immediately for any enabled channels.
	result, err := m.channelsSvc.ListEnabled(ctx)
	if err != nil {
		m.log.Warn("Failed to list channels for initial setup", zap.Error(err))
	} else if len(result.Channels) > 0 {
		m.log.Info("Running initial check for enabled channels", zap.Int("count", len(result.Channels)))
		m.checkDueChannels(ctx, result.Channels)
	}

	m.log.Info("Channel monitor entering scheduling loop", zap.Duration("tick", schedulerTick))

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

			success := true
			var lastErr string

			m.checkChannel(checkCtx, ch)

			nextCheckAt := m.nextCheckTime(ch, success)

			if err := m.channelsSvc.MarkChecked(ctx, channels.MarkCheckedCommand{
				ID:          ch.ID,
				NextCheckAt: nextCheckAt,
				Success:     success,
				LastError:   lastErr,
			}); err != nil {
				m.log.Error("Failed to mark channel as checked",
					zap.String("channel_id", ch.ID),
					zap.Error(err))
			}
		}()
	}
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
