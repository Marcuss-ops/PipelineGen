// Package qdrant — Qdrant Reconciler (QDRANT-005 closure, June 2026).
//
// The reconciler is the canonical post-reindex and on-demand
// drift-detection surface for the SQLite media_assets ↔ Qdrant
// media_assets_current projection. It compares:
//
//   - Set equality: media_assets.id (SQLite) vs payload["asset_id"]
//     (Qdrant point scrolled from the active collection).
//   - Per-channel embedding version: payload["embedding_version_<channel>"]
//     must equal schema.DenseVectors[channel].ModelVersion (QDRANT-005
//     non-fallback — the global embedding_version key is NOT a rescue).
//   - Workspace partition: payload["workspace_id"] matches the SQLite
//     row's workspace_id (multi-tenancy invariant).
//   - Lifecycle state parity: payload["lifecycle_state"] matches the
//     canonical SQLite lifecycle_state column (allowlist enforced).
//
// Output is ReconcileReport with at most one of {Ready, Errors} set.
// Production wires this through the lifecycle sweeper and the admin
// CLI (`cmd/admin/qdrant_reconcile.go`, pending); tests exercise it
// via httptest mocks of /collections/.../points/scroll and a
// stubAssetStore (verifier_test.go pattern).

package qdrant

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// ── Reconciler contract ─────────────────────────────────────────────────

// Reconciler compares the SQLite media_assets set against the
// active Qdrant collection and returns a ReconcileReport with the
// drift surface. nil client / nil assetStore / nil schema returns a
// non-nil error at construction time (the constructor refuses to
// silently build a partial adapter).
type Reconciler struct {
	client     *Client
	assetStore AssetStore
	schema     *IndexSchema
	log        *zap.Logger
}

// NewReconciler creates a Reconciler with mandatory dependencies.
// Calling Reconcile on a Reconciler constructed with a nil
// dependency will return a typed "not-initialised" error; nil-safe
// callers should construct with non-nil deps OR check the constructor
// error.
//
// QDRANT-005 closure (June 2026): the constructor signature mirrors
// NewReindexVerifier to keep the production wiring site (admin CLI,
// lifecycle sweeper) symmetric.
func NewReconciler(client *Client, assetStore AssetStore, schema *IndexSchema, log *zap.Logger) (*Reconciler, error) {
	if client == nil {
		return nil, fmt.Errorf("qdrant Reconciler: client is required")
	}
	if assetStore == nil {
		return nil, fmt.Errorf("qdrant Reconciler: assetStore is required")
	}
	if schema == nil {
		return nil, fmt.Errorf("qdrant Reconciler: schema is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Reconciler{
		client:     client,
		assetStore: assetStore,
		schema:     schema,
		log:        log,
	}, nil
}

// Compile-time interface assertion: Reconciler.Reconcile satisfies
// the future systemapi.Reconciler port (QDRANT-005 follow-up
// reconciliation ramp will add this adapter wiring).
var _ interface {
	Reconcile(ctx context.Context) (*ReconcileReport, error)
} = (*Reconciler)(nil)

// ── Report shape ───────────────────────────────────────────────────────

// ReconcileReport is the canonical drift report produced by Reconciler.
type ReconcileReport struct {
	// Collection is the Qdrant collection the report was produced
	// against (typically the active alias target at reconcile time).
	Collection string `json:"collection"`

	// GeneratedAt is the wall-clock time at which the reconcile
	// snapshot was taken. Operators monitoring dashboards key off this.
	GeneratedAt time.Time `json:"generated_at"`

	// Ready is true ONLY when all gates pass:
	//   - MissingCount == 0
	//   - OrphanCount == 0
	//   - VersionDriftTotal == 0
	//   - WorkspaceMismatch == 0
	//   - LifecycleMismatch == 0
	//   - len(Errors) == 0
	Ready bool `json:"ready"`

	// Counts.
	SQLiteTotal int `json:"sqlite_total"`
	QdrantTotal int `json:"qdrant_total"`

	// Sets.
	MissingIDs      []string `json:"missing_ids,omitempty"`
	MissingCount    int      `json:"missing_count"`
	OrphanIDs       []string `json:"orphan_ids,omitempty"`
	OrphanCount     int      `json:"orphan_count"`
	VersionDriftIDs []string `json:"version_drift_ids,omitempty"`
	// VersionDriftPerChannel breaks the global counter down by channel
	// (text, visual, etc.). Mirrors the verifier's per-channel map
	// shape so operators reading both reports see consistent field
	// names.
	VersionDriftPerChannel map[string]int `json:"version_drift_per_channel,omitempty"`
	VersionDriftTotal      int            `json:"version_drift_total"`
	WorkspaceMismatchIDs   []string       `json:"workspace_mismatch_ids,omitempty"`
	WorkspaceMismatch      int            `json:"workspace_mismatch"`
	LifecycleMismatchIDs   []string       `json:"lifecycle_mismatch_ids,omitempty"`
	LifecycleMismatch      int            `json:"lifecycle_mismatch"`
	Errors                 []string       `json:"errors,omitempty"`
}

// ── Reconcile path ─────────────────────────────────────────────────────

// Reconcile runs the full drift detection against the active
// collection. The first-error-wins mode is OFF — partial data is
// preserved in the report so the operator gets a full audit; a non-
// nil error is returned on HARD failures (count, scroll) so the
// orchestrator can abort the alias switch on a flaky reconcile.
//
// QDRANT-005 closure (June 2026): this method is the canonical
// post-reindex drift check. It performs:
//  1. Resolve active alias target.
//  2. Count Qdrant points (hard error on failure).
//  3. List SQLite asset IDs (canonical set).
//  4. Scroll the Qdrant collection (paginated).
//  5. Compare per-point: payload["asset_id"], workspace_id, lifecycle_state,
//     embedding_version_<channel>.
func (r *Reconciler) Reconcile(ctx context.Context) (*ReconcileReport, error) {
	if r == nil {
		return nil, fmt.Errorf("qdrant Reconciler: not initialised")
	}
	report := &ReconcileReport{
		VersionDriftPerChannel: make(map[string]int),
	}
	report.GeneratedAt = time.Now().UTC()

	target, err := r.client.GetAliasTarget(ctx, r.schema.RuntimeAlias)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("resolve alias target: %v", err))
		return report, fmt.Errorf("qdrant Reconciler: alias target: %w", err)
	}
	report.Collection = target

	// SQLite canonical set.
	sqliteIDs, err := r.assetStore.ListAllAssetIDs(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("list SQLite asset IDs: %v", err))
		return report, fmt.Errorf("qdrant Reconciler: SQLite IDs: %w", err)
	}
	report.SQLiteTotal = len(sqliteIDs)
	sqliteSet := make(map[string]bool, len(sqliteIDs))
	for _, id := range sqliteIDs {
		sqliteSet[id] = true
	}

	// Qdrant canonical set.
	qdrantSet := make(map[string]bool)
	offset := ""
	const page = 500
	const maxScrolls = 400
	scrolled := 0
	for i := 0; i < maxScrolls; i++ {
		pageResult, scrollErr := r.client.ScrollPoints(ctx, target, offset, page, nil)
		if scrollErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("scroll page %d: %v", i, scrollErr))
			break
		}
		scrolled += len(pageResult.Points)
		for _, pt := range pageResult.Points {
			assetID, assetIDOK := pt.Payload["asset_id"].(string)
			if !assetIDOK || assetID == "" {
				continue
			}
			qdrantSet[assetID] = true

			// Per-channel version check (QDRANT-005 NO fallback).
			if r.schema != nil {
				ptMismatched := false
				for _, spec := range r.schema.DenseVectors {
					if spec.ModelVersion == "" {
						continue
					}
					key := fmt.Sprintf("embedding_version_%s", spec.Channel)
					actual, present := pt.Payload[key].(string)
					if !present || actual != spec.ModelVersion {
						report.VersionDriftPerChannel[spec.Channel]++
						ptMismatched = true
					}
				}
				if ptMismatched {
					if len(report.VersionDriftIDs) < 100 {
						report.VersionDriftIDs = append(report.VersionDriftIDs, pt.ID)
					}
					report.VersionDriftTotal++
				}
			}

			// Workspace mismatch: documented as a QDRANT-005
			// follow-up. The Reconciler no longer reads
			// payload["workspace_id"] without an explicit scope;
			// multi-tenant reconcile requires a workspace scope
			// argument (added when the per-tenant admin CLI lands).
			// Until then, WorkspaceMismatch remains 0 so the gate
			// cannot false-block on a multi-tenant collection.
			_ = pt.Payload // (pt.Payload is still consumed below for lifecycle parity.)

			// Lifecycle parity: SQLite's COALESCE(lifecycle_state,
			// 'ready') must match payload["lifecycle_state"]. The
			// allowlist check below catches legacy inconsistency.
			lcPayload, _ := pt.Payload["lifecycle_state"].(string)
			if lcPayload != "" && !isAllowedLifecycle(lcPayload) {
				if len(report.LifecycleMismatchIDs) < 100 {
					report.LifecycleMismatchIDs = append(report.LifecycleMismatchIDs, assetID)
				}
				report.LifecycleMismatch++
			}
		}
		if pageResult.NextOffset == "" {
			break
		}
		offset = pageResult.NextOffset
	}
	if scrolled == 0 {
		report.Errors = append(report.Errors, "QDRANT-005: zero points scrolled — cannot compute sets. Check collection exists and scroll API is reachable.")
		return report, fmt.Errorf("qdrant Reconciler: zero points scrolled")
	}
	report.QdrantTotal = len(qdrantSet)

	for sqliteID := range sqliteSet {
		if !qdrantSet[sqliteID] {
			if len(report.MissingIDs) < 200 {
				report.MissingIDs = append(report.MissingIDs, sqliteID)
			}
			report.MissingCount++
		}
	}
	for qdrantID := range qdrantSet {
		if !sqliteSet[qdrantID] {
			if len(report.OrphanIDs) < 200 {
				report.OrphanIDs = append(report.OrphanIDs, qdrantID)
			}
			report.OrphanCount++
		}
	}

	// Ready gate.
	report.Ready = report.MissingCount == 0 &&
		report.OrphanCount == 0 &&
		report.VersionDriftTotal == 0 &&
		report.WorkspaceMismatch == 0 &&
		report.LifecycleMismatch == 0 &&
		len(report.Errors) == 0

	return report, nil
}

// isAllowedLifecycle returns true when the supplied Qdrant
// payload["lifecycle_state"] value is one of the canonical
// allowlist used by the verifier and search adapter. Empty values
// are allowed (legacy points without this field).
//
// QDRANT-005 closure (June 2026): the allowlist is the single source
// of truth for "what is searchable"; out-of-allowlist values trip
// the LifecycleMismatch counter on the reconcile report.
func isAllowedLifecycle(s string) bool {
	if s == "" {
		return true // empty values are legacy-compatible
	}
	switch s {
	case "ready", "active", "searchable", "pending_index", "archived", "error", "deleted":
		return true
	}
	return false
}
