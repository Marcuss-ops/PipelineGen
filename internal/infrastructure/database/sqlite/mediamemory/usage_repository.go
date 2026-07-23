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
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
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
		channel_id, video_id,
		selected, manually_selected, rejected, render_completed,
		created_at`

// usageInsertColumnAliases keeps the INSERT placeholder ? count
// in lock-step with usageSelectColumns (godlike/06 SSOT: a drift
// between SELECT and INSERT is a silent runtime fault).
const usageInsertColumnCount = 14

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
	if !media.IsKnownSlotKind(ev.SlotKind) {
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
	// godlike/06 SSOT (Fase 2.3 anti-repetition): channel_id +
	// video_id flow verbatim into the row, mirroring the
	// SELECT column order above. Nullable for legacy rows
	// pre-migration-169 so a back-fill is unnecessary — the
	// ranker treats empty strings as "no penalty input available"
	// but the same-asset penalty (UsageCount + SuccessScore)
	// still drives the contract.
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	q := `INSERT INTO media_usage_events
		(` + usageSelectColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.db.ExecContext(ctx, q,
		ev.ID, ev.ProjectID, ev.SceneID, ev.ConceptID,
		ev.AssetID, ev.BindingID, string(ev.SlotKind),
		nullable(ev.ChannelID), nullable(ev.VideoID),
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

// ListProjectUsages is the Fase 2.3 anti-repetition read seam.
//
// godlike/06 SSOT (denormalized identity): the SELECT enumerates
// every column including channel_id + video_id (migration 169) so
// the resolver's PopulateRepetitionPenalty has full identity to
// work with (no media_assets join). The result set is bounded by
// `limit` (canonical upper bound at the resolver is
// AntiRepetitionHistoryLimit = 1000) so unbounded project scans
// cannot blow up at runtime.
func (r *usageRepository) ListProjectUsages(ctx context.Context, projectID string, limit int) ([]mediamemory.UsageEvent, error) {
	args := []any{projectID}
	q := `SELECT ` + usageSelectColumns + ` FROM media_usage_events WHERE project_id = ? ORDER BY created_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.queryUsageEvents(ctx, q, args, "list by project")
}

// ListSince is the Fase 1.6 ranker warm-up read seam.
// godlike/06 SSOT (port-driven read): the canonical read is a
// single SQL query bounded by both `since` (lower bound on
// created_at) and `limit` (canonical upper bound for warm-up is
// AntiRepetitionHistoryLimit = 1000). The FeedbackService
// .AggregateSince helper groups the bounded slice by (concept, slot)
// in Go so the ranker can seed its initial score estimate without a
// runtime JOIN against media_bindings.
//
// A zero `since` is treated as "no lower bound" — the canonical
// post-deploy warm-up path reads every event newer-than-the-cutoff
// timestamp; a legacy zero-value call returns the most recent
// `limit` events so the warm-up can never silently drop rows.
func (r *usageRepository) ListSince(ctx context.Context, since time.Time, limit int) ([]mediamemory.UsageEvent, error) {
	var (
		args []any
		q    string
	)
	if since.IsZero() {
		// Zero-value since = "no lower bound"; canonical fallback
		// for post-deploy full warm-up. ORDER BY created_at DESC
		// so the most recent rows come first (ranker warm-up
		// priority is recency-weighted).
		q = `SELECT ` + usageSelectColumns + ` FROM media_usage_events ORDER BY created_at DESC`
	} else {
		args = append(args, since.UTC().Format(time.RFC3339Nano))
		q = `SELECT ` + usageSelectColumns + ` FROM media_usage_events WHERE created_at >= ? ORDER BY created_at DESC`
	}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.queryUsageEvents(ctx, q, args, "list since")
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
		channelID        sql.NullString
		videoID          sql.NullString
		selected         int
		manuallySelected int
		rejected         int
		renderCompleted  int
		createdAt        string
	)
	if err := s.Scan(
		&ev.ID, &ev.ProjectID, &ev.SceneID, &ev.ConceptID,
		&ev.AssetID, &ev.BindingID, &slotKind,
		&channelID, &videoID,
		&selected, &manuallySelected, &rejected, &renderCompleted,
		&createdAt,
	); err != nil {
		return mediamemory.UsageEvent{}, err
	}
	if channelID.Valid {
		ev.ChannelID = channelID.String
	}
	if videoID.Valid {
		ev.VideoID = videoID.String
	}
	ev.SlotKind = media.SlotKind(slotKind)
	if !media.IsKnownSlotKind(ev.SlotKind) {
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
