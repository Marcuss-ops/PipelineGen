// Package app — outbox monitor adapter (Wave 14 PR5, June 2026).
//
// outboxMonitorAdapter wraps *outboxevents.Repository to satisfy
// outbox.MonitorPort declared in
// internal/application/jobs/outbox/ports.go. AGENTS.md Pattern 0 says
// the composition root is the only place allowed to bridge a concrete
// infrastructure type to an application-layer port. The adapter
// translates the concrete *outboxevents.Event into the
// application-layer outbox.EventDTO at the infra seam so the api
// layer never imports outboxevents directly.
//
// Compile-time assertion: outboxMonitorAdapter satisfies
// outbox.MonitorPort. Future port drift surfaces at build time, not
// first runtime call.
package app

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// outboxMonitorAdapter bridges *outboxevents.Repository → outbox.MonitorPort.
type outboxMonitorAdapter struct {
	repo *outboxevents.Repository
}

// Compile-time assertion (AGENTS.md Pattern 0).
var _ outbox.MonitorPort = (*outboxMonitorAdapter)(nil)

// newOutboxMonitorAdapter wraps a concrete *outboxevents.Repository as
// an outbox.MonitorPort. nil-safe — a nil repo produces a nil port so
// the caller's nil check stays semantic ("port wired?").
func newOutboxMonitorAdapter(repo *outboxevents.Repository) outbox.MonitorPort {
	if repo == nil {
		return nil
	}
	return &outboxMonitorAdapter{repo: repo}
}

// CountByStatus delegates to the concrete repo.
func (a *outboxMonitorAdapter) CountByStatus(ctx context.Context, status string) (int64, error) {
	if a == nil || a.repo == nil {
		return 0, nil
	}
	return a.repo.CountByStatus(ctx, status)
}

// ListPending delegates then converts []outboxevents.Event → []outbox.EventDTO.
// Field-by-field copy: JSON keys are identical because both Event and
// EventDTO use the exported PascalCase field names with no `json:`
// struct tags, and the time fields stay as *time.Time (matching the
// pointer-vs-string semantics the operator dashboard parses).
func (a *outboxMonitorAdapter) ListPending(ctx context.Context) ([]outbox.EventDTO, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	events, err := a.repo.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	dtos := make([]outbox.EventDTO, len(events))
	for i, e := range events {
		dtos[i] = outbox.EventDTO{
			ID:            e.ID,
			EventType:     e.EventType,
			AggregateID:   e.AggregateID,
			AggregateType: e.AggregateType,
			PayloadJSON:   e.PayloadJSON,
			Status:        e.Status,
			AttemptCount:  e.AttemptCount,
			MaxAttempts:   e.MaxAttempts,
			LastError:     e.LastError,
			EventKey:      e.EventKey,
			WorkerID:      e.WorkerID,
			LeaseID:       e.LeaseID,
			LeaseExpiry:   e.LeaseExpiry,
			CompletedAt:   e.CompletedAt,
			CreatedAt:     e.CreatedAt,
			UpdatedAt:     e.UpdatedAt,
		}
	}
	return dtos, nil
}
