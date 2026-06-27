package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// LeaseReaper is the local scanning-port for periodic lease reaping.
//
// Declared in scanner.go (NOT in the canonical job.JobBroker interface) so
// the reaper injection doesn't widen the canonical Store surface. PR-Reaper
// (ADR-0002 §D6.1, June 2026) abstraction refinement: bound to the concrete
// *SQLiteStore would force the future *pgbroker.Store to implement
// RequeueExpiredLeases with the exact same shape; if the postgres adapter
// cannot (e.g. SKIP LOCKED-style claim semantics are different), the reaper
// MUST declare a per-adapter struct rather than fake-availability-stub the
// method (per godlike/07 "no fake availability").
//
// Signature mirrors the SQLiteStore public method exactly so the existing
// `*SQLiteStore` satisfies LeaseReaper implicitly with no adapter changes
// at the current call site (`internal/app/lifecycle.go::NewScanner(jobsRepo, log)`).
type LeaseReaper interface {
	RequeueExpiredLeases(ctx context.Context, now time.Time, limit int) ([]RequeueResult, error)
}

type Scanner struct {
	reaper LeaseReaper
	log    *zap.Logger
}

// NewScanner constructs the periodic lease-reaper Scanner. The reaper port
// is the local LeaseReaper interface (NOT the concrete *SQLiteStore) —
// so the canonical job.JobBroker surface stays untouched and the future
// postgres adapter can plug its own reaper in without recompiling the
// application layer (PR-Reaper / D6.1, June 2026).
func NewScanner(reaper LeaseReaper, log *zap.Logger) *Scanner {
	return &Scanner{reaper: reaper, log: log}
}

// RequeueExpiredLeases is the scanner-tick entry point. It calls the
// underlying reaper (the LeaseReaper) with a fixed limit of 1000
// (preserves the prior `RequeueExpiredLeasesNoArg` default).
// PR-Reaper switched from `RequeueExpiredLeasesNoArg` (now removed; orphan)
// to the canonical `RequeueExpiredLeases(ctx, now, limit)` call.
func (s *Scanner) RequeueExpiredLeases(ctx context.Context) error {
	if _, err := s.reaper.RequeueExpiredLeases(ctx, time.Now(), 1000); err != nil {
		s.log.Error("failed to requeue expired leases", zap.Error(err))
		return err
	}
	return nil
}

// Start runs the scanner in a tick loop until ctx is cancelled. The
// underlying reaper is invoked once per interval; ctx cancellation
// surfaces via the next RequeueExpiredLeases call (which propagates
// the cancellation). Production interval is 5 minutes (see
// internal/app/lifecycle.go::startBackgroundJobs).
func (s *Scanner) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.log.Info("starting lease scanner", zap.Duration("interval", interval))

	for {
		select {
		case <-ctx.Done():
			s.log.Info("stopping lease scanner")
			return
		case <-ticker.C:
			if err := s.RequeueExpiredLeases(ctx); err != nil {
				s.log.Error("lease scan failed", zap.Error(err))
			}
		}
	}
}
