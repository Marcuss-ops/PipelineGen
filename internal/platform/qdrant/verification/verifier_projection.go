// Package verification — PROJECTION VERIFIER (August 2026): parity of
// the ACTIVE projection.
//
// ReindexVerifier (verifier.go) validates a freshly-built collection
// BEFORE the alias switch. This file adds the operator-facing verifier
// for the projection that is LIVE right now:
//
//  1. Resolve the runtime alias target (the active collection).
//
//  2. Load the canonical eligible asset IDs from SQLite. The loader is
//     SQLiteAssetStore.ListAllAssetIDs, whose WHERE clause is the
//     SearchIndexEligibilitySQL SSOT — the exact same boundary the
//     Qdrant projection (indexing.IndexWriter / asset_store) uses.
//
//  3. Scroll EVERY point in the active collection and collect the
//     payload asset_ids.
//
//  4. Compute the set parity (plan item #8 — "Il verifier deve
//     confrontare il set GIUSTO"):
//
//     eligible_sqlite   = |SQLiteEligibleAssetIDs|
//     qdrant_points     = |QdrantActiveAssetIDs|
//     missing_in_qdrant = eligible but absent in Qdrant (projection bug)
//     orphan_in_qdrant  = in Qdrant but absent/ineligible in SQLite
//     (stale projection)
//
// PASS (report.Passed) requires ALL of:
//
//	missing_in_qdrant == 0
//	orphan_in_qdrant  == 0
//	no point lacks a payload asset_id
//	complete scan     == true (no abort, no page error)
//	zero errors
//
// This deliberately does NOT compare COUNT(index_state='INDEXED') vs
// COUNT(Qdrant): INDEXED is an observed projection result, while
// eligibility is a property of the canonical asset row.
package verification

import (
	"context"
	"fmt"
	"sort"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ProjectionVerificationReport is the machine-readable outcome of
// VerifyActiveProjection: the eligible-vs-active set parity.
type ProjectionVerificationReport struct {
	Collection           string   `json:"collection"`
	EligibleSQLite       int      `json:"eligible_sqlite"`
	QdrantPoints         int      `json:"qdrant_points"`
	PointsMissingAssetID int      `json:"points_missing_asset_id,omitempty"`
	MissingCount         int      `json:"missing_in_qdrant"`
	MissingIDs           []string `json:"missing_ids,omitempty"`
	OrphanCount          int      `json:"orphan_in_qdrant"`
	OrphanIDs            []string `json:"orphan_ids,omitempty"`
	CompleteScan         bool     `json:"complete_scan"`
	Errors               []string `json:"errors,omitempty"`
	Passed               bool     `json:"passed"`
}

// Passes is the SINGLE PASS rule (plan item #8): PASS only when the
// eligible set and the active Qdrant set are exactly the same — zero
// missing, zero orphan — AND nothing prevented the comparison (every
// point carries its asset_id, the scan completed, no errors).
func (r ProjectionVerificationReport) Passes() bool {
	return r.CompleteScan &&
		r.MissingCount == 0 &&
		r.OrphanCount == 0 &&
		r.PointsMissingAssetID == 0 &&
		len(r.Errors) == 0
}

// ProjectionVerifier validates the ACTIVE Qdrant projection against the
// canonical SQLite eligible set.
type ProjectionVerifier struct {
	client     *transport.Client
	assetStore indexing.AssetStore
	schema     *schema.IndexSchema // provides RuntimeAlias
	log        *zap.Logger

	// BatchSize is the scroll page size (default 500).
	BatchSize int
	// MaxScrolls is the safety cap on scroll pages. A trailing
	// NextOffset at the cap makes the scan incomplete (not passed).
	MaxScrolls int
}

// NewProjectionVerifier creates the active-projection verifier. schema
// supplies the runtime alias (must be non-nil); log may be nil.
func NewProjectionVerifier(client *transport.Client, assetStore indexing.AssetStore, schema *schema.IndexSchema, log *zap.Logger) *ProjectionVerifier {
	if log == nil {
		log = zap.NewNop()
	}
	return &ProjectionVerifier{
		client:     client,
		assetStore: assetStore,
		schema:     schema,
		log:        log,
		BatchSize:  500,
		MaxScrolls: 400,
	}
}

// VerifyActiveProjection runs the full parity check against the active
// projection resolved from the canonical runtime alias.
//
// Returns a populated report plus a non-nil error ONLY on a fatal
// failure (alias resolution, SQLite listing, or scroll page error) —
// the report is still usable for diagnostics and Passed is false.
func (v *ProjectionVerifier) VerifyActiveProjection(ctx context.Context) (*ProjectionVerificationReport, error) {
	if v.schema == nil {
		return nil, fmt.Errorf("projection verifier: schema is nil (runtime alias required)")
	}

	// ── 1. Resolve the active collection ────────────────────────────
	collection, err := v.client.ResolveRuntimeCollection(ctx, v.schema.RuntimeAlias)
	if err != nil {
		return nil, err
	}
	if collection == "" {
		return nil, fmt.Errorf("runtime alias %q has no production target; rebuild the canonical collection %q", v.schema.RuntimeAlias, schema.ProductionCollection)
	}
	report := &ProjectionVerificationReport{Collection: collection}

	// ── 2. Eligible SQLite asset IDs (SearchIndexEligibilitySQL SSOT) ─
	eligibleIDs, err := v.assetStore.ListAllAssetIDs(ctx)
	if err != nil {
		return report, fmt.Errorf("list eligible SQLite asset IDs: %w", err)
	}
	report.EligibleSQLite = len(eligibleIDs)
	v.log.Info("projection verifier: eligible SQLite set loaded",
		zap.Int("eligible_sqlite", report.EligibleSQLite),
		zap.String("collection", collection),
	)

	// ── 3. Scroll the active collection for asset_ids ───────────────
	qdrantIDs, missingAssetID, scrollErrs, abort, err := v.scrollActiveAssetIDs(ctx, collection)
	if err != nil {
		return report, err
	}
	report.CompleteScan = !abort && len(scrollErrs) == 0
	report.PointsMissingAssetID = missingAssetID
	report.Errors = append(report.Errors, scrollErrs...)

	// ── 4. Set parity (pure) ────────────────────────────────────────
	parity := computeProjectionParity(eligibleIDs, qdrantIDs)
	report.QdrantPoints = parity.QdrantPoints
	report.MissingCount = parity.MissingCount
	report.MissingIDs = parity.MissingIDs
	report.OrphanCount = parity.OrphanCount
	report.OrphanIDs = parity.OrphanIDs

	// ── 5. PASS rule (single source: Passes) ────────────────────────
	report.Passed = report.Passes()
	return report, nil
}

// scrollActiveAssetIDs scrolls the collection to completion and returns
// the set of payload asset_ids, the count of points without asset_id,
// per-point diagnostics, and abort (true when the scan could not
// complete). A page error is fatal: the caller gets a non-nil err.
func (v *ProjectionVerifier) scrollActiveAssetIDs(ctx context.Context, collection string) (map[string]struct{}, int, []string, bool, error) {
	assetIDs := make(map[string]struct{})
	var missing int
	var errs []string
	batchSize := v.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}
	maxScrolls := v.MaxScrolls
	if maxScrolls <= 0 {
		maxScrolls = 400
	}
	offset := ""
	for i := 0; i < maxScrolls; i++ {
		res, serr := v.client.ScrollPoints(ctx, collection, offset, batchSize, nil)
		if serr != nil {
			return assetIDs, missing, errs, true, fmt.Errorf("scroll page %d of %q: %w", i, collection, serr)
		}
		if res == nil || len(res.Points) == 0 {
			break
		}
		for _, p := range res.Points {
			id, ok := p.Payload["asset_id"].(string)
			if !ok || id == "" {
				missing++
				errs = append(errs, fmt.Sprintf("point %q missing payload asset_id", p.ID))
				continue
			}
			assetIDs[id] = struct{}{}
		}
		if res.NextOffset == "" {
			break
		}
		offset = res.NextOffset
		if i == maxScrolls-1 {
			return assetIDs, missing, errs, true, fmt.Errorf(
				"scroll iteration cap %d reached on %q with NextOffset still trailing (scan incomplete)",
				maxScrolls, collection,
			)
		}
	}
	return assetIDs, missing, errs, false, nil
}

// computeProjectionParity is the pure set-parity core: given the
// eligible SQLite IDs (slice) and the active Qdrant asset_ids (set from
// the scroll), it produces the parity counts + sorted ID lists. No I/O
// — fully unit-testable.
func computeProjectionParity(eligibleIDs []string, qdrantIDs map[string]struct{}) (parity ProjectionVerificationReport) {
	eligibleSet := make(map[string]struct{}, len(eligibleIDs))
	for _, id := range eligibleIDs {
		eligibleSet[id] = struct{}{}
	}

	parity.QdrantPoints = len(qdrantIDs)

	// missing_in_qdrant: eligible but absent in the active collection.
	for id := range eligibleSet {
		if _, ok := qdrantIDs[id]; !ok {
			parity.MissingCount++
			parity.MissingIDs = append(parity.MissingIDs, id)
		}
	}
	// orphan_in_qdrant: present in the active collection but absent (or
	// ineligible) in SQLite — a stale projection point.
	for id := range qdrantIDs {
		if _, ok := eligibleSet[id]; !ok {
			parity.OrphanCount++
			parity.OrphanIDs = append(parity.OrphanIDs, id)
		}
	}
	sort.Strings(parity.MissingIDs)
	sort.Strings(parity.OrphanIDs)
	return parity
}
