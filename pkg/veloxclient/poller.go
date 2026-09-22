// poller.go — the ONE job poller for every async surface.
//
// The repo's operating rules prescribe a single poller for all jobs, but the
// client only had GetJobStatus (a one-shot read) plus RouteJobsFull, so every
// caller that needed "wait until terminal" grew its own loop: different
// intervals, inconsistent terminal vocabularies, and no shared timeout or
// cancellation. WaitJob is that loop, once.
//
// Contract:
//   - polls GET /api/jobs/{id}/full (RouteJobsFull);
//   - treats the server's BOTH vocabularies as terminal
//     (SUCCEEDED/FAILED uppercase worker states and queued/running/completed
//     lowercase states) via IsTerminalStatus, so a caller never compares raw
//     strings;
//   - returns the final JobStatusResponse on success;
//   - returns (response, ErrJobFailed-wrapped error) on a terminal FAILURE so
//     the caller still has the error/progress payload;
//   - fails with ErrPollTimeout when the deadline passes without a terminal
//     state, and returns ctx.Err() when the caller cancels;
//   - a not-yet-visible job (404 while a job row propagates) is retried until
//     the deadline instead of being reported as missing.
package veloxclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Default polling policy: 2s initial interval growing 1.5x to 15s, 30m cap.
// The cap is generous because a stock run or a batch clip extraction can
// legitimately take many minutes; callers that want a tighter bound pass
// WithPollTimeout.
const (
	DefaultPollInterval    = 2 * time.Second
	DefaultPollMaxInterval = 15 * time.Second
	DefaultPollTimeout     = 30 * time.Minute
	PollBackoffFactor      = 1.5
)

var (
	// ErrJobFailed wraps a terminal FAILED/CANCELLED/DEAD job. The final
	// JobStatusResponse is returned alongside it so the caller can read the
	// server's error and partial result.
	ErrJobFailed = errors.New("veloxclient: job reached a terminal failure state")
	// ErrPollTimeout is returned when the deadline passes with the job still
	// in a non-terminal state.
	ErrPollTimeout = errors.New("veloxclient: timed out waiting for terminal job state")
)

// PollOptions configures WaitJob.
type PollOptions struct {
	// Interval is the first wait between polls (DefaultPollInterval).
	Interval time.Duration
	// MaxInterval caps the exponential growth (DefaultPollMaxInterval).
	MaxInterval time.Duration
	// Timeout bounds the whole wait (DefaultPollTimeout).
	Timeout time.Duration
	// OnPoll, when set, is called with every observed status (including the
	// terminal one). Use it for progress logging; it must not block.
	OnPoll func(*JobStatusResponse)
}

// PollOption mutates PollOptions.
type PollOption func(*PollOptions)

// WithPollInterval sets the initial interval.
func WithPollInterval(d time.Duration) PollOption {
	return func(o *PollOptions) {
		if d > 0 {
			o.Interval = d
		}
	}
}

// WithPollMaxInterval caps the backoff interval.
func WithPollMaxInterval(d time.Duration) PollOption {
	return func(o *PollOptions) {
		if d > 0 {
			o.MaxInterval = d
		}
	}
}

// WithPollTimeout bounds the total wait.
func WithPollTimeout(d time.Duration) PollOption {
	return func(o *PollOptions) {
		if d > 0 {
			o.Timeout = d
		}
	}
}

// WithPollObserver registers a per-poll callback.
func WithPollObserver(fn func(*JobStatusResponse)) PollOption {
	return func(o *PollOptions) {
		if fn != nil {
			o.OnPoll = fn
		}
	}
}

// WaitJob polls GET /api/jobs/{id}/full until the job reaches a terminal state
// or the deadline/context ends. On a successful terminal state it returns
// (response, nil); on a terminal failure it returns (response, error wrapping
// ErrJobFailed); on timeout it returns (nil, ErrPollTimeout).
func (c *Client) WaitJob(ctx context.Context, jobID string, opts ...PollOption) (*JobStatusResponse, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("veloxclient: WaitJob requires a non-empty jobID")
	}
	cfg := PollOptions{
		Interval:    DefaultPollInterval,
		MaxInterval: DefaultPollMaxInterval,
		Timeout:     DefaultPollTimeout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	interval := cfg.Interval
	var lastErr error
	for {
		resp, err := c.GetJobStatus(ctx, jobID)
		if err != nil {
			// A 404 right after submit means the row has not propagated yet;
			// keep polling. Any other error is recorded and also retried — a
			// transient 5xx must not abort a legitimate wait.
			lastErr = err
		} else {
			lastErr = nil
			if cfg.OnPoll != nil {
				cfg.OnPoll(resp)
			}
			if terminal, success := IsTerminalStatus(resp.Status); terminal {
				if success {
					return resp, nil
				}
				return resp, fmt.Errorf("%w: id=%s status=%s error=%s", ErrJobFailed, resp.ID, resp.Status, resp.Error)
			}
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				if lastErr != nil {
					return nil, fmt.Errorf("%w: last error: %v", ErrPollTimeout, lastErr)
				}
				return nil, ErrPollTimeout
			}
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		if next := time.Duration(float64(interval) * PollBackoffFactor); next > cfg.MaxInterval {
			interval = cfg.MaxInterval
		} else {
			interval = next
		}
	}
}
