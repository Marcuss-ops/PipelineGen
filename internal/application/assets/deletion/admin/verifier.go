// Package deletion/admin (TODO 5, QDRANT-002-B, June 2026) hosts the
// admin-only hard-delete path for media_assets. It is intentionally
// placed in a subpackage so the import path itself signals "not for
// production use" — only cmd/admin/** and operator-driven tooling can
// reach these handlers.
//
// Production HTTP callers MUST NOT import this package. CI gate
// scripts/ci-architectural-checks.sh::Check 5 blocks any handler in
// internal/api/** that imports internal/application/assets/deletion/admin.
package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AssetVerifier is the canonical pre-flight check for hard-delete
// eligibility. A hard delete is permitted IFF all three conditions
// hold:
//
//  1. media_assets.lifecycle_state = 'DELETE_PENDING' or 'DELETED'
//     (the row has been soft-deleted first via
//     outbox.Dispatcher.EnqueueAndDelete; verifyclip / clip_ops.go
//     and the public API go through this path).
//
//  2. The matching Qdrant point is absent (zero semantic vectors
//     reference the asset_id; otherwise a hard delete in SQLite
//     leaves orphan vectors recoverable only via a costly
//     /points/delete re-projection).
//
//  3. Zero pending outbox events reference the asset_id (no
//     in-flight indexing / delivery / sync work that would race
//     with the physical row drop; the IndexingHandler /
//     DeliveryHandler pool must be empty for this id).
//
// The verifier surface returns a single typed error so callers
// (admin CLI / Service) can branch on intent via errors.Is, and
// emits a structured reason string for the operator log.
//
// Future: when AssetVerifier is wired against the real AssetStore /
// Qdrant client, the production implementation will replace the
// stub fields below.
type AssetVerifier interface {
	Verify(ctx context.Context, assetID string) (*VerifyReport, error)
}

// VerifyReport is the structured outcome of AssetVerifier.Verify.
// `Eligible` is the gate; the booleans below explain WHICH condition
// is the blocker (so the admin CLI can tell the operator which
// follow-up step is needed before retrying).
type VerifyReport struct {
	Eligible bool

	LifecycleDELETED bool // lifecycle_state ∈ {DELETE_PENDING, DELETED}
	QdrantAbsent     bool // no Qdrant point with id=assetID
	OutboxNoPending  bool // count(outbox_events WHERE aggregate_id=assetID AND status='pending') == 0

	// OutboxPendingCount is supplied for operator-triage detail even
	// when the count is zero (idiomatic observability).
	OutboxPendingCount int64

	// RefusalReason is "" if Eligible=true; otherwise it carries
	// "hard delete refused: <condition>" for the admin CLI to log.
	RefusalReason string
}

// ErrAssetVerifier is the canonical refusal sentinel. Wrapped errors
// carrying a sub-reason are produced by the concrete verifier; the
// top-level cause is always ErrAssetVerifier so callers can switch on
// intent via errors.Is(err, admin.ErrAssetVerifier).
var ErrAssetVerifier = errors.New("hard delete refused")

// SqliteAssetVerifier is the concrete AssetVerifier implementation
// backed by the production media_assets + outbox_events SQLite
// tables. The Qdrant-side check is supplied as an injected function
// (AssetExistsInQdrant) so the production wiring can plug in the real
// qdrant.HasPoint helper while the test suite uses a stub.
//
// Concurrency: a single Verify call performs 3 SELECTs (lifecycle,
// outbox pendings, qdrant count via the func). Each SELECT is
// independent; an error in any one aborts the whole call with a
// wrapped error.
type SqliteAssetVerifier struct {
	DB                  *sql.DB
	AssetExistsInQdrant func(ctx context.Context, assetID string) (bool, error)
}

// Verify runs the 3-condition gate for the given assetID and returns
// a VerifyReport. The Report is always non-nil; error is non-nil
// only on infrastructure failures (DB query / Qdrant client error).
//
// Eligibility logic: all three booleans must be true. Any false
// condition triggers ErrAssetVerifier wrapping + RefusalReason
// stringification in the Report.
func (v *SqliteAssetVerifier) Verify(ctx context.Context, assetID string) (*VerifyReport, error) {
	if assetID == "" {
		return nil, fmt.Errorf("admin.SqliteAssetVerifier.Verify: assetID is required")
	}
	if v.DB == nil {
		return nil, fmt.Errorf("admin.SqliteAssetVerifier.Verify: DB is required")
	}

	rep := &VerifyReport{}

	// 1) lifecycle_state must be DELETE_PENDING or DELETED.
	var lifecycle string
	err := v.DB.QueryRowContext(ctx,
		`SELECT COALESCE(lifecycle_state, '') FROM media_assets WHERE id = ?`, assetID,
	).Scan(&lifecycle)
	if err != nil {
		if err == sql.ErrNoRows {
			rep.RefusalReason = "asset not found in media_assets"
			return rep, fmt.Errorf("%w: %s", ErrAssetVerifier, rep.RefusalReason)
		}
		return nil, fmt.Errorf("admin.SqliteAssetVerifier.Verify select lifecycle: %w", err)
	}
	rep.LifecycleDELETED = lifecycle == "DELETE_PENDING" || lifecycle == "DELETED"

	// 2) Qdrant point absent (delegated to injected func).
	if v.AssetExistsInQdrant != nil {
		exists, qerr := v.AssetExistsInQdrant(ctx, assetID)
		if qerr != nil {
			return nil, fmt.Errorf("admin.SqliteAssetVerifier.Verify qdrant existence: %w", qerr)
		}
		rep.QdrantAbsent = !exists
	} else {
		// Default conservative posture: if no Qdrant probe is wired,
		// fail closed. Production wiring will always supply a probe.
		rep.QdrantAbsent = false
		rep.RefusalReason = "Qdrant presence probe not wired (fail-closed default)"
	}

	// 3) Zero pending outbox events for this aggregate_id.
	if err := v.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ? AND status = 'pending'`,
		assetID,
	).Scan(&rep.OutboxPendingCount); err != nil {
		return nil, fmt.Errorf("admin.SqliteAssetVerifier.Verify count pending outbox: %w", err)
	}
	rep.OutboxNoPending = rep.OutboxPendingCount == 0

	// Aggregate gate.
	rep.Eligible = rep.LifecycleDELETED && rep.QdrantAbsent && rep.OutboxNoPending
	if !rep.Eligible {
		if rep.RefusalReason == "" {
			rep.RefusalReason = refuseReason(rep)
		}
		return rep, fmt.Errorf("%w: %s", ErrAssetVerifier, rep.RefusalReason)
	}
	return rep, nil
}

// refuseReason formats the first failing condition into a stable
// "hard delete refused: <condition>" string for operator triage.
// Order matches the spec's enumerated conditions (lifecycle first,
// then Qdrant, then outbox) so the message is deterministic across
// runs.
func refuseReason(r *VerifyReport) string {
	switch {
	case !r.LifecycleDELETED:
		return "hard delete refused: lifecycle_state is not DELETE_PENDING or DELETED"
	case !r.QdrantAbsent:
		return "hard delete refused: Qdrant point still present for this asset_id"
	case !r.OutboxNoPending:
		return fmt.Sprintf("hard delete refused: %d pending outbox_event(s) reference this asset_id", r.OutboxPendingCount)
	default:
		return "hard delete refused: unknown gate condition"
	}
}
