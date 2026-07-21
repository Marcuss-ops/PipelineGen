// Package mediamemory (sqlite infrastructure) — usage_repository.go
// is the canonical concrete impl of mediamemory.UsageRepository.
//
// godlike/06 SSOT: the SQL ↔ Go row conversion for media_usage_events
// lives ONLY here. DDL canonical home:
// migrations/sqlite/167_mediamemory_usage_events.sql. Wire canonical
// home: internal/application/mediamemory/types.go::UsageEvent.
// This file is the bridge.
//
// godlike/07 NO-FAKE-AVAILABILITY: the table is append-only
// (Append-only audit log per godlike/07 Compliance; we expose
// ONLY Append at the port — no Update or Delete). This is the
// canonical choice so the ranker can replay the exact sequence
// of accept/reject/replace events and recompute SuccessScore
// deterministically on warm-up.
package mediamemory

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// usageRepository is the canonical concrete
// mediamemory.UsageRepository backed by SQLite.
type usageRepository struct {
	db *sql.DB
}

// NewUsageRepository constructs the canonical repository.
func NewUsageRepository(db *sql.DB) mediamemory.UsageRepository {
	return &usageRepository{db: db}
}

// Compile-time assertion.
var _ mediamemory.UsageRepository = (*usageRepository)(nil)

const usageSelectColumns = `id, project_id, scene_id, concept_id,
		asset_id, binding_id, slot_kind,
		selected, manually_selected, rejected, render_completed,
		created_at`

// Append is the canonical entrypoint. The port signature is
// `Append(ctx, ev) error` per godlike/06 SSOT — an append-only
// audit log need not return the persisted row (the caller
// already supplied every field). NO UPDATE / NO DELETE is
// exposed — see the file-level godlike/07 SSOT comment above.
//
// The server-assigned ID + CreatedAt are written to the row;
// callers that need to read them back call ListByAsset or
// ListByConcept (no silent side-channel return path).
func (r *usageRepository) Append(ctx context.Context, ev mediamemory.UsageEvent) error {
	if !mediamemory.IsKnownSlotKind(ev.SlotKind) {
		return fmt.Errorf(
			"mediamemory: usage event slot_kind=%q: %w",
			string(ev.SlotKind), mediamemory.ErrInvalidSlotKind,
		)
	}
	now := time.Now().UTC()
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = now
	}
	q := `INSERT INTO media_usage_events
		(` + usageSelectColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.db.ExecContext(ctx, q,
		ev.ID, ev.ProjectID, ev.SceneID, ev.ConceptID,
		ev.AssetID, ev.BindingID, string(ev.SlotKind),
		boolToInt(ev.Selected), boolToInt(ev.ManuallySelected),
		boolToInt(ev.Rejected), boolToInt(ev.RenderCompleted),
		ev.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("mediamemory: append usage event: %w", err)
	}
	return nil
}

// ListByConcept is the ranker warm-up read (NO UPDATE; the
// FeedbackService writes UsageEvents via Append and the ranker
// recomputes SuccessScore in-memory).
func (r *usageRepository) ListByConcept(ctx context.Context, conceptID string, limit int) ([]mediamemory.UsageEvent, error) {
	args := []any{conceptID}
	q := `SELECT ` + usageSelectColumns + ` FROM media_usage_events WHERE concept_id = ? ORDER BY created_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.queryUsageEvents(ctx, q, args, "list by concept")
}

// ListByAsset returns every event touching the given asset_id.
func (r *usageRepository) ListByAsset(ctx context.Context, assetID string, limit int) ([]mediamemory.UsageEvent, error) {
	args := []any{assetID}
	q := `SELECT ` + usageSelectColumns + ` FROM media_usage_events WHERE asset_id = ? ORDER BY created_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.queryUsageEvents(ctx, q, args, "list by asset")
}

// ── Helpers ──────────────────────────────────────────────────

func (r *usageRepository) queryUsageEvents(ctx context.Context, q string, args []any, op string) ([]mediamemory.UsageEvent, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: %s: %w", op, err)
	}
	defer rows.Close()
	out := make([]mediamemory.UsageEvent, 0, 32)
	for rows.Next() {
		ev, err := scanUsageEventRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mediamemory: %s iterate: %w", op, err)
	}
	return out, nil
}

func scanUsageEventRow(s rowScanner) (mediamemory.UsageEvent, error) {
	var (
		ev               mediamemory.UsageEvent
		slotKind         string
		selected         int
		manuallySelected int
		rejected         int
		renderCompleted  int
		createdAt        string
	)
	if err := s.Scan(
		&ev.ID, &ev.ProjectID, &ev.SceneID, &ev.ConceptID,
		&ev.AssetID, &ev.BindingID, &slotKind,
		&selected, &manuallySelected, &rejected, &renderCompleted,
		&createdAt,
	); err != nil {
		return mediamemory.UsageEvent{}, err
	}
	ev.SlotKind = mediamemory.SlotKind(slotKind)
	if !mediamemory.IsKnownSlotKind(ev.SlotKind) {
		return mediamemory.UsageEvent{}, fmt.Errorf(
			"mediamemory: usage event %q has unknown slot_kind %q",
			ev.ID, slotKind,
		)
	}
	ev.Selected = selected != 0
	ev.ManuallySelected = manuallySelected != 0
	ev.Rejected = rejected != 0
	ev.RenderCompleted = renderCompleted != 0
	t, err := parseTime(createdAt)
	if err != nil {
		return mediamemory.UsageEvent{}, fmt.Errorf("mediamemory: usage event %q created_at: %w", ev.ID, err)
	}
	ev.CreatedAt = t
	return ev, nil
}
