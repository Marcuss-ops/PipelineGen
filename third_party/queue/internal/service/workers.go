package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// RegisterWorker upserts a worker's identity and records its initial
// heartbeat, then refreshes the worker-health metrics.
func (s *Service) RegisterWorker(ctx context.Context, worker model.Worker) error {
	if s.workerRepo == nil {
		return fmt.Errorf("worker repository is not configured")
	}
	if err := s.workerRepo.Register(ctx, worker); err != nil {
		return err
	}
	s.observeWorkerHealth(ctx)
	return nil
}

// WorkerHeartbeat records a heartbeat for a registered worker, then refreshes
// the worker-health metrics.
func (s *Service) WorkerHeartbeat(ctx context.Context, workerID string) error {
	if s.workerRepo == nil {
		return fmt.Errorf("worker repository is not configured")
	}
	if err := s.workerRepo.Heartbeat(ctx, workerID); err != nil {
		return err
	}
	s.observeWorkerHealth(ctx)
	return nil
}

// ListWorkers returns all registered workers, each annotated with its DERIVED
// liveness.
//
// The annotation happens here, not in the store: the store returns rows, and
// \"which of these is actually alive?\" is one classification (model) applied to
// them. Without it the endpoint served the status the worker last REPORTED, so a
// frozen worker (all threads stopped, heartbeat hours old) was listed as
// `status: ready` — the exact reading a benchmark or a certification preflight
// would trust.
func (s *Service) ListWorkers(ctx context.Context) ([]model.Worker, error) {
	if s.workerRepo == nil {
		return nil, fmt.Errorf("worker repository is not configured")
	}
	workers, err := s.workerRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	boundaries := s.livenessWindow()
	now := s.now()
	for i := range workers {
		workers[i] = model.WithLiveness(workers[i], boundaries, now)
	}
	return workers, nil
}

// WorkerHealth returns the aggregate worker-health snapshot, derived from the
// SAME rows and the same boundaries as ListWorkers.
func (s *Service) WorkerHealth(ctx context.Context) (model.WorkerHealth, error) {
	if s.workerRepo == nil {
		return model.WorkerHealth{}, fmt.Errorf("worker repository is not configured")
	}
	workers, err := s.workerRepo.List(ctx)
	if err != nil {
		return model.WorkerHealth{}, err
	}
	return model.SummarizeWorkerHealth(workers, s.livenessWindow(), s.now()), nil
}

// ReadyWorkers returns the ids of the workers that are READY right now.
//
// It is the preflight primitive: \"can this fleet run a job?\" is answered by a
// live worker, not by a registered row. Kept on the service (rather than left to
// each caller to compute from a /workers body) so the answer uses the same
// boundaries as the listing it came from.
func (s *Service) ReadyWorkers(ctx context.Context) ([]string, error) {
	if s.workerRepo == nil {
		return nil, fmt.Errorf("worker repository is not configured")
	}
	workers, err := s.workerRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	return model.ReadyWorkerIDs(workers, s.livenessWindow(), s.now()), nil
}

// RefreshWorkerHealth recomputes the worker-health metrics from the current
// registry snapshot. It is called on register/heartbeat and periodically by
// the server so the ready/offline gauges decay as heartbeats age.
func (s *Service) RefreshWorkerHealth(ctx context.Context) {
	s.observeWorkerHealth(ctx)
}

// livenessWindow resolves the heartbeat-age boundaries for the configured
// staleness window. One call per classification pass, so every worker in a
// snapshot is judged against the same clock.
func (s *Service) livenessWindow() model.WorkerLivenessBoundaries {
	return model.WorkerLivenessWindow(s.staleAfter)
}

// now reads the service clock. It is a seam because worker liveness is a
// function of elapsed time: a test cannot reproduce a frozen worker by waiting
// out a 90-second window, and a worker that stopped hours ago must be
// reproducible exactly, not approximately.
func (s *Service) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// SetLivenessClock injects the clock used to classify worker liveness. It
// affects ONLY the liveness classification (ListWorkers / WorkerHealth /
// ReadyWorkers); nothing else in the service reads it. Production never calls
// it — time.Now() is the clock.
func (s *Service) SetLivenessClock(clock func() time.Time) {
	s.clock = clock
}

func (s *Service) observeWorkerHealth(ctx context.Context) {
	if s.workerRepo == nil || s.metrics == nil {
		return
	}
	workers, err := s.workerRepo.List(ctx)
	if err != nil {
		log.Printf("worker health query failed: %v", err)
		// Do not freeze gauges at stale values: on query failure the last
		// ready/offline snapshot is no longer trustworthy for autoscaling.
		// Reset to 0 so dashboards do not show phantom ready workers while
		// the DB is unreachable.
		s.metrics.WorkersReady.Set(0)
		s.metrics.WorkersDegraded.Set(0)
		s.metrics.WorkersStale.Set(0)
		s.metrics.WorkersOffline.Set(0)
		return
	}
	h := model.SummarizeWorkerHealth(workers, s.livenessWindow(), s.now())
	s.metrics.WorkersReady.Set(float64(h.Ready))
	s.metrics.WorkersDegraded.Set(float64(h.Degraded))
	s.metrics.WorkersStale.Set(float64(h.Stale))
	s.metrics.WorkersOffline.Set(float64(h.Offline))
}
