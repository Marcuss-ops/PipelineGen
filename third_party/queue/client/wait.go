package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Producer-side waiting. Two paths share one terminal-state definition:
//
//   - WaitTerminal prefers the server-side long poll GET /jobs/{id}/wait, which
//     is event-driven (the queue's single Notifier wakes the parked request the
//     moment the job reaches a terminal state), and degrades to the polling
//     Wait loop when the queue predates that route;
//   - Wait keeps the historical Get-polling API for compatibility.
//
// `rendered` is deliberately not terminal: the render is durable but external
// publication is still pending and the job remains re-claimable, so a producer
// that stopped waiting could observe an artifact that is never published. This
// mirrors service.IsTerminalState exactly.
const (
	// waitRoundWindow bounds one server-side long poll. The queue caps a single
	// wait at its own maximum, so this is a request, not a guarantee.
	waitRoundWindow = 20 * time.Second
	// waitFallbackInterval paces the polling loop for queues without the wait
	// route.
	waitFallbackInterval = 250 * time.Millisecond
)

// errWaitRouteUnsupported marks a queue server that does not expose the
// producer long poll, so the caller can degrade to polling instead of failing.
var errWaitRouteUnsupported = errors.New("queue wait route unsupported")

// IsTerminalState reports whether a job state is terminal for a producer
// waiting on completion. `rendered` is deliberately excluded (see the package
// note above).
func IsTerminalState(state State) bool {
	switch state {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

// WaitTerminal blocks until the job reaches a terminal state, preferring the
// event-driven server-side long poll and falling back to polling Wait when the
// queue has no wait route. A job that does not exist fails immediately with
// ErrNotFound; a cancelled context fails with the context error.
func (c *Client) WaitTerminal(ctx context.Context, id string) (Job, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Job{}, err
		}
		job, err := c.waitRound(ctx, id)
		switch {
		case errors.Is(err, errWaitRouteUnsupported):
			return c.Wait(ctx, id, waitFallbackInterval)
		case err != nil:
			// A long-poll request can fail transiently while the job itself is
			// still healthy (for example, when the queue is under load and the
			// wait handler misses its response-header deadline). Do not fail the
			// producer run and orphan a render that is still executing. The
			// status endpoint is independent of the parked wait request, so use
			// the established polling path for the rest of this wait.
			if ctx.Err() != nil {
				return Job{}, ctx.Err()
			}
			if errors.Is(err, ErrNotFound) {
				return Job{}, err
			}
			return c.Wait(ctx, id, waitFallbackInterval)
		}
		if IsTerminalState(job.State) {
			return job, nil
		}
		// The bounded wait elapsed without a terminal state: poll again so a
		// missed wake-up or a cross-replica transition can never stall us.
	}
}

// waitRound performs one bounded server-side long poll. A 404 on the wait route
// is ambiguous: it means either the job is gone or the route does not exist.
// The job lookup disambiguates, so an older queue degrades to polling while an
// unknown job still fails with ErrNotFound.
func (c *Client) waitRound(ctx context.Context, id string) (Job, error) {
	target := c.baseURL + "/jobs/" + url.PathEscape(id) + "/wait?max_wait_ms=" +
		strconv.FormatInt(waitRoundWindow.Milliseconds(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Job{}, fmt.Errorf("queue wait request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Job{}, fmt.Errorf("queue wait do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		if _, err := c.Get(ctx, id); err != nil {
			return Job{}, err
		}
		return Job{}, errWaitRouteUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return Job{}, fmt.Errorf("queue wait: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return Job{}, fmt.Errorf("queue wait decode: %w", err)
	}
	return job, nil
}

// Wait polls Get until the job reaches a terminal state. It retains the
// polling API for compatibility but uses exponential backoff, avoiding a
// fixed multi-second latency for short jobs while limiting request pressure.
func (c *Client) Wait(ctx context.Context, id string, interval time.Duration) (Job, error) {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	if interval > time.Second {
		interval = time.Second
	}
	backoff := interval
	backoffTimer := time.NewTimer(backoff)
	defer backoffTimer.Stop()
	for {
		job, err := c.Get(ctx, id)
		if err == nil && IsTerminalState(job.State) {
			return job, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Job{}, err
		}
		select {
		case <-ctx.Done():
			return Job{}, ctx.Err()
		case <-backoffTimer.C:
		}
		if backoff < time.Second {
			backoff *= 2
			if backoff > time.Second {
				backoff = time.Second
			}
		}
		backoffTimer.Reset(backoff)
	}
}
