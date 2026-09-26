// Package downloader — youtube_gate.go: global YouTube IP budget + 429 cooldown.
//
// Every yt-dlp invocation hits the same public IP; 8 construction sites of
// YTDLPDownloader share ONE process-local semaphore so parallel jobs cannot
// burst past YouTube's hot-IP limits. A transient 429 arms a shared cooldown
// that every worker honours before acquiring the next slot.
package downloader

import (
	"context"
	"log"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/process"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

var youTubeRateLimitedRe = regexp.MustCompile(`(?i)` +
	`http error 429|too many requests|rate limit`)

var (
	ytGateOnce sync.Once
	ytGate     concurrent.Semaphore
	// ytCooldownUntil holds unix-nanos of the expiry instant. 0 = no cooldown.
	ytCooldownUntil atomic.Int64
)

// ytGateWidth is the canonical fail-closed default applied when config is 0.
const ytGateWidthDefault = 3

// ytCooldownDefault is used when the per-instance cooldown is not configured.
const ytCooldownDefault = 60 * time.Second

func ensureYouTubeGate(width int) concurrent.Semaphore {
	if width <= 0 {
		width = ytGateWidthDefault
	}
	ytGateOnce.Do(func() { ytGate = concurrent.NewSemaphore(width) })
	return ytGate
}

// resetYouTubeGateForTest replaces the global gate and cooldown. Test-only;
// callers that need hermetic isolation should invoke this under exclusive
// control (no concurrent Download callers in the same process).
func resetYouTubeGateForTest(width int) concurrent.Semaphore {
	if width <= 0 {
		width = ytGateWidthDefault
	}
	ytGate = concurrent.NewSemaphore(width)
	// Keep the Once in the "done" state so ensureYouTubeGate remains a
	// no-op after a test-supplied gate is installed; tests that call
	// resetYouTubeGateForTest explicitly manage the global gate for their
	// process.
	ytGateOnce.Do(func() {})
	ytCooldownUntil.Store(0)
	return ytGate
}

// youTubeCooldownNow is an injectable clock seam matching the transportSleep
// pattern in transport_retry.go. Nil = time.Now.
var youTubeCooldownNow func() time.Time

func nowForYouTubeCooldown() time.Time {
	if youTubeCooldownNow != nil {
		return youTubeCooldownNow()
	}
	return time.Now()
}

// runYouTube executes one yt-dlp invocation under the shared IP budget when
// the URL targets YouTube. Non-YouTube URLs and an uninitialised gate bypass
// the limiter. The cooldown is honoured BEFORE acquiring the semaphore so a
// single cooling worker does not hold a slot hostage for 60s.
func (d *YTDLPDownloader) runYouTube(ctx context.Context, url string, args []string, opts process.Options) (*process.Result, error) {
	if !isYouTubeURL(url) || d == nil || d.ytGate == nil {
		return d.run(ctx, args, opts)
	}
	if err := d.waitOutYouTubeCooldown(ctx); err != nil {
		return nil, err
	}
	if err := d.ytGate.AcquireCtx(ctx); err != nil {
		return nil, err
	}
	defer d.ytGate.Release()

	result, err := d.run(ctx, args, opts)
	if err != nil && youTubeRateLimitedRe.MatchString(err.Error()) {
		cooldown := d.yt429Cooldown
		if cooldown <= 0 {
			cooldown = ytCooldownDefault
		}
		ytCooldownUntil.Store(nowForYouTubeCooldown().Add(cooldown).UnixNano())
		log.Printf("downloader: YouTube rate-limit on %s, pausing acquisitions %s", url, cooldown)
	}
	return result, err
}

func (d *YTDLPDownloader) waitOutYouTubeCooldown(ctx context.Context) error {
	for {
		remaining := time.Until(time.Unix(0, ytCooldownUntil.Load()))
		if remaining <= 0 {
			return nil
		}
		// Slice in 1s so a longer cooldown armed concurrently is honoured.
		wait := remaining
		if wait > time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
