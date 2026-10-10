package cliprender

import (
	"context"
	"fmt"
	"sync"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// benchJobsService is the durable job surface the real ParentAggregator reads.
// Formerly owned by the deleted bench_harness_test.go (1184 lines); only the
// fake kept, because aggregator_finalize_test.go uses it. The throughput
// harness itself (benchRemote, scenarios, reports) is gone.
type benchJobsService struct {
	mu          sync.Mutex
	parents     map[string]*job.Job
	children    map[string]*job.Job
	finalizedAt map[string]time.Time
	finalized   map[string]job.Status
	calls       int
}

func newBenchJobsService() *benchJobsService {
	return &benchJobsService{
		parents:     map[string]*job.Job{},
		children:    map[string]*job.Job{},
		finalizedAt: map[string]time.Time{},
		finalized:   map[string]job.Status{},
	}
}

func (s *benchJobsService) Register(parent *job.Job, childID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parents[parent.ID] = parent
	s.children[childID] = &job.Job{ID: childID, Type: TypeClipRender, Status: job.StatusRunning}
}

func (s *benchJobsService) CompleteChild(childID string, status job.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.children[childID]; ok {
		c.Status = status
	}
}

func (s *benchJobsService) Get(_ context.Context, id string) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.children[id]; ok {
		clone := *c
		return &clone, nil
	}
	if p, ok := s.parents[id]; ok {
		clone := *p
		return &clone, nil
	}
	return nil, fmt.Errorf("bench jobs service: unknown job %s", id)
}

func (s *benchJobsService) ListAwaitingAggregation(_ context.Context, _ string, _ int) ([]job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]job.Job, 0, len(s.parents))
	for id, p := range s.parents {
		if _, done := s.finalizedAt[id]; done {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

func (s *benchJobsService) FinalizeAggregateParent(_ context.Context, id string, status job.Status, _ map[string]any, _ string, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.finalizedAt[id] = time.Now()
	s.finalized[id] = status
	return nil
}
