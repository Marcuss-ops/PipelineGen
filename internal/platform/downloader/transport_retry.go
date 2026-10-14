package downloader

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/process"
)

const transportRetryAttempts = 3

// EnvTransportRetries is the operator override for transport-level acquisition
// retries (PIPELINEGEN_YTDLP_TRANSPORT_RETRIES), clamped to 1..5.
const EnvTransportRetries = "PIPELINEGEN_YTDLP_TRANSPORT_RETRIES"

// transportAttempts resolves the per-acquisition attempt budget. Sectioned
// (--download-sections) fetches get one fewer attempt than full-source
// downloads: with the 2700s section bound, 3 full-bound retries would burn up
// to 135 minutes against a 60-minute job timeout before reporting terminal.
// The job-level retry (DefaultMaxRetries) owns the outer budget; the transport
// layer fails fast so the job can re-plan instead of pinning a worker slot.
func transportAttempts(sectioned bool) int {
	attempts := transportRetryAttempts
	if raw := strings.TrimSpace(os.Getenv(EnvTransportRetries)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			attempts = parsed
		}
	}
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 5 {
		attempts = 5
	}
	if sectioned && attempts > 1 {
		attempts--
	}
	return attempts
}

var transientTransportErrorRe = regexp.MustCompile(`(?i)` +
	`network is unreachable|` +
	`connection reset|connection refused|` +
	`temporary failure in name resolution|temporary failure resolving|` +
	`i/o timeout|connection timed out|` +
	`unexpected eof|network timeout`)

// isTransientTransportError classifies failures where repeating the same
// acquisition request may succeed. It deliberately excludes client-policy
// errors (handled by isYouTubeClientRetryableError), permanent HTTP errors,
// and context cancellation/deadline errors.
func isTransientTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return transientTransportErrorRe.MatchString(err.Error())
}

func (d *YTDLPDownloader) sleepTransportRetry(ctx context.Context, attempt int) error {
	// Backoff is 2s then 5s, with up to 1s jitter. The third attempt is the
	// final attempt and does not sleep afterwards.
	delay := 2 * time.Second
	if attempt >= 2 {
		delay = 5 * time.Second
	}
	delay += time.Duration(rand.Intn(1000)) * time.Millisecond
	if d.transportSleep != nil {
		return d.transportSleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// runWithTransportRetry retries a complete acquisition attempt, preserving
// the separate player-client fallback policy inside runAttempt. A transport
// retry starts from the primary client again; it never treats transport
// errors as evidence that another player client is needed.
func (d *YTDLPDownloader) runWithTransportRetry(ctx context.Context, url string, runAttempt func() (*process.Result, error)) (*process.Result, error) {
	return d.runWithTransportRetrySectioned(ctx, url, false, runAttempt)
}

// runWithTransportRetrySectioned is runWithTransportRetry with the sectioned
// budget: section fetches fail fast (see transportAttempts) so a doomed
// 600s window does not burn multiples of the 2700s bound inside one job.
func (d *YTDLPDownloader) runWithTransportRetrySectioned(ctx context.Context, url string, sectioned bool, runAttempt func() (*process.Result, error)) (*process.Result, error) {
	maxAttempts := transportAttempts(sectioned)
	var lastResult *process.Result
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return lastResult, err
		}
		result, err := runAttempt()
		if err == nil || !isTransientTransportError(err) || attempt == maxAttempts {
			return result, err
		}
		lastResult, lastErr = result, err
		log.Printf("downloader: transient transport error for %s, retrying acquisition (%d/%d): %v", url, attempt+1, maxAttempts, err)
		if err := d.sleepTransportRetry(ctx, attempt); err != nil {
			return lastResult, err
		}
	}
	return lastResult, lastErr
}
