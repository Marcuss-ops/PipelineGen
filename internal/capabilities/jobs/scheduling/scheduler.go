// Package scheduling — scheduler.go: the canonical deferred-job scheduler.
//
// The scheduler owns ONE job: turning due SCHEDULED rows into claimable
// QUEUED work without ever exceeding the admission policy. It is the only
// writer of the promotion transition; workers remain the only claimers of
// QUEUED rows, so scheduling and execution never race.
//
// Loop shape: promote everything due that the policy admits, then sleep
// until the earliest next run_at (bounded by MaxIdle). When a tick leaves
// due-but-blocked jobs behind — quota spent or the concurrency lane full —
// it re-checks at the base cadence instead of spinning on the due row.
package scheduling

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// DayKey is the canonical UTC day bucket for the promotion counter.
func DayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// SchedulerDeps is the typed construction input. Store and Policy are
// mandatory; the rest fall back to canonical defaults.
type SchedulerDeps struct {
	// Store is the scheduling persistence port (job.ScheduleStore).
	// MANDATORY.
	Store job.ScheduleStore
	// Policy bounds promotion (daily quota + concurrency). The zero value
	// is unlimited.
	Policy AdmissionPolicy
	// Interval is the base cadence used when due work is blocked. Default 30s.
	Interval time.Duration
	// MaxIdle caps how long the loop sleeps when the next run_at is far away.
	// Default 5m so a newly scheduled job is noticed promptly on clock skew.
	MaxIdle time.Duration
	// BatchLimit caps how many due rows one tick examines. Default 200.
	BatchLimit int
	// Now is the clock seam. Default time.Now.
	Now func() time.Time
	// Log is the structured logger. Default zap.NewNop().
	Log *zap.Logger
}

// Scheduler promotes due scheduled jobs into the queue.
type Scheduler struct {
	store    job.ScheduleStore
	policy   AdmissionPolicy
	interval time.Duration
	maxIdle  time.Duration
	batch    int
	now      func() time.Time
	log      *zap.Logger
}

// NewScheduler validates the dependencies and applies the defaults.
func NewScheduler(deps SchedulerDeps) (*Scheduler, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("scheduler: store is required")
	}
	if err := deps.Policy.Validate(); err != nil {
		return nil, err
	}
	interval := deps.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	maxIdle := deps.MaxIdle
	if maxIdle <= 0 {
		maxIdle = 5 * time.Minute
	}
	batch := deps.BatchLimit
	if batch <= 0 {
		batch = 200
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}
	return &Scheduler{
		store:    deps.Store,
		policy:   deps.Policy,
		interval: interval,
		maxIdle:  maxIdle,
		batch:    batch,
		now:      now,
		log:      log,
	}, nil
}

// PromotionResult summarises one tick.
type PromotionResult struct {
	// Due is how many schedules were due at tick time.
	Due int
	// Promoted is how many moved SCHEDULED → QUEUED.
	Promoted int
	// Deferred is how many were due but blocked by the admission policy.
	Deferred int
	// Skipped is how many lost the promotion CAS (cancelled / already moved).
	Skipped int
}

// Tick performs one promotion pass. It is exported so tests can drive the
// scheduler deterministically without a clock.
func (s *Scheduler) Tick(ctx context.Context) (PromotionResult, error) {
	var res PromotionResult
	if s == nil || s.store == nil {
		return res, fmt.Errorf("scheduler: not initialised")
	}

	now := s.now()
	due, err := s.store.ListDueSchedules(ctx, now, s.batch)
	if err != nil {
		return res, fmt.Errorf("scheduler: list due: %w", err)
	}
	res.Due = len(due)
	if len(due) == 0 {
		return res, nil
	}

	slots := s.policy.Slots(0, 0) // unbounded unless a ceiling is configured
	if !s.policy.Unbounded() {
		active, err := s.store.CountActiveJobs(ctx)
		if err != nil {
			return res, fmt.Errorf("scheduler: count active: %w", err)
		}
		promotedToday := 0
		if s.policy.DailyQuota > 0 {
			promotedToday, err = s.store.ScheduledPromotions(ctx, DayKey(now))
			if err != nil {
				return res, fmt.Errorf("scheduler: read daily counter: %w", err)
			}
		}
		slots = s.policy.Slots(active, promotedToday)
	}

	day := DayKey(now)
	for i, sched := range due {
		if slots <= 0 {
			res.Deferred += len(due) - i
			break
		}
		if s.policy.DailyQuota > 0 {
			granted, err := s.store.ReserveDailyPromotion(ctx, day, s.policy.DailyQuota)
			if err != nil {
				return res, fmt.Errorf("scheduler: reserve daily quota: %w", err)
			}
			if !granted {
				// Quota spent: everything remaining rolls into the next day.
				res.Deferred += len(due) - i
				break
			}
		}
		promoted, err := s.store.PromoteScheduled(ctx, sched.JobID, now)
		if err != nil {
			return res, fmt.Errorf("scheduler: promote %s: %w", sched.JobID, err)
		}
		if promoted {
			res.Promoted++
			slots--
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

// Run drives Tick until ctx is cancelled. It never returns an error for a
// transient store failure: a failed tick is logged and retried at the base
// cadence, so a momentary database lock cannot stop scheduling forever.
func (s *Scheduler) Run(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("scheduler: nil receiver")
	}
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}

		res, err := s.Tick(ctx)
		if err != nil {
			s.log.Warn("job scheduler tick failed", zap.Error(err))
		} else if res.Promoted > 0 || res.Deferred > 0 {
			s.log.Info("scheduled job promotion",
				zap.Int("due", res.Due),
				zap.Int("promoted", res.Promoted),
				zap.Int("deferred", res.Deferred),
				zap.Int("skipped", res.Skipped))
		}
		timer.Reset(s.nextDelay(ctx, res))
	}
}

// nextDelay returns how long to sleep before the next tick. Blocked work
// (Deferred) retries at the base cadence; otherwise the loop sleeps until
// the earliest run_at, bounded by MaxIdle.
func (s *Scheduler) nextDelay(ctx context.Context, res PromotionResult) time.Duration {
	delay := s.interval
	if res.Deferred > 0 {
		return delay
	}
	next, err := s.store.NextScheduledAt(ctx)
	if err != nil || next == nil {
		return delay
	}
	d := next.Sub(s.now())
	if d <= 0 {
		return delay
	}
	if d > s.maxIdle {
		return s.maxIdle
	}
	return d
}
