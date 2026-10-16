package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// Register upserts a worker's identity and records its initial heartbeat. The
// context is accepted for interface parity and ignored: the store never blocks
// (see the package doc).
func (s *Repository) Register(_ context.Context, worker model.Worker) error {
	if worker.ID == "" {
		return fmt.Errorf("worker id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	existing, ok := s.workers[worker.ID]
	if !ok {
		existing = worker
		existing.StartedAt = now
	} else {
		// Refresh identity fields but preserve the original start time.
		existing.Hostname = worker.Hostname
		existing.Status = worker.Status
		existing.RenderingGenVersion = worker.RenderingGenVersion
		existing.ChrononVersion = worker.ChrononVersion
		existing.OverlaySchemaVersion = worker.OverlaySchemaVersion
		existing.GPUBackend = worker.GPUBackend
		existing.GPUDevice = worker.GPUDevice
		existing.GPUDriver = worker.GPUDriver
	}
	existing.LastHeartbeatAt = now
	s.workers[worker.ID] = existing
	return nil
}

// Heartbeat records a heartbeat for a registered worker.
func (s *Repository) Heartbeat(_ context.Context, workerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[workerID]
	if !ok {
		return fmt.Errorf("worker %s is not registered", workerID)
	}
	worker.LastHeartbeatAt = time.Now()
	s.workers[workerID] = worker
	return nil
}

// List returns all registered workers sorted by ID.
func (s *Repository) List(_ context.Context) ([]model.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]model.Worker, 0, len(s.workers))
	for _, w := range s.workers {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// There is deliberately no Health method here: the aggregate is DERIVED from
// List() by the single authority in internal/model (see the WorkerRepository
// contract). A backend that classified liveness itself would be a second answer
// to "is this worker alive?".
