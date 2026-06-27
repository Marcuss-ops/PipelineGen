package qdrant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ── Drift taxonomy (QDRANT-005B, June 2026) ─────────────────────────

// DriftClass is the canonical category used by the Reconciler to label
// data divergence between the SQLite media_assets table and a Qdrant
// collection.
//
// The 5 named classes cover the failure modes observed during prior
// reindex operations:
//   - MISSING      — present in SQLite (live), absent in Qdrant
//   - EXTRA        — present in Qdrant, absent in SQLite OR DB row DELETED
//   - STALE        — present in both, lifecycle_state field diverges
//   - VERSION      — present in both, embedding_version does not match
//     the schema's CurrentEmbeddingVersion
//   - ID_MISMATCH  — Qdrant point.ID does NOT equal
//     AssetIDToQdrantPointID(payload.asset_id), a sign that the point
//     was written by a legacy path that bypassed the canonical
//     namespacing boundary in pointid.go
type DriftClass string

const (
	DriftMissing    DriftClass = "MISSING"
	DriftExtra      DriftClass = "EXTRA"
	DriftStale      DriftClass = "STALE"
	DriftVersion    DriftClass = "VERSION"
	DriftIdMismatch DriftClass = "ID_MISMATCH"
)

// AllDriftClasses returns the canonical 5-class enum list, useful for
// pre-allocating DriftSummary maps. Order matches the constant block
// above.
func AllDriftClasses() []DriftClass {
	return []DriftClass{
		DriftMissing,
		DriftExtra,
		DriftStale,
		DriftVersion,
		DriftIdMismatch,
	}
}

// DriftItem is a single unit of divergence reported by Reconcile.
type DriftItem struct {
	AssetID string     `json:"asset_id"`
	PointID string     `json:"point_id,omitempty"`
	Class   DriftClass `json:"class"`
	Detail  string     `json:"detail,omitempty"`
}

// ── Status values (mirroring reaper.go's vocabulary for operator UX) ──

const (
	ReconStatusOK             = "ok"
	ReconStatusNoop           = "noop"
	ReconStatusPartial        = "partial"
	ReconStatusFailed         = "failed"
	ReconStatusScanIncomplete  = "scan_incomplete"
	ReconStatusDBTruncated    = "db_truncated"
)

// ── Defaults ─────────────────────────────────────────────────────────

// DefaultMaxDBRows bounds the in-memory snapshot of `media_assets` the
// Reconciler builds during the DB-scan phase. The default keeps a corpus
// whose live rows ≤ 500K fully in memory (≈40 MB for dbSnap entries +
// map overhead) without paging. Operators with larger corpora must set
// ReconcileOptions.MaxDBRows deliberately — see the @TODO below.
//
// Achievement notes (June 2026 review-fix round):
//   - At 10^6 rows the snapshot uses ≈80–200 MB; the applyRepairs phase
//     can push peak RSS higher because UpsertFromClips holds mapper state.
//   - New (QDRANT-005B): reaching the cap yields Status="db_truncated"
//     and aborts the apply phase (same fail-closed contract as scroll
//     failure). This is a strict improvement over silently OOMing the
//     process.
//
// Follow-up: a streaming variant (chunked DB scan + per-chunk apply
// batch) is tracked as QDRANT-005B-N+1.
const DefaultMaxDBRows = 500_000

// defaultNonDeletedLCS is the canonical "live" lifecycle_state set
// after normalization (see internal/infrastructure/qdrant/asset_store.go).
// Any value outside this set is treated as DELETED for reconciliation.
var defaultNonDeletedLCS = []string{
	"active",
	"ready",
	"staging",
	"processing",
	"pending",
}

// ── Options & result types ───────────────────────────────────────────

// ReconcileOptions configures a single Reconcile run.
type ReconcileOptions struct {
	// Collection is the Qdrant collection name to scan (required).
	Collection string

	// BatchSize is the scroll page size (1–500, default 500). Values
	// above Qdrant's effective limit are capped rather than rejected
	// so a noisy operator flag does not block the run.
	BatchSize int

	// RepairLimit caps the number of repair operations applied during
	// this run (0 = unlimited). The scan itself is NEVER truncated —
	// a partial scan must fail closed (see Reconciler docstring for the
	// operator contract).
	RepairLimit int

	// MaxDBRows caps the in-memory SQLite snapshot built during the
	// pre-scroll phase. Reaching the cap sets Status="db_truncated"
	// and blocks the apply phase (same fail-closed contract as Qdrant
	// scroll failure). Set to 0 to fall back to DefaultMaxDBRows.
	MaxDBRows int

	// DryRun reports which points/assets would be affected without
	// mutating Qdrant or SQLite.
	DryRun bool

	// NonDeletedLCS is the set of normalized lifecycle_state values
	// considered "live" (i.e., should appear in Qdrant). Rows in
	// SQLite with lifecycle_state NOT in this set (after the
	// normalizeLifecycleState case-insensitive mapping applied in
	// asset_store.go) are treated as DELETED and produce EXTRA drifts
	// for their corresponding Qdrant points, NOT MISSING for themselves.
	// Default: ["active", "ready", "staging", "processing", "pending"].
	NonDeletedLCS []string
}

// ReconcileResult is the outcome of a Reconcile run.
type ReconcileResult struct {
	RunID          string             `json:"run_id"`
	Collection     string             `json:"collection"`
	DBScanned      int                `json:"db_scanned"`
	QDScanned      int                `json:"qd_scanned"`
	StartedAt      time.Time          `json:"started_at"`
	CompletedAt    time.Time          `json:"completed_at"`
	Status         string             `json:"status"`
	DryRun         bool               `json:"dry_run"`
	Drift          []DriftItem        `json:"drift,omitempty"`
	DriftSummary   map[DriftClass]int `json:"drift_summary"`
	RepairedUpserts int               `json:"repaired_upserts"`
	RepairedDeletes int               `json:"repaired_deletes"`
	Errors         []string           `json:"errors,omitempty"`
	DriftSample    []DriftItem        `json:"drift_sample,omitempty"` // first 50 per class
}

// ── Repairer (the contract the Reconciler needs to apply drift fixes) ─

// Repairer is the union of the canonical write contracts in the Qdrant
// package: IndexWriterPort (upsert) and QdrantDeleter (delete). The
// concrete implementation (*IndexWriter) satisfies both. The Reconciler
// uses direct REST UPSERT/DELETE on Qdrant — both are idempotent on
// point IDs.
//
// Idempotency note (operator audit):
//   - UPSERT on a Qdrant point ID replaces whatever is at that ID. Re-running
//     reconcile produces the same payload; net effect on Qdrant is null-set.
//   - DELETE on a missing point ID surfaces a 404 through Client.DeletePoints.
//     The Reconciler logs the failure as a Warn and continues — the point
//     is treated as already-gone, which is the safer assumption for the
//     next pass (no double-delete UID bookkeeping needed).
//   - For audit traceability (QDRANT-005B review-fix), every repair
//     operation is logged with a deterministic repair_action_id derived
//     from (runID + class + assetID). This is a per-request audit token,
//     NOT a dedup table — the user spec mentioned event_key for the same
//     purpose and we honour the spirit by making each repair identifiable
//     in logs and downstream outbox correlation.
type Repairer interface {
	IndexWriterPort
	QdrantDeleter
}

// Compile-time assertion: *IndexWriter satisfies the Repairer interface.
// Both IndexWriterPort (upsert) and QdrantDeleter (delete) are already
// individually asserted in index_writer.go; this combined assertion
// catches drift in either contract that would otherwise only fail at
// the first Reconcile() call site.
var _ Repairer = (*IndexWriter)(nil)

// ── Internal snapshot types ──────────────────────────────────────────

// dbSnap is the lightweight per-row payload the Reconciler needs to
// detect MISSING/STALE drifts. We deliberately avoid FetchAsset (the
// IndexWriter path) because that pulls embeddings and the full
// metadata JSON — neither is needed for drift classification, and
// loading them per row at our corpus scale (~10^6 rows) would melt
// the SQLite connection pool.
type dbSnap struct {
	ID             string
	LifecycleState string
	EmbeddingVer   string
}

// qdrantSnap is the point-side snapshot. We scroll with with_vector=false
// so only the payload is returned, which is sufficient for classification.
// The canonical point.ID is the AssetIDToQdrantPointID form per pointid.go.
type qdrantSnap struct {
	PointID        string
	PayloadAssetID string
	LifecycleState string
	EmbeddingVer   string
}

// ── Reconciler ───────────────────────────────────────────────────────

// ErrNilDB is returned when a Reconciler is constructed with a nil DB.
var ErrNilDB = errors.New("reconciler: db is nil")

// ErrDBTruncated signals that the SQLite snapshot exceeded MaxDBRows
// and the run failed closed before the apply phase began. Carry the
// observed row count for the operator's runbook follow-up.
type ErrDBTruncated struct {
	MaxDBRows int
	Observed  int
}

func (e *ErrDBTruncated) Error() string {
	return fmt.Sprintf("reconciler: db snapshot exceeded MaxDBRows=%d (observed at least %d rows); repairs NOT applied", e.MaxDBRows, e.Observed)
}

// Reconciler compares SQLite media_assets against a Qdrant collection
// and (when opted-in) applies repairs via the canonical IndexWriter.
//
// Fail-closed contract (QDRANT-005B):
//   - Partial Qdrant scroll → Status="scan_incomplete", no apply.
//   - DB snapshot exceeding MaxDBRows → Status="db_truncated", no apply.
//   - Context cancellation mid-loop → Status="scan_incomplete", no apply.
//
// All three branches are equivalently strict: applying repairs against
// an incomplete view would manufacture false-positive MISSING drifts.
type Reconciler struct {
	client    *Client
	db        *sql.DB
	repairer  Repairer
	schema    *IndexSchema
	log       *zap.Logger
	collection string // set per-Reconcile call; used as drift_action_id scope

	// scrollFn is the Qdrant scroll driver. Defaults to client.ScrollPoints.
	// Tests can swap it for a deterministic stub to exercise fail-closed
	// orchestration paths (scroll error mid-loop → scan_incomplete) without
	// an HTTP server. Kept as a closure field rather than an interface to
	// avoid introducing a new public abstraction; tests access it via
	// package-internal access in reconciler_scroll_test.go.
	scrollFn func(ctx context.Context, collection, offset string, limit int) (*ScrollResult, error)
}

// NewReconciler creates a Reconciler backed by the Qdrant client + the
// media SQLite DB. Pass *IndexWriter as repairer to satisfy both
// upsert and delete via the same concrete (REST is idempotent on
// point IDs).
func NewReconciler(client *Client, db *sql.DB, repairer Repairer, schema *IndexSchema, log *zap.Logger) *Reconciler {
	if log == nil {
		log = zap.NewNop()
	}
	r := &Reconciler{
		client:   client,
		db:       db,
		repairer: repairer,
		schema:   schema,
		log:      log,
	}
	if client != nil {
		r.scrollFn = client.ScrollPoints
	}
	return r
}

// Reconcile runs the full scan + classify + (optional) repair pipeline.
// See the Reconciler type docstring for the fail-closed contract.
//
// Steps:
//  1. Snapshot the media_assets table (raw SQL — single SELECT, bounded
//     by MaxDBRows). Reaching the cap aborts with Status="db_truncated".
//  2. Scroll the Qdrant collection exhaustively via scrollFn. ANY
//     non-clean exit (error, ctx cancel, cap) yields Status="scan_incomplete"
//     and skips apply.
//  3. Classify drift into the 5 DriftClass buckets.
//  4. If DryRun=false and scan was clean, apply repairs via Repairer.
//     RepairLimit caps the count; reaching it sets Status="partial".
func (r *Reconciler) Reconcile(ctx context.Context, opts ReconcileOptions) (*ReconcileResult, error) {
	if r.client == nil {
		return nil, ErrNilClient
	}
	if r.db == nil {
		return nil, ErrNilDB
	}
	if opts.Collection == "" {
		return nil, fmt.Errorf("reconciler: collection is required")
	}
	r.collection = opts.Collection

	batch := opts.BatchSize
	if batch <= 0 {
		batch = 500
	}
	if batch > 1000 {
		batch = 1000
		r.log.Warn("reconciler batch size capped",
			zap.Int("requested", opts.BatchSize),
			zap.Int("effective", batch))
	}

	maxRows := opts.MaxDBRows
	if maxRows <= 0 {
		maxRows = DefaultMaxDBRows
	}

	liveSet := opts.NonDeletedLCS
	if len(liveSet) == 0 {
		liveSet = defaultNonDeletedLCS
	}
	liveLookup := make(map[string]struct{}, len(liveSet))
	for _, s := range liveSet {
		liveLookup[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
	}

	runID, err := generateReconcileRunID()
	if err != nil {
		return nil, fmt.Errorf("reconciler: generate run id: %w", err)
	}

	started := time.Now().UTC()
	res := &ReconcileResult{
		RunID:        runID,
		Collection:   opts.Collection,
		StartedAt:    started,
		DryRun:       opts.DryRun,
		DriftSummary: make(map[DriftClass]int, len(AllDriftClasses())),
	}

	// ── Phase 1: DB snapshot (bounded by MaxDBRows) ─────────────
	dbByID, dbErr := r.fetchDBSnapshot(ctx, liveLookup, maxRows)
	if dbErr != nil {
		res.DBScanned = len(dbByID)
		res.CompletedAt = time.Now().UTC()
		if truncated, ok := dbErr.(*ErrDBTruncated); ok {
			// fail-closed on DB cap; repairs not applied.
			res.Status = ReconStatusDBTruncated
			res.Errors = append(res.Errors,
				fmt.Sprintf("db snapshot truncated at MaxDBRows=%d (observed ≥%d); repairs NOT applied",
					truncated.MaxDBRows, truncated.Observed))
		} else {
			res.Status = ReconStatusFailed
			res.Errors = append(res.Errors, fmt.Sprintf("db snapshot: %v", dbErr))
		}
		return res, nil
	}
	res.DBScanned = len(dbByID)
	r.log.Info("reconciler: db snapshot complete",
		zap.Int("rows", len(dbByID)),
		zap.Int("max_rows", maxRows),
		zap.String("collection", opts.Collection))

	// ── Phase 2: Qdrant scroll (MUST finish completely) ───────────
	qdByPointID, qdByAssetID, scrollAborted := r.fullScroll(ctx, opts.Collection, batch, res)
	if scrollAborted {
		res.CompletedAt = time.Now().UTC()
		return res, nil
	}
	r.log.Info("reconciler: qdrant scroll complete",
		zap.Int("points", res.QDScanned),
		zap.String("collection", opts.Collection))

	// ── Phase 3: classify drift ───────────────────────────────────
	drifts := classifyDrift(dbByID, qdByPointID, qdByAssetID, r.schema, liveLookup)
	res.Drift = drifts
	for _, c := range AllDriftClasses() {
		res.DriftSummary[c] = 0
	}
	for _, d := range drifts {
		res.DriftSummary[d.Class]++
		if len(res.DriftSample) < 50 {
			res.DriftSample = append(res.DriftSample, d)
		}
	}

	r.log.Info("reconciler: drift classification complete",
		zap.Int("missing", res.DriftSummary[DriftMissing]),
		zap.Int("extra", res.DriftSummary[DriftExtra]),
		zap.Int("stale", res.DriftSummary[DriftStale]),
		zap.Int("version", res.DriftSummary[DriftVersion]),
		zap.Int("id_mismatch", res.DriftSummary[DriftIdMismatch]),
		zap.String("run_id", runID))

	// ── Phase 4: apply repairs (only when scan was clean) ─────────
	if !opts.DryRun && r.repairer != nil {
		upserts, deletes, partial := r.applyRepairs(ctx, drifts, opts.RepairLimit, runID)
		res.RepairedUpserts = upserts
		res.RepairedDeletes = deletes
		if partial {
			res.Status = ReconStatusPartial
			res.Errors = append(res.Errors,
				fmt.Sprintf("repair limit %d reached; remaining drifts unapplied", opts.RepairLimit))
		}
	}

	res.CompletedAt = time.Now().UTC()

	// Final status: scan_incomplete / db_truncated / partial already set;
	// otherwise pick by drift.
	if res.Status == "" {
		switch {
		case len(drifts) == 0:
			res.Status = ReconStatusNoop
		default:
			res.Status = ReconStatusOK
		}
	}

	return res, nil
}

// ── Phase 2 helper: full scroll (fail-closed on truncation) ──────────

// fullScroll drives r.scrollFn until empty-Offset / EOS. Returns true
// when the scroll must abort the run (sets Status fields on res in-place).
// Populates res.QDScanned + returns the two qdrantSnap maps so the
// orchestrator can hand them straight to classifyDrift.
func (r *Reconciler) fullScroll(ctx context.Context, collection string, batch int, res *ReconcileResult) (map[string]qdrantSnap, map[string]qdrantSnap, bool) {
	qdByPointID := make(map[string]qdrantSnap)
	qdByAssetID := make(map[string]qdrantSnap)
	offset := ""
	for {
		if err := ctx.Err(); err != nil {
			res.Status = ReconStatusScanIncomplete
			res.Errors = append(res.Errors, fmt.Sprintf("ctx canceled: %v", err))
			r.log.Warn("reconciler: ctx canceled mid-scroll — failing closed", zap.Error(err))
			return qdByPointID, qdByAssetID, true
		}
		page, err := r.scrollFn(ctx, collection, offset, batch)
		if err != nil {
			res.Status = ReconStatusScanIncomplete
			res.Errors = append(res.Errors, fmt.Sprintf("qdrant scroll offset=%q: %v", offset, err))
			r.log.Warn("reconciler: scroll error — failing closed",
				zap.String("offset", offset),
				zap.Error(err))
			return qdByPointID, qdByAssetID, true
		}
		if page == nil || len(page.Points) == 0 {
			return qdByPointID, qdByAssetID, false
		}
		res.QDScanned += len(page.Points)

		for _, pt := range page.Points {
			snap := qdrantSnap{PointID: pt.ID}
			if pt.Payload != nil {
				if id, ok := pt.Payload["asset_id"].(string); ok {
					snap.PayloadAssetID = id
				}
				if lcs, ok := pt.Payload["lifecycle_state"].(string); ok {
					snap.LifecycleState = normalizeLifecycleStateForReconcile(lcs)
				} else if st, ok := pt.Payload["status"].(string); ok {
					snap.LifecycleState = normalizeLifecycleStateForReconcile(st)
				}
				if v, ok := pt.Payload["embedding_version"].(string); ok {
					snap.EmbeddingVer = v
				}
			}
			qdByPointID[snap.PointID] = snap
			if snap.PayloadAssetID != "" {
				qdByAssetID[snap.PayloadAssetID] = snap
			}
		}

		if page.NextOffset == "" {
			return qdByPointID, qdByAssetID, false
		}
		offset = page.NextOffset
	}
}

// ── Phase 1 helper: DB snapshot via raw SQL with MaxDBRows cap ──────

// fetchDBSnapshot does a single SELECT on media_assets, returning only
// the columns the Reconciler needs for classification. It reads
// COALESCE(lifecycle_state, status, 'ACTIVE') so legacy rows that have
// only `status` still classify correctly (QDRANT-004 closure shape).
//
// Returns ErrDBTruncated when the row count reaches maxRows and there
// are more rows to scan — the caller MUST treat this as fail-closed.
func (r *Reconciler) fetchDBSnapshot(ctx context.Context, liveLookup map[string]struct{}, maxRows int) (map[string]dbSnap, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(lifecycle_state, status, 'ACTIVE'), COALESCE(embedding_version, '')
		FROM media_assets
		WHERE deleted_at IS NULL OR deleted_at = ''
	`)
	if err != nil {
		return nil, fmt.Errorf("query media_assets: %w", err)
	}
	defer rows.Close()

	out := make(map[string]dbSnap)
	for rows.Next() {
		var s dbSnap
		var lcs string
		if err := rows.Scan(&s.ID, &lcs, &s.EmbeddingVer); err != nil {
			return nil, fmt.Errorf("scan media_assets row: %w", err)
		}
		s.LifecycleState = normalizeLifecycleStateForReconcile(lcs)
		if _, isLive := liveLookup[s.LifecycleState]; !isLive {
			// Treat non-live rows as DELETED: do not include them in the
			// "live DB set" — any matching Qdrant point is therefore EXTRA.
			continue
		}
		out[s.ID] = s
		if maxRows > 0 && len(out) > maxRows {
			// Bail with the snapshot we already built, plus the cap. The
			// caller will fail closed (Status="db_truncated"); this returns
			// no applyable state so the apply phase cannot accidentally run.
			rows.Close()
			return out, &ErrDBTruncated{MaxDBRows: maxRows, Observed: len(out)}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media_assets: %w", err)
	}
	return out, nil
}

// ── Phase 3 helper: classification (pure, unit-testable) ─────────────

// classifyDrift is the pure-function part of the pipeline. It produces
// DriftItems from pre-built DB and Qdrant snapshots. Splitting this out
// from the HTTP-driven Reconcile orchestrator lets unit tests exercise
// every drift class without a stubbed Client or DB.
func classifyDrift(
	dbByID map[string]dbSnap,
	qdByPointID map[string]qdrantSnap,
	qdByAssetID map[string]qdrantSnap,
	schema *IndexSchema,
	liveLookup map[string]struct{},
) []DriftItem {
	var out []DriftItem
	expectedVer := ""
	if schema != nil {
		expectedVer = CurrentEmbeddingVersion
	}

	seenAssets := make(map[string]struct{}, len(qdByPointID))

	// Pass 1: classify each Qdrant point against the DB.
	for pointID, q := range qdByPointID {
		assetID := q.PayloadAssetID
		if assetID == "" {
			// Orphan point: no asset_id in payload → EXTRA (cannot classify ID_MISMATCH).
			out = append(out, DriftItem{
				PointID: pointID,
				Class:   DriftExtra,
				Detail:  "qdrant point has no payload.asset_id",
			})
			continue
		}

		// ID_MISMATCH: point.ID should be uuid5(assetID) under our namespace.
		expected := AssetIDToQdrantPointID(assetID)
		if pointID != expected {
			out = append(out, DriftItem{
				AssetID: assetID,
				PointID: pointID,
				Class:   DriftIdMismatch,
				Detail:  fmt.Sprintf("point.ID=%q expected=%q (canonical uuid5 mismatch)", pointID, expected),
			})
		}

		dbRow, inDB := dbByID[assetID]
		if !inDB {
			out = append(out, DriftItem{
				AssetID: assetID,
				PointID: pointID,
				Class:   DriftExtra,
				Detail:  "payload asset_id not present in media_assets (deleted or absent)",
			})
			seenAssets[assetID] = struct{}{}
			continue
		}
		seenAssets[assetID] = struct{}{}

		// STALE: lifecycle_state diverges.
		if q.LifecycleState != "" && dbRow.LifecycleState != "" && q.LifecycleState != dbRow.LifecycleState {
			out = append(out, DriftItem{
				AssetID: assetID,
				PointID: pointID,
				Class:   DriftStale,
				Detail:  fmt.Sprintf("lifecycle_state qdrant=%q db=%q", q.LifecycleState, dbRow.LifecycleState),
			})
		}

		// VERSION: embedding_version does not match the schema's current.
		if expectedVer != "" {
			if q.EmbeddingVer != "" && q.EmbeddingVer != expectedVer {
				out = append(out, DriftItem{
					AssetID: assetID,
					PointID: pointID,
					Class:   DriftVersion,
					Detail:  fmt.Sprintf("embedding_version qdrant=%q expected=%q", q.EmbeddingVer, expectedVer),
				})
			} else if q.EmbeddingVer == "" {
				out = append(out, DriftItem{
					AssetID: assetID,
					PointID: pointID,
					Class:   DriftVersion,
					Detail:  fmt.Sprintf("embedding_version missing on qdrant point; expected=%q", expectedVer),
				})
			}
		}

		_ = liveLookup // referenced for symmetry; liveSet already filtered dbByID
	}

	// Pass 2: classify DB-only assets (live rows with no Qdrant point).
	// Use both canonical and payload-asset-id indexes to avoid double-reporting
	// when a point was written with a non-canonical point.ID (the ID_MISMATCH
	// case from Pass 1).
	for assetID := range dbByID {
		if _, seen := seenAssets[assetID]; seen {
			continue
		}
		expected := AssetIDToQdrantPointID(assetID)
		if _, present := qdByPointID[expected]; present {
			continue
		}
		out = append(out, DriftItem{
			AssetID: assetID,
			PointID: expected,
			Class:   DriftMissing,
			Detail:  fmt.Sprintf("db has live asset; qdrant has no point at canonical id=%q", expected),
		})
	}

	_ = qdByAssetID // currently unused; kept as secondary index for future drift heuristics

	return out
}

// ── Phase 4 helper: apply repairs (UPSERT / DELETE via Repairer) ─────

// applyRepairs walks the classified drifts and calls the canonical
// IndexWriter paths for repair. Each repair emits THREE audit tokens
// (review-fix round, June 2026):
//
//   - run_id:           per-Reconcile run (random 16-byte hex). Lets you
//                       filter logs to a specific reconciliation window.
//   - repair_action_id: per-(run, action) — derived from run_id + drift
//                       class + asset/point id. Uniquely identifies one
//                       repair attempt within a run.
//   - drift_action_id:  STABLE across runs — derived solely from
//                       (collection, drift class, asset id). Lets you
//                       correlate "did this drift ever get repaired before"
//                       across runs; this is the audit primitive the
//                       user spec ("event_key stabile") requires.
//
//	  Class         Repair action
//	  -----------   ------------------------------------------------
//	  MISSING       UpsertFromClip (re-build point from SQLite row)
//	  STALE         UpsertFromClip (overwrite payload.lifecycle_state)
//	  VERSION       UpsertFromClip (overwrite payload.embedding_version)
//	  EXTRA         DeletePoints (point not in live DB set)
//	  ID_MISMATCH   DeletePoints on the bad id + UpsertFromClip
//	                 (re-create under the canonical id). The "double
//	                 action" is intentional — the schema's id-namespace
//	                 invariant requires only the canonical id to remain.
//
// Return: (upserts, deletes, partial). `partial` is true when RepairLimit
// was hit before all applicable drifts were processed.
func (r *Reconciler) applyRepairs(ctx context.Context, drifts []DriftItem, limit int, runID string) (int, int, bool) {
	upserts, deletes := 0, 0
	partial := false

	idsToDelete := make([]string, 0)
	// Each upsert entry carries the originating drift class so the audit
	// log preserves original context (MISSING re-upsert vs ID_MISMATCH
	// re-upsert-after-delete) — both routes get a partial class token
	// inside repair_action_id, but the stable drift_action_id uses the
	// genuine originating class so cross-run correlation is class-aware.
	type upsertEntry struct {
		assetID string
		class   DriftClass
	}
	idsToUpsert := make([]upsertEntry, 0)

	for _, d := range drifts {
		switch d.Class {
		case DriftExtra:
			if d.PointID != "" {
				idsToDelete = append(idsToDelete, d.PointID)
			}
		case DriftIdMismatch:
			if d.PointID != "" {
				idsToDelete = append(idsToDelete, d.PointID)
			}
			idsToUpsert = append(idsToUpsert, upsertEntry{assetID: d.AssetID, class: d.Class})
		case DriftMissing, DriftStale, DriftVersion:
			idsToUpsert = append(idsToUpsert, upsertEntry{assetID: d.AssetID, class: d.Class})
		}
	}

	for _, id := range idsToDelete {
		if limit > 0 && (upserts+deletes) >= limit {
			partial = true
			break
		}
		// drift_action_id is keyed off (collection, class, point id); the
		// asset-id namespace is encoded in the canonical UUIDv5 form of the
		// point id, so re-running reconcile on the same EXTRA drift
		// produces the same stable audit token.
		driftID := driftActionID(r.collection, string(DriftExtra), id)
		actionID := repairActionID(runID, string(DriftExtra), id, "")
		if err := r.repairer.DeletePoints(ctx, []string{id}); err != nil {
			r.log.Warn("reconciler: delete failed",
				zap.String("class", string(DriftExtra)),
				zap.String("point_id", id),
				zap.String("drift_action_id", driftID),
				zap.String("repair_action_id", actionID),
				zap.String("run_id", runID),
				zap.Error(err))
			continue
		}
		deletes++
		r.log.Info("reconciler: repair applied",
			zap.String("class", string(DriftExtra)),
			zap.String("point_id", id),
			zap.String("drift_action_id", driftID),
			zap.String("repair_action_id", actionID),
			zap.String("run_id", runID))
	}

	for _, entry := range idsToUpsert {
		if limit > 0 && (upserts+deletes) >= limit {
			partial = true
			break
		}
		// Both audit tokens preserve the originating drift class — the
		// audit log surfaces "this MISSING drift at asset_a was repaired
		// in run R1" with a stable cross-run identifier.
		driftID := driftActionID(r.collection, string(entry.class), entry.assetID)
		actionID := repairActionID(runID, string(entry.class), entry.assetID, "")
		if err := r.repairer.UpsertFromClips(ctx, []string{entry.assetID}); err != nil {
			r.log.Warn("reconciler: upsert failed",
				zap.String("class", string(entry.class)),
				zap.String("asset_id", entry.assetID),
				zap.String("drift_action_id", driftID),
				zap.String("repair_action_id", actionID),
				zap.String("run_id", runID),
				zap.Error(err))
			continue
		}
		upserts++
		r.log.Info("reconciler: repair applied",
			zap.String("class", string(entry.class)),
			zap.String("asset_id", entry.assetID),
			zap.String("drift_action_id", driftID),
			zap.String("repair_action_id", actionID),
			zap.String("run_id", runID))
	}

	return upserts, deletes, partial
}

// repairActionID deterministically derives an audit ID for a single
// repair action. Inputs: runID (the reconciler run hash), class (drift
// category), and the asset/point ID being repaired. The "extra" field
// is reserved for future disambiguation (e.g. point vs asset on ID_MISMATCH)
// without breaking the hash contract.
//
// Contract: identical inputs produce the same ID so an operator can
// re-grep their audit log to find every repair that *should* have run
// for a given audit session. Note this token is per-RUN; for STABLE
// cross-run audit correlation use driftActionID (below).
func repairActionID(runID, class, id, extra string) string {
	h := sha256.New()
	h.Write([]byte(runID))
	h.Write([]byte{0}) // separator
	h.Write([]byte(class))
	h.Write([]byte{0})
	h.Write([]byte(id))
	if extra != "" {
		h.Write([]byte{0})
		h.Write([]byte(extra))
	}
	return "rep_" + hex.EncodeToString(h.Sum(nil))[:20]
}

// driftActionID is the STABLE audit token for a single unit of drift.
// It is independent of runID so re-running reconciliation produces
// the same audit ID for the same drift — this is the contract the
// user spec ("idempotente via event_key stabile") explicitly required.
//
// Inputs:
//   - collection: the Qdrant collection being reconciled
//   - class:      the drift class (MISSING/EXTRA/STALE/VERSION/ID_MISMATCH)
//   - assetID:    the asset ID whose drift is being audited
//
// Use alongside repairActionID: drift_action_id correlates the same
// drift across runs; repair_action_id correlates the same repair
// attempt within a run. Together they form a full audit trail.
func driftActionID(collection, class, assetID string) string {
	h := sha256.New()
	h.Write([]byte(collection))
	h.Write([]byte{0}) // separator
	h.Write([]byte(class))
	h.Write([]byte{0})
	h.Write([]byte(assetID))
	return "dft_" + hex.EncodeToString(h.Sum(nil))[:20]
}

// ── Helpers ──────────────────────────────────────────────────────────

// normalizeLifecycleStateForReconcile mirrors asset_store.go's
// normalizeLifecycleState — the canonical lowercase mapping for the
// canonical 6 enum values:
//
//	  ACTIVE / active      → "active"
//	  STAGING / staging    → "staging"
//	  PROCESSING / …       → "processing"
//	  DELETED / deleted    → "deleted"
//	  ready                → "ready"
//	  pending              → "pending"
//
// Anything outside the canonical set falls back to "deleted" — a safe
// default that ensures unknown lifecycle values are treated as non-live
// (and therefore produce EXTRA drifts for any matching Qdrant point)
// rather than mis-classified as ACTIVE.
func normalizeLifecycleStateForReconcile(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	switch l {
	case "active":
		return "active"
	case "staging":
		return "staging"
	case "processing":
		return "processing"
	case "deleted":
		return "deleted"
	case "ready":
		return "ready"
	case "pending":
		return "pending"
	default:
		return "deleted"
	}
}

// generateReconcileRunID produces a hex-encoded random 16-byte identifier.
// Mirrors reaper.go's scheme — separate symbol so reconciler run IDs
// can be distinguished from reaper run IDs in audit logs.
func generateReconcileRunID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	return hex.EncodeToString(b), nil
}
