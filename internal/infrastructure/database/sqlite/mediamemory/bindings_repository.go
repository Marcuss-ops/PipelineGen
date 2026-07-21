// Package mediamemory (sqlite infrastructure) — bindings_repository.go
// is the canonical concrete impl of mediamemory.BindingRepository.
//
// godlike/06 SSOT (one canonical owner per fact): the SQL ↔ Go row
// conversion for media_bindings lives ONLY here. DDL SSOT:
// migrations/sqlite/164_mediamemory_bindings.sql. Application-layer
// wire SSOT: internal/application/mediamemory/types.go::MediaBinding.
// This file is the bridge.
//
// godlike/07 NO-FAKE-AVAILABILITY: UNIQUE(concept_id, asset_id,
// slot_kind) violations surface as wrapped ErrDuplicateBinding.
// Misses surface as wrapped ErrBindingNotFound. The caller
// branches via errors.Is.
//
// composition: depends ONLY on application-layer (godlike/06
// layering). No logger import; observability is wired at the
// composition root.
package mediamemory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// bindingsRepository is the canonical concrete
// mediamemory.BindingRepository backed by SQLite.
type bindingsRepository struct {
	db *sql.DB
}

// NewBindingsRepository constructs the canonical repository.
func NewBindingsRepository(db *sql.DB) mediamemory.BindingRepository {
	return &bindingsRepository{db: db}
}

// Compile-time assertion: bindingsRepository satisfies the
// canonical port. Drift is a build error.
var _ mediamemory.BindingRepository = (*bindingsRepository)(nil)

const bindingsSelectColumns = `id, concept_id, asset_id, start_ms, end_ms,
		slot_kind, origin, approval_status,
		manual_score, semantic_score, quality_score, success_score,
		usage_count, last_used_at, created_at, updated_at`

// Upsert validates the SlotKind against the canonical closed set
// BEFORE the SQL round-trip (godlike/06 SSOT: gate input on the
// boundary). Inserts a new binding or updates the existing row
// keyed by (concept_id, asset_id, slot_kind).
func (r *bindingsRepository) Upsert(ctx context.Context, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error) {
	if !mediamemory.IsKnownSlotKind(b.SlotKind) {
		return mediamemory.MediaBinding{}, fmt.Errorf(
			"mediamemory: binding slot_kind=%q: %w",
			string(b.SlotKind), mediamemory.ErrInvalidSlotKind,
		)
	}
	now := time.Now().UTC()
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}
	b.UpdatedAt = now

	q := `INSERT INTO media_bindings
		(` + bindingsSelectColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(concept_id, asset_id, slot_kind) DO UPDATE SET
			start_ms        = excluded.start_ms,
			end_ms          = excluded.end_ms,
			origin          = excluded.origin,
			approval_status = excluded.approval_status,
			manual_score    = excluded.manual_score,
			semantic_score  = excluded.semantic_score,
			quality_score   = excluded.quality_score,
			success_score   = excluded.success_score,
			usage_count     = excluded.usage_count,
			last_used_at    = excluded.last_used_at,
			updated_at      = excluded.updated_at
		RETURNING ` + bindingsSelectColumns

	row := r.db.QueryRowContext(ctx, q,
		b.ID, b.ConceptID, b.AssetID,
		b.StartMs, b.EndMs,
		string(b.SlotKind), string(b.Origin), string(b.ApprovalStatus),
		b.ManualScore, b.SemanticScore, b.QualityScore, b.SuccessScore,
		b.UsageCount, nullableTimePtr(b.LastUsedAt),
		b.CreatedAt.Format(time.RFC3339Nano), b.UpdatedAt.Format(time.RFC3339Nano),
	)
	return scanBindingRow(row)
}

// FindByID wraps ErrBindingNotFound when the row is missing.
func (r *bindingsRepository) FindByID(ctx context.Context, id string) (mediamemory.MediaBinding, error) {
	q := `SELECT ` + bindingsSelectColumns + ` FROM media_bindings WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	b, err := scanBindingRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mediamemory.MediaBinding{}, fmt.Errorf("mediamemory: find binding %q: %w", id, mediamemory.ErrBindingNotFound)
		}
		return mediamemory.MediaBinding{}, err
	}
	return b, nil
}

// ListApprovedByConcept is the resolver's Level 0 hot path:
// approved bindings only, ordered by SuccessScore desc (godlike/06
// SSOT: indexed idx_media_bindings_approved_slot +
// idx_media_bindings_success cover the read pattern).
//
// If slotKinds is non-empty, the query filters by the canonical
// closed set; if any slot kind is unknown, ErrInvalidSlotKind is
// returned BEFORE the SQL round-trip (godlike/07).
func (r *bindingsRepository) ListApprovedByConcept(ctx context.Context, conceptID string, slotKinds []mediamemory.SlotKind, limit int) ([]mediamemory.MediaBinding, error) {
	for _, sk := range slotKinds {
		if !mediamemory.IsKnownSlotKind(sk) {
			return nil, fmt.Errorf(
				"mediamemory: list approved slot_kind=%q: %w",
				string(sk), mediamemory.ErrInvalidSlotKind,
			)
		}
	}
	args := []any{conceptID, string(mediamemory.ApprovalApproved)}
	q := `SELECT ` + bindingsSelectColumns + `
		FROM media_bindings
		WHERE concept_id = ? AND approval_status = ?`
	if len(slotKinds) > 0 {
		placeholders := strings.Repeat("?,", len(slotKinds))
		placeholders = placeholders[:len(placeholders)-1]
		q += ` AND slot_kind IN (` + placeholders + `)`
		for _, sk := range slotKinds {
			args = append(args, string(sk))
		}
	}
	q += ` ORDER BY success_score DESC, manual_score DESC, updated_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: list approved bindings (concept=%q): %w", conceptID, err)
	}
	defer rows.Close()
	out := make([]mediamemory.MediaBinding, 0, 8)
	for rows.Next() {
		b, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mediamemory: list approved bindings iterate: %w", err)
	}
	return out, nil
}

// ListByAsset returns every binding that references an asset_id
// (used by anti-repetition on the same-source-clip check).
func (r *bindingsRepository) ListByAsset(ctx context.Context, assetID string) ([]mediamemory.MediaBinding, error) {
	args := []any{assetID}
	q := `SELECT ` + bindingsSelectColumns + ` FROM media_bindings WHERE asset_id = ?`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: list by asset %q: %w", assetID, err)
	}
	defer rows.Close()
	out := make([]mediamemory.MediaBinding, 0, 4)
	for rows.Next() {
		b, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mediamemory: list by asset iterate: %w", err)
	}
	return out, nil
}

// ListByConcept returns every binding (any status) for the
// concept, ordered by updated_at desc. Used by the dashboard's
// Visual Memory page (full diff view, including pending + rejected
// rows). NOT the resolver hot path (the resolver uses
// ListApprovedByConcept).
func (r *bindingsRepository) ListByConcept(ctx context.Context, conceptID string) ([]mediamemory.MediaBinding, error) {
	q := `SELECT ` + bindingsSelectColumns + `
		FROM media_bindings
		WHERE concept_id = ?
		ORDER BY updated_at DESC`
	rows, err := r.db.QueryContext(ctx, q, conceptID)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: list by concept %q: %w", conceptID, err)
	}
	defer rows.Close()
	out := make([]mediamemory.MediaBinding, 0, 8)
	for rows.Next() {
		b, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mediamemory: list by concept iterate: %w", err)
	}
	return out, nil
}

// Delete is provided for admin reindex flows. Not used by the
// resolver hot path.
func (r *bindingsRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM media_bindings WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("mediamemory: delete binding %q: %w", id, err)
	}
	return nil
}

// ── Row scanning (helper) ──────────────────────────────────

func scanBindingRow(s rowScanner) (mediamemory.MediaBinding, error) {

	var (
		b              mediamemory.MediaBinding
		startMs        sql.NullFloat64
		endMs          sql.NullFloat64
		slotKind       string
		origin         string
		approvalStatus string
		lastUsedAt     sql.NullString
		createdAt      string
		updatedAt      string
	)
	if err := s.Scan(
		&b.ID, &b.ConceptID, &b.AssetID,
		&startMs, &endMs,
		&slotKind, &origin, &approvalStatus,
		&b.ManualScore, &b.SemanticScore, &b.QualityScore, &b.SuccessScore,
		&b.UsageCount, &lastUsedAt,
		&createdAt, &updatedAt,
	); err != nil {
		return mediamemory.MediaBinding{}, err
	}
	b.SlotKind = mediamemory.SlotKind(slotKind)
	if !mediamemory.IsKnownSlotKind(b.SlotKind) {
		return mediamemory.MediaBinding{}, fmt.Errorf(
			"mediamemory: binding %q has unknown slot_kind %q",
			b.ID, slotKind,
		)
	}
	b.Origin = mediamemory.Origin(origin)
	if !mediamemory.IsKnownOrigin(b.Origin) {
		return mediamemory.MediaBinding{}, fmt.Errorf(
			"mediamemory: binding %q has unknown origin %q",
			b.ID, origin,
		)
	}
	b.ApprovalStatus = mediamemory.ApprovalStatus(approvalStatus)
	if !mediamemory.IsKnownApprovalStatus(b.ApprovalStatus) {
		return mediamemory.MediaBinding{}, fmt.Errorf(
			"mediamemory: binding %q has unknown approval_status %q",
			b.ID, approvalStatus,
		)
	}
	if startMs.Valid {
		b.StartMs = int64(startMs.Float64)
	}
	if endMs.Valid {
		b.EndMs = int64(endMs.Float64)
	}
	if lastUsedAt.Valid {
		t, err := parseTime(lastUsedAt.String)
		if err != nil {
			return mediamemory.MediaBinding{}, fmt.Errorf("mediamemory: binding %q last_used_at: %w", b.ID, err)
		}
		b.LastUsedAt = &t
	}
	var err error
	if b.CreatedAt, err = parseTime(createdAt); err != nil {
		return mediamemory.MediaBinding{}, fmt.Errorf("mediamemory: binding %q created_at: %w", b.ID, err)
	}
	if b.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return mediamemory.MediaBinding{}, fmt.Errorf("mediamemory: binding %q updated_at: %w", b.ID, err)
	}
	return b, nil
}

// ── Nullable helpers ───────────────────────────────────
//
// Shared helpers (parseTime, nullableString, nullableTimePtr,
// boolToInt, isUniqueViolation, rowScanner) live in helpers.go
// per godlike/06 SSOT; this file no longer redeclares them.
// (parseTime is consumed at the scanBindingRow call sites below.)
