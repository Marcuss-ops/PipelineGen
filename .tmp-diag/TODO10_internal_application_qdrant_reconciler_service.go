// Package reconciler is the Qdrant collection-vs-SQLite reconciler (QDRANT-005,
// PipelineGen, June 2026). The reconciler is the application-level companion to
// the infrastructure-level ReindexVerifier:
//
//   - Verifier (internal/infrastructure/qdrant/verifier.go) is a READ-ONLY
//     diagnostic. It validates a freshly-reindexed collection before the alias
//     switch and surfaces missing/orphan/payload/version issues without any
//     side-effects.
//   - Reconciler (this file) is a SCAN-OR-APPLY operation. It re-scans an
//     already-active collection against SQLite (media_assets canonical IDs),
//     computes drift, and — under operator opt-in — applies repair. Without
//     the operator opt-in (DryRun=true) it returns the report as-is; with it,
//     repair is gated on a CompleteScan boolean that ANDs 5 failure conditions.
//
// QDRANT-005 (TODO 8, June 2026) fail-closed contract:
//
//  1. A scroll page error blocks apply.
//  2. A page-cap reached before drain complete blocks apply.
//  3. NextOffset still set after the last iterated page blocks apply (the
//     "drain didn't finish" symptom).
//  4. Collection expected non-empty but zero points read blocks apply.
//  5. Any point payload missing asset_id blocks apply.
//
// Conditions 4 and 5 are silent in the verifier's READ-ONLY mode (they are
// stamped into report.Errors but Ready still depends on the SAME gates).
// The reconciler raises them to CompleteScan-blocking because the apply phase
// would otherwise delete orphan points or re-emit missing IDs based on
// partial or unverified data.
//
// Dry-run mode treats CompleteScan=false as advisory (the report still
// returns with a warning) so operators can preview the drift before committing.
//
// QDRANT-006 (TODO 10, June 2026) port contract:
//
// The apply phase needs two collaborators — `Outbox` (re-emit missing IDs as
// reconcile_reindex events) and `PayloadMutator` (delete orphan Qdrant
// points). Both are first-class Service struct fields; both must be wired
// when DryRun=false and drift is present, and either being nil is a
// fail-fast contract violation (ErrOutboxRequired / ErrPayloadMutatorRequired).
// The interface split lives in `ports.go` so dependency direction is
// explicit and stub-able. The existing `Repairer` field on `ReconcileOptions`
// remains for orchestration-hook consistency with TODO 8 and for tests that
// don't need the lower-level ports — the production composition layer will
// typically inject the canonical port-shaped adapters and a thin Repairer
// that delegates to them, but for unit tests the higher-level Repairer is
// usually simpler to author.
package reconciler

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// ErrRefusingApply is the sentinel returned when CompleteScan=false and
// DryRun=false. Tests assert errors.Is(err, ErrRefusingApply); callers
// surface it as 409 Conflict or a structured "apply blocked" log line.
var ErrRefusingApply = errors.New("reconcile: refusing apply on incomplete scan")

// Scroller abstracts the Qdrant scroll API. The production adapter wraps
// internal/infrastructure/qdrant.Client.ScrollPoints. Tests inject a scriptable
// fake that returns canned (Points, NextOffset) tuples.
type Scroller interface {
	ScrollPoints(ctx context.Context, collection, offset string, limit int) (*ScrollPage, error)
}

// AssetIDsLister abstracts SQLite media_assets ID retrieval. The production
// adapter wraps internal/infrastructure/qdrant.SQLiteAssetStore.ListAllAssetIDs.
// Tests inject a stub returning a static slice.
type AssetIDsLister interface {
	ListAllAssetIDs(ctx context.Context) ([]string, error)
}

// Repairer abstracts the side-effects applied on a clean CompleteScan=true
// report. The production implementation iterates OrphanIDs through a Qdrant
// DeletePoints call AND re-emits MissingIDs via the outbox dispatcher. Tests
// inject a counter mock to verify "zero repairs on incomplete scan".
type Repairer interface {
	Apply(ctx context.Context, report *ReconcileReport) error
}

// ScannedPoint is the lean projection of a Qdrant point that the reconciler
// consumes. We deliberately do NOT pull dense vectors or sparse BM25 — only
// PointID + the minimum payload fields required for the missing/orphan set
// computation, the asset_id-missing gate, AND the source_version collated
// for orphan-cleanup event emission.
type ScannedPoint struct {
	PointID       string
	AssetID       string // empty ⇒ condition 5 trips for this point
	Source        string // informational only (logged; not part of repair contract)
	SourceVersion string // captured from payload["source_version"]; empty if absent
}

// ScrollPage is the lean projection of qdrant.ScrollResult. Empty NextOffset
// signals "drain complete"; a non-empty NextOffset means more pages.
type ScrollPage struct {
	Points     []ScannedPoint
	NextOffset string
}

// ReconcileOptions is the operator-facing input to Reconcile. Required fields:
// Collection, AssetIDs, Scroller. MaxPages defaults to 400 (mirrors the
// verifier's safety cap) when zero is passed.
//
// Repairer is the legacy orchestration hook from TODO 8 — it remains an
// options-level field for legacy compatibility. In production the Service
// is typically constructed via NewServiceWithDeps with both Outbox and
// PayloadMutator wired so the production composition layer can author
// its own Repairer that delegates to those ports (clean dependency
// direction; no Service-level orchestration logic duplicated in the
// options bag). For tests that don't need the port-level granularity,
// Repairer is sufficient on its own.
type ReconcileOptions struct {
	// Collection is the Qdrant collection name to reconcile against.
	Collection string

	// MaxPages bounds the scroll loop. 0 == default (400). Negative == 0.
	MaxPages int

	// PageSize is the per-scroll limit. 0 == default (500).
	PageSize int

	// DryRun=true returns the report with no side-effects. DryRun=false
	// applies Repairer.Apply when CompleteScan=true.
	DryRun bool

	// ExpectCollectionNonEmpty toggles condition 4 ("zero points read but
	// collection expected non-empty"). When false, an empty collection
	// is considered a legitimately-drained scan and CompleteScan stays
	// true. Production callers pass true; CI tests that operate on empty
	// colletions pass false.
	ExpectCollectionNonEmpty bool

	// Scroller is the Qdrant scroll adapter (infrastructure in prod).
	Scroller Scroller

	// AssetIDs returns the canonical media_assets SQLite IDs (infrastructure
	// adapter in prod: SQL on media_assets WHERE media_type != 'folder'
	// AND deleted_at IS NULL).
	AssetIDs AssetIDsLister

	// Repairer applies drift-correction. Required when DryRun=false AND
	// (MissingCount > 0 OR OrphanCount > 0). Tests pass a counter mock;
	// production wires the canonical orphan-delete + missing-re-emit
	// implementation from internal/application/qdrant/reconciler.
	Repairer Repairer
}

const (
	defaultMaxPages = 400
	defaultPageSize = 500
)

// ReconcileReport is the full output. CompleteScan is the canonical
// decision-flag for the apply gate; the per-condition sub-flags (PageErrors,
// AssetIDMissingPoints, MaxPagesReached, ZeroPointsUnexpected) are present so
// operators can pinpoint WHY a scan was rejected.
//
// Added in QDRANT-006 (TODO 10, June 2026):
//   - RepairAttempted int — count of items the Service handed to the
//     repair collaborators (Missing + Orphan drift size). Set BEFORE
//     Repairer.Apply is called. Drives the "all attempts completed" gate.
//   - RepairSucceeded int — count of items the Service considers
//     successfully repaired. Set AFTER Repairer.Apply: equals
//     RepairAttempted on a nil-error return, equals 0 on error. Drives
//     the "all attempts succeeded" gate for Applied=true.
//   - Applied stays the operator-facing flag (DTO contract preserved);
//     its truth table now derives from the counters instead of the
//     "Repairer.Apply returned nil" binary, so a future per-item
//     partial-success model only has to refactor the increment site.
type ReconcileReport struct {
	Collection           string
	TotalPages           int
	PageErrors           []error
	AssetIDMissingPoints int  // condition 5: payload missing asset_id (cumulative)
	MaxPagesReached      bool // condition 2: cap hit
	NextOffsetLingering  bool // condition 3: NextOffset still set after cap
	ZeroPointsUnexpected bool // condition 4: ExpectCollectionNonEmpty but 0 scrolled
	PointsScrolled       int
	MissingIDs           []string          // IDs in SQLite NOT in Qdrant (= need re-emit)
	OrphanIDs            []string          // IDs in Qdrant NOT in SQLite (= need delete)
	OrphanSources        map[string]string // orphan asset_id → captured source_version
	MissingCount         int
	OrphanCount          int
	CompleteScan         bool // AND of all conditions
	Errors               []string
	// RepairAttempted / RepairSucceeded (QDRANT-006 TODO 10): repair
	// counters stamped by Service.Apply. Applied is derived from them
	// (Attempted>0 AND Attempted==Succeeded).
	RepairAttempted int
	RepairSucceeded int
	Applied         bool // true iff Repairer.Apply succeeded AND the counter arithmetic holds
}

// Service is the canonical reconciler. Holds the two canonical port
// dependencies (Outbox + PayloadMutator) added in QDRANT-006 TODO 10. The
// zero-port Service returned by NewService() exists for legacy TODO 8 tests
// that author their apply logic through the per-call `Repairer` field —
// those tests are typically dry-run and never trip the port gates.
// Production wiring goes through NewServiceWithDeps which lets composition
// roots verify both ports are non-nil in unit-test fixture mode.
type Service struct {
	outbox  Outbox
	payload PayloadMutator
}

// NewService returns a Service with no Outbox or PayloadMutator wired. This
// is the legacy constructor for tests + dry-run callers; production calls
// NewServiceWithDeps with both ports supplied. A Service returned by
// NewService() applies the port-required guards described in Reconcile's
// apply phase: any non-dry-run call with drift present will return one of
// the two sentinel errors before invoking the per-call Repairer. That is
// the spec-correct behaviour: zero-port Service must NOT silently noop.
func NewService() *Service {
	return &Service{}
}

// ServiceDeps is the constructor bag for NewServiceWithDeps. Both fields
// are optional (zero-value = port absent); the Service is the same as
// `NewService()` plus this bag. Production composition should pass
// non-nil values; tests may pass nil for one or both to exercise the
// sentinel-error paths.
type ServiceDeps struct {
	// Outbox — nil ⇒ ErrOutboxRequired when missing IDs need re-emit
	// in non-dry-run. Production wires *outbox.Dispatcher.
	Outbox Outbox
	// PayloadMutator — nil ⇒ ErrPayloadMutatorRequired when orphan
	// IDs need Qdrant-side delete in non-dry-run. Production wires
	// *qdrant.IndexWriter.
	PayloadMutator PayloadMutator
}

// NewServiceWithDeps returns a Service with both ports wired. The shape
// matches the production composition layer calling
// `reconciler.NewServiceWithDeps(reconciler.ServiceDeps{Outbox: d, PayloadMutator: iw})`
// — no shim layer, no adapter struct. The constructor does NOT verify
// non-nil (that is Reconcile's job) so the test fixtures can construct
// any subset of ports they want to exercise.
func NewServiceWithDeps(deps ServiceDeps) *Service {
	return &Service{
		outbox:  deps.Outbox,
		payload: deps.PayloadMutator,
	}
}

// Reconcile is the canonical entry point. The flow is:
//
//  1. Pull SQLite IDs (single query).
//  2. Scroll Qdrant with paging, bounded by MaxPages.
//  3. Each iteration processes Points (count asset-IDs, surface missing-asset-id),
//     advances NextOffset, and detects the cap-hit on the LAST allowed iteration.
//  4. After loop: compute missing/orphan sets; AND the 5 conditions into
//     CompleteScan; assert the dry-run / apply gate.
//  5. If DryRun=false AND CompleteScan=true AND (Missing>0 OR Orphan>0),
//     invoke the per-call Repairer AND enforce the port-side guards (TODO 10).
//
// QDRANT-006 (TODO 10) apply-phase guard order:
//
//	a. Repairer=nil fails-fast with the TODO 8 fail-fast error (legacy).
//	b. Outbox=nil ALWAYS triggers ErrOutboxRequired (regardless of drift
//	   direction). Spec-literal reading: "Apply senza outbox/payload
//	   rifiutato esplicitamente" ⇒ both ports are explicit apply
//	   dependencies. Production wires both regardless of drift direction;
//	   the strict gate prevents silent fallback if either port is dropped
//	   from a future composition root.
//	c. PayloadMutator=nil ALWAYS triggers ErrPayloadMutatorRequired.
//	d. Set RepairAttempted = MissingCount + OrphanCount.
//	e. Call Repairer.Apply; if it returns an error, RepairSucceeded=0
//	   and the error propagates with a wrapped count for operator triage.
//	f. If Repairer.Apply succeeds, RepairSucceeded = RepairAttempted.
//	g. Applied = RepairAttempted > 0 AND RepairSucceeded == RepairAttempted.
//
// Steps (b), (c), (d), (f), (g) are the TODO 10 additions. Dry-run mode
// short-circuits BEFORE these gates so ports are allowed nil there.
//
// Any unrecoverable scroll error is captured as PageErrors and breaks the
// loop; the report is still returned (advisory evidence of partial scan).
// Apply is gated on CompleteScan.
func (s *Service) Reconcile(ctx context.Context, opts ReconcileOptions) (*ReconcileReport, error) {
	if opts.Collection == "" {
		return nil, errors.New("reconcile: collection is required")
	}
	if opts.Scroller == nil {
		return nil, errors.New("reconcile: Scroller is required")
	}
	if opts.AssetIDs == nil {
		return nil, errors.New("reconcile: AssetIDs is required")
	}

	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = defaultMaxPages
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}

	report := &ReconcileReport{Collection: opts.Collection}

	// Step 1 — SQLite canonical IDs.
	sqliteIDs, err := opts.AssetIDs.ListAllAssetIDs(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("list SQLite IDs: %v", err))
		return report, fmt.Errorf("reconcile scan incomplete: failed to list SQLite IDs: %w", err)
	}
	sqliteSet := make(map[string]bool, len(sqliteIDs))
	for _, id := range sqliteIDs {
		sqliteSet[id] = true
	}

	// Step 2 — Scroll Qdrant with paging.
	qdrantIDs := make(map[string]bool)
	qdrantPoints := make(map[string]ScannedPoint)
	var offset string

	for iteration := 0; iteration < maxPages; iteration++ {
		report.TotalPages++

		page, err := opts.Scroller.ScrollPoints(ctx, opts.Collection, offset, pageSize)
		if err != nil {
			// Condition 1: a scroll page fails. We accumulate the error
			// and break — operators get the partial report. CompleteScan
			// will be false below.
			report.PageErrors = append(report.PageErrors, err)
			report.Errors = append(report.Errors,
				fmt.Sprintf("scroll page %d: %v", iteration, err))
			break
		}

		// Defensive: nil page is treated as terminal-empty. Real Qdrant
		// never returns nil here (it returns an empty Pages slice + empty
		// NextOffset), but we guard so a buggy adapter doesn't crash the loop.
		if page == nil {
			break
		}

		for _, pt := range page.Points {
			report.PointsScrolled++
			if pt.AssetID == "" {
				// Condition 5: payload missing asset_id. We skip the
				// point from the qdrantIDs set so it can't masquerade as
				// an orphan-of-empty-asset-id.
				report.AssetIDMissingPoints++
				report.Errors = append(report.Errors,
					fmt.Sprintf("point %q: missing asset_id in payload", pt.PointID))
				continue
			}
			qdrantIDs[pt.AssetID] = true
			qdrantPoints[pt.AssetID] = pt
		}

		if page.NextOffset == "" {
			break // drain complete
		}
		offset = page.NextOffset

		// Detect cap-hit+linger BEFORE the next iteration tries to read.
		// iteration+1 is the index of the NEXT scroll attempt; if it would
		// equal or exceed maxPages, the drain stops here with NextOffset
		// still set — conditions 2 AND 3 both fire.
		if iteration+1 >= maxPages {
			report.MaxPagesReached = true
			report.NextOffsetLingering = true
			report.Errors = append(report.Errors,
				fmt.Sprintf("max pages reached (%d) with NextOffset set; remaining points NOT drained", maxPages))
			break
		}
	}

	// Condition 4: collection expected non-empty but zero points read.
	if opts.ExpectCollectionNonEmpty && report.PointsScrolled == 0 {
		report.ZeroPointsUnexpected = true
		report.Errors = append(report.Errors,
			"zero points scrolled but collection expected non-empty")
	}

	// Compute missing/orphan sets. We sort for stable test diffs.
	for sqliteID := range sqliteSet {
		if !qdrantIDs[sqliteID] {
			report.MissingIDs = append(report.MissingIDs, sqliteID)
		}
	}
	for qdrantID := range qdrantIDs {
		if !sqliteSet[qdrantID] {
			report.OrphanIDs = append(report.OrphanIDs, qdrantID)
		}
	}
	sort.Strings(report.MissingIDs)
	sort.Strings(report.OrphanIDs)
	if report.OrphanCount > 0 && report.OrphanSources == nil {
		report.OrphanSources = make(map[string]string, report.OrphanCount)
	}
	// Capture orphan source_versions in a single post-loop pass. The
	// source_version is the live Qdrant payload value, NOT a placeholder;
	// it's part of the reconcile_delete event_key for orphan cleanup.
	for _, orphan := range report.OrphanIDs {
		if pt, ok := qdrantPoints[orphan]; ok && pt.SourceVersion != "" {
			report.OrphanSources[orphan] = pt.SourceVersion
		}
	}
	report.MissingCount = len(report.MissingIDs)
	report.OrphanCount = len(report.OrphanIDs)

	// CompleteScan = AND of all 5 fail conditions. Each sub-flag is a hard
	// "fail-closed" signal: any one of them blocks the apply phase.
	report.CompleteScan =
		len(report.PageErrors) == 0 &&
			!report.MaxPagesReached &&
			!report.NextOffsetLingering &&
			!report.ZeroPointsUnexpected &&
			report.AssetIDMissingPoints == 0

	// Decisional rule.
	if !report.CompleteScan && !opts.DryRun {
		return report, fmt.Errorf("%w: %d error(s); see report.Errors",
			ErrRefusingApply, len(report.Errors))
	}

	// Apply stage. Dry-run returns the report as-is. Non-dry-run requires
	// both the per-call Repairer (legacy TODO 8) AND the two port dependencies
	// (QDRANT-006 TODO 10) if drift is present in the relevant direction.
	if !opts.DryRun && (report.MissingCount > 0 || report.OrphanCount > 0) {
		// Step (a) — per-call Repairer fail-fast (TODO 8 invariant).
		// Kept FIRST so existing tests that expected the
		// "Repairer is required" error string still match.
		if opts.Repairer == nil {
			return report, errors.New("reconcile: Repairer is required for non-dry-run apply with drift present")
		}

		// Step (b) — QDRANT-006 TODO 10 port guard (Outbox). Applied
		// UNCONDITIONALLY per the spec literal: "apply senza outbox
		// rifiutato esplicitamente". A directional guard (only check
		// when MissingCount > 0) would let orphan-only drift sneak
		// through with outbox=nil — contradicts the canonical reading
		// of "both dependencies required for apply". Stamping this
		// gate ahead of the payload gate means outbox errors surface
		// first (operators see a stable ordering in the dashboard).
		if s.outbox == nil {
			return report, ErrOutboxRequired
		}

		// Step (c) — QDRANT-006 TODO 10 port guard (PayloadMutator).
		// Same unconditional contract; if outbox is statically
		// configured but payload was dropped on a refactor, this
		// surfaces it explicitly.
		if s.payload == nil {
			return report, ErrPayloadMutatorRequired
		}

		// Step (d) — stamp the attempted counter before Repairer.Apply.
		// Attempted == full drift size at this point.
		report.RepairAttempted = report.MissingCount + report.OrphanCount

		// Step (e) — invoke Repairer.Apply. Errors propagate with a
		// count-wrapped message so operator triage sees both the
		// underlying error AND how many repairs were lost.
		if err := opts.Repairer.Apply(ctx, report); err != nil {
			report.RepairSucceeded = 0
			return report, fmt.Errorf("reconcile: repair failed (%d attempted, %d succeeded): %w",
				report.RepairAttempted, report.RepairSucceeded, err)
		}

		// Step (f) — Repairer.Apply succeeded: all items considered
		// repaired (binary model — see comment above ReconcileReport).
		report.RepairSucceeded = report.RepairAttempted

		// Step (g) — Applied truth table. Two preconditions now hold:
		// ≥1 item was attempted AND every attempted item succeeded.
		report.Applied = report.RepairAttempted > 0 &&
			report.RepairSucceeded == report.RepairAttempted
	}

	return report, nil
}
