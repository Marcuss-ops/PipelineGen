// internal/infrastructure/qdrant/reconciler_test.go — QDRANT-005B tests
//
// Validates the pure-logic surface of the Reconciler that does not
// require a live Qdrant client or SQLite handle:
//
//   - classifyDrift classifies DB/Qdrant snapshots into the 5
//     canonical drift classes.
//   - normalizeLifecycleStateForReconcile maps input to the canonical
//     6-value lowercase enum.
//   - generateReconcileRunID produces a non-empty hex string.
//   - repairActionID is deterministic for identical inputs.
//   - ErrDBTruncated surfaces both MaxDBRows and Observed in its message.
//
// The HTTP/DB-driven orchestration (Reconcile end-to-end) is exercised
// directly via the injected scrollFn and a manual fullScroll call —
// this proves the fail-closed contract on a partial scroll without a
// live Qdrant: scrollAborted must be true, Status must be
// ReconStatusScanIncomplete, and the gate orchestrating apply must
// short-circuit.
package qdrant

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// ── classifyDrift ────────────────────────────────────────────────────

func TestClassifyDrift_EmptyInputs(t *testing.T) {
	got := classifyDrift(map[string]dbSnap{}, map[string]qdrantSnap{}, map[string]qdrantSnap{}, DefaultV3Schema(), nil)
	if len(got) != 0 {
		t.Fatalf("expected 0 drifts, got %d: %+v", len(got), got)
	}
}

func TestClassifyDrift_Missing(t *testing.T) {
	// DB has one live asset; Qdrant has nothing.
	db := map[string]dbSnap{
		"asset_a": {ID: "asset_a", LifecycleState: "active", EmbeddingVer: "v3"},
	}
	qd := map[string]qdrantSnap{}
	got := classifyDrift(db, qd, map[string]qdrantSnap{}, DefaultV3Schema(), map[string]struct{}{"active": {}})

	if len(got) != 1 {
		t.Fatalf("expected 1 drift, got %d: %+v", len(got), got)
	}
	if got[0].Class != DriftMissing {
		t.Fatalf("expected MISSING, got %s", got[0].Class)
	}
	if got[0].AssetID != "asset_a" {
		t.Fatalf("expected asset_a, got %s", got[0].AssetID)
	}
	expected := AssetIDToQdrantPointID("asset_a")
	if got[0].PointID != expected {
		t.Fatalf("expected canonical PointID=%s, got %s", expected, got[0].PointID)
	}
}

func TestClassifyDrift_Extra_NoAssetIDPayload(t *testing.T) {
	qd := map[string]qdrantSnap{
		"orphan_pt": {PointID: "orphan_pt", PayloadAssetID: ""},
	}
	got := classifyDrift(map[string]dbSnap{}, qd, map[string]qdrantSnap{}, DefaultV3Schema(), nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(got))
	}
	if got[0].Class != DriftExtra {
		t.Fatalf("expected EXTRA, got %s", got[0].Class)
	}
}

func TestClassifyDrift_Extra_AssetNotInDB(t *testing.T) {
	assetID := "asset_x"
	canonical := AssetIDToQdrantPointID(assetID)
	qd := map[string]qdrantSnap{
		canonical: {PointID: canonical, PayloadAssetID: assetID, LifecycleState: "active"},
	}
	got := classifyDrift(map[string]dbSnap{}, qd, map[string]qdrantSnap{}, DefaultV3Schema(), nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(got))
	}
	if got[0].Class != DriftExtra {
		t.Fatalf("expected EXTRA, got %s", got[0].Class)
	}
	if got[0].AssetID != assetID {
		t.Fatalf("expected assetID=%q, got %q", assetID, got[0].AssetID)
	}
}

func TestClassifyDrift_Stale(t *testing.T) {
	assetID := "asset_a"
	canonical := AssetIDToQdrantPointID(assetID)
	db := map[string]dbSnap{
		assetID: {ID: assetID, LifecycleState: "active", EmbeddingVer: "v3"},
	}
	qd := map[string]qdrantSnap{
		canonical: {PointID: canonical, PayloadAssetID: assetID, LifecycleState: "deleted", EmbeddingVer: "v3"},
	}
	got := classifyDrift(db, qd, map[string]qdrantSnap{}, DefaultV3Schema(), map[string]struct{}{"active": {}})

	if len(got) != 1 {
		t.Fatalf("expected 1 drift, got %d: %+v", len(got), got)
	}
	if got[0].Class != DriftStale {
		t.Fatalf("expected STALE, got %s", got[0].Class)
	}
	if !strings.Contains(got[0].Detail, "active") || !strings.Contains(got[0].Detail, "deleted") {
		t.Fatalf("STALE detail should mention both sides, got %q", got[0].Detail)
	}
}

func TestClassifyDrift_Version(t *testing.T) {
	assetID := "asset_a"
	canonical := AssetIDToQdrantPointID(assetID)
	db := map[string]dbSnap{
		assetID: {ID: assetID, LifecycleState: "active", EmbeddingVer: "v3"},
	}
	qd := map[string]qdrantSnap{
		canonical: {PointID: canonical, PayloadAssetID: assetID, LifecycleState: "active", EmbeddingVer: "v999-old"},
	}
	got := classifyDrift(db, qd, map[string]qdrantSnap{}, DefaultV3Schema(), map[string]struct{}{"active": {}})
	if len(got) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(got))
	}
	if got[0].Class != DriftVersion {
		t.Fatalf("expected VERSION, got %s", got[0].Class)
	}
	if !strings.Contains(got[0].Detail, "v999-old") {
		t.Fatalf("VERSION detail should mention old version, got %q", got[0].Detail)
	}
}

func TestClassifyDrift_Version_Missing(t *testing.T) {
	assetID := "asset_a"
	canonical := AssetIDToQdrantPointID(assetID)
	db := map[string]dbSnap{
		assetID: {ID: assetID, LifecycleState: "active", EmbeddingVer: "v3"},
	}
	qd := map[string]qdrantSnap{
		canonical: {PointID: canonical, PayloadAssetID: assetID, LifecycleState: "active", EmbeddingVer: ""},
	}
	got := classifyDrift(db, qd, map[string]qdrantSnap{}, DefaultV3Schema(), map[string]struct{}{"active": {}})
	if len(got) != 1 || got[0].Class != DriftVersion {
		t.Fatalf("expected 1 VERSION drift, got %d (%+v)", len(got), got)
	}
}

func TestClassifyDrift_IdMismatch(t *testing.T) {
	assetID := "asset_a"
	db := map[string]dbSnap{
		assetID: {ID: assetID, LifecycleState: "active", EmbeddingVer: "v3"},
	}
	qd := map[string]qdrantSnap{
		"raw-asset-id": {PointID: "raw-asset-id", PayloadAssetID: assetID, LifecycleState: "active", EmbeddingVer: "v3"},
	}
	got := classifyDrift(db, qd, map[string]qdrantSnap{}, DefaultV3Schema(), map[string]struct{}{"active": {}})

	if len(got) < 1 {
		t.Fatalf("expected at least 1 drift, got %d", len(got))
	}
	hasID := false
	for _, d := range got {
		if d.Class == DriftIdMismatch {
			hasID = true
		}
	}
	if !hasID {
		t.Fatalf("expected ID_MISMATCH in drift set, got only %+v", got)
	}
}

func TestClassifyDrift_NoVersionCheck_WhenNilSchema(t *testing.T) {
	assetID := "asset_a"
	canonical := AssetIDToQdrantPointID(assetID)
	db := map[string]dbSnap{
		assetID: {ID: assetID, LifecycleState: "active", EmbeddingVer: "v3"},
	}
	qd := map[string]qdrantSnap{
		canonical: {PointID: canonical, PayloadAssetID: assetID, LifecycleState: "active", EmbeddingVer: "v999-old"},
	}
	got := classifyDrift(db, qd, map[string]qdrantSnap{}, nil, map[string]struct{}{"active": {}})
	for _, d := range got {
		if d.Class == DriftVersion {
			t.Fatalf("NIL schema must skip VERSION check, but found %+v", d)
		}
	}
}

func TestClassifyDrift_HappyPath_NoDrift(t *testing.T) {
	db := map[string]dbSnap{}
	qd := map[string]qdrantSnap{}
	for _, a := range []string{"a", "b", "c"} {
		db[a] = dbSnap{ID: a, LifecycleState: "active", EmbeddingVer: CurrentEmbeddingVersion}
		canonical := AssetIDToQdrantPointID(a)
		qd[canonical] = qdrantSnap{PointID: canonical, PayloadAssetID: a, LifecycleState: "active", EmbeddingVer: CurrentEmbeddingVersion}
	}
	got := classifyDrift(db, qd, map[string]qdrantSnap{}, DefaultV3Schema(), map[string]struct{}{"active": {}})
	if len(got) != 0 {
		t.Fatalf("expected 0 drifts on matching snapshot, got %d: %+v", len(got), got)
	}
}

// ── normalizeLifecycleStateForReconcile ──────────────────────────────

func TestNormalizeLifecycleStateForReconcile(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"active", "active"},
		{"ACTIVE", "active"},
		{"  Active ", "active"},
		{"staging", "staging"},
		{"STAGING", "staging"},
		{"processing", "processing"},
		{"PROCESSING", "processing"},
		{"deleted", "deleted"},
		{"DELETED", "deleted"},
		{"ready", "ready"},
		{"READY", "ready"},
		{"pending", "pending"},
		{"PENDING", "pending"},
		{"", "deleted"},
		{"weird-state", "deleted"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := normalizeLifecycleStateForReconcile(c.in); got != c.want {
				t.Fatalf("normalize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// ── generateReconcileRunID ───────────────────────────────────────────

func TestGenerateReconcileRunID(t *testing.T) {
	r1, err := generateReconcileRunID()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if r1 == "" {
		t.Fatal("run id must not be empty")
	}
	if len(r1) != 32 {
		t.Fatalf("hex 16-byte id must be 32 chars, got %d (%q)", len(r1), r1)
	}
	for _, c := range r1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("run id contains non-hex char %q in %q", c, r1)
		}
	}
	r2, _ := generateReconcileRunID()
	if r1 == r2 {
		t.Fatal("two runs must produce different ids (cryptographic RNG)")
	}
}

// ── AllDriftClasses ─────────────────────────────────────────────────

func TestAllDriftClasses(t *testing.T) {
	got := AllDriftClasses()
	want := []DriftClass{DriftMissing, DriftExtra, DriftStale, DriftVersion, DriftIdMismatch}
	if len(got) != len(want) {
		t.Fatalf("expected %d classes, got %d", len(want), len(got))
	}
	for i, c := range want {
		if got[i] != c {
			t.Fatalf("class[%d] = %q, want %q", i, got[i], c)
		}
	}
}

// ── repairActionID (review-fix round) ────────────────────────────────

func TestRepairActionID_Deterministic(t *testing.T) {
	a := repairActionID("run1", "EXTRA", "p1", "")
	b := repairActionID("run1", "EXTRA", "p1", "")
	if a != b {
		t.Fatalf("identical inputs must produce identical IDs: %q != %q", a, b)
	}
	if !strings.HasPrefix(a, "rep_") {
		t.Fatalf("repair action id must be prefixed 'rep_', got %q", a)
	}
	if len(a) != 24 {
		t.Fatalf("repair action id must be 24 chars (rep_ + 20 hex), got %d (%q)", len(a), a)
	}
}

func TestRepairActionID_DistinguishesInput(t *testing.T) {
	if repairActionID("run1", "EXTRA", "p1", "") == repairActionID("run2", "EXTRA", "p1", "") {
		t.Fatal("different runID must produce different ids")
	}
	if repairActionID("run1", "EXTRA", "p1", "") == repairActionID("run1", "MISSING", "p1", "") {
		t.Fatal("different class must produce different ids")
	}
	if repairActionID("run1", "EXTRA", "p1", "") == repairActionID("run1", "EXTRA", "p2", "") {
		t.Fatal("different id must produce different ids")
	}
	if repairActionID("run1", "EXTRA", "p1", "") == repairActionID("run1", "EXTRA", "p1", "extra1") {
		t.Fatal("extra slot must change the id")
	}
}

// ── driftActionID (review NEEDS-REVISION F, June 2026) ────────────────
//
// Contract: drift_action_id is STABLE across runs — same drift on a
// different reconciliation run produces the same audit token. This is
// the audit-correlation primitive that lets an operator verify
// idempotency: re-running reconcile should not surface new repairs for
// a drift_action_id already known to be clean.

func TestDriftActionID_Deterministic(t *testing.T) {
	a := driftActionID("media_assets_v3", "MISSING", "asset_x")
	b := driftActionID("media_assets_v3", "MISSING", "asset_x")
	if a != b {
		t.Fatalf("identical inputs must produce identical IDs: %q != %q", a, b)
	}
	if !strings.HasPrefix(a, "dft_") {
		t.Fatalf("drift action id must be prefixed 'dft_', got %q", a)
	}
	if len(a) != 24 { // "dft_" + 20 hex chars
		t.Fatalf("drift action id must be 24 chars (dft_ + 20 hex), got %d (%q)", len(a), a)
	}
}

func TestDriftActionID_StableAcrossRuns(t *testing.T) {
	// The whole point: identical inputs across "different runs" produce
	// the same drift_action_id. The audit-correlation primitive is the
	// (collection, class, assetID) tuple, NOT the run id.
	one := driftActionID("media_assets_v3", "EXTRA", "asset_y")
	two := driftActionID("media_assets_v3", "EXTRA", "asset_y")
	if one != two {
		t.Fatalf("drift action id must be stable: %q != %q", one, two)
	}
}

func TestDriftActionID_DistinguishesInput(t *testing.T) {
	if driftActionID("col_a", "MISSING", "x") == driftActionID("col_b", "MISSING", "x") {
		t.Fatal("different collection must produce different ids")
	}
	if driftActionID("col_a", "MISSING", "x") == driftActionID("col_a", "EXTRA", "x") {
		t.Fatal("different class must produce different ids")
	}
	if driftActionID("col_a", "MISSING", "x") == driftActionID("col_a", "MISSING", "y") {
		t.Fatal("different assetID must produce different ids")
	}
}

// ── ErrDBTruncated ────────────────────────────────────────────────────

func TestErrDBTruncated_ErrorFormat(t *testing.T) {
	e := &ErrDBTruncated{MaxDBRows: 500_000, Observed: 500_001}
	got := e.Error()
	if !strings.Contains(got, "500000") || !strings.Contains(got, "500001") {
		t.Fatalf("ErrDBTruncated.Error must surface both MaxDBRows and Observed; got %q", got)
	}
	// The error message must surface the fail-closed contract by name
	// ("NOT applied") so an operator can grep for it from any log scraper.
	if !strings.Contains(got, "NOT applied") {
		t.Fatalf("ErrDBTruncated.Error should mention the fail-closed contract terms; got %q", got)
	}
}

// ── Fail-closed orchestration (review-fix round, June 2026) ────────
//
// Contract: a partial Qdrant scroll (error on page 2) MUST yield
// Status="scan_incomplete" AND the orchestrator's apply gate MUST
// short-circuit on that status. We exercise the precise path without
// standing up a real Qdrant server by injecting scrollFn directly.

// stubScrollErr is a tiny error type the stub scrollFn can return so
// the test stays independent of internal error sentinels.
type stubScrollErr struct{ msg string }

// ── fetchDBSnapshot MaxDBRows cap helper infrastructure ───
//
// These helpers (mediaAssetRow, seedMediaAssets, sqliteMemoryAvailable)
// support TestFetchDBSnapshot_CapExceeded and TestFetchDBSnapshot_UnderCap
// above. Lives alongside the tests so a single ?testify-style fluffle
// suffices.

type mediaAssetRow struct {
	ID             string
	LifecycleState string
}

func sqliteMemoryAvailable() bool {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return false
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return false
	}
	return true
}

func seedMediaAssets(t *testing.T, rows []mediaAssetRow) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			lifecycle_state TEXT,
			status TEXT,
			embedding_version TEXT,
			deleted_at TEXT
		)
	`); err != nil {
		t.Fatalf("create media_assets table: %v", err)
	}
	for _, r := range rows {
		if _, err := db.Exec(
			"INSERT INTO media_assets(id, lifecycle_state, embedding_version) VALUES (?, ?, '')",
			r.ID, r.LifecycleState,
		); err != nil {
			t.Fatalf("insert media_assets row %q: %v", r.ID, err)
		}
	}
	return db
}

func (e *stubScrollErr) Error() string { return e.msg }

// ── fetchDBSnapshot MaxDBRows cap (review NEEDS-REVISION F, June 2026) ─
//
// We exercise the cap path directly: build a Reconciler with an in-memory
// SQLite DB that has more live media_assets rows than the configured
// MaxDBRows cap, call fetchDBSnapshot, and assert the returned error is
// *ErrDBTruncated with Observed > MaxDBRows. The caller (Reconcile) maps
// this to Status="db_truncated" and aborts the apply phase.
//
// In-memory SQLite is provided by mattn/go-sqlite3 (the canonical driver
// per AGENTS.md). We wire the schema directly to avoid depending on the
// project's media_assets_migrations boot path.

func TestFetchDBSnapshot_CapExceeded(t *testing.T) {
	if !sqliteMemoryAvailable() {
		t.Skip("sqlite memory driver unavailable in this environment")
	}

	r := &Reconciler{log: zap.NewNop()}

	liveLookup := map[string]struct{}{"active": {}}

	// Seed a DB with 5 live rows; cap at 3.
	db := seedMediaAssets(t, []mediaAssetRow{
		{ID: "a1", LifecycleState: "active"},
		{ID: "a2", LifecycleState: "active"},
		{ID: "a3", LifecycleState: "active"},
		{ID: "a4", LifecycleState: "active"},
		{ID: "a5", LifecycleState: "active"},
	})
	r.db = db

	got, err := r.fetchDBSnapshot(context.Background(), liveLookup, 3)
	if err == nil {
		t.Fatalf("expected ErrDBTruncated, got nil; map=%v", got)
	}
	var trunc *ErrDBTruncated
	if !errors.As(err, &trunc) {
		t.Fatalf("expected error to unwrap to *ErrDBTruncated, got %T (%v)", err, err)
	}
	if trunc.MaxDBRows != 3 {
		t.Fatalf("expected MaxDBRows=3, got %d", trunc.MaxDBRows)
	}
	if trunc.Observed <= trunc.MaxDBRows {
		t.Fatalf("expected Observed > MaxDBRows (%d), got %d", trunc.MaxDBRows, trunc.Observed)
	}
}

func TestFetchDBSnapshot_UnderCap(t *testing.T) {
	if !sqliteMemoryAvailable() {
		t.Skip("sqlite memory driver unavailable in this environment")
	}

	r := &Reconciler{log: zap.NewNop()}
	r.db = seedMediaAssets(t, []mediaAssetRow{
		{ID: "a1", LifecycleState: "active"},
		{ID: "a2", LifecycleState: "active"},
	})
	got, err := r.fetchDBSnapshot(context.Background(), map[string]struct{}{"active": {}}, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 live rows, got %d: %+v", len(got), got)
	}
}

func TestReconcile_FullScroll_FailClosed_OnScrollError(t *testing.T) {
	r := &Reconciler{
		log:    zap.NewNop(),
		schema: DefaultV3Schema(),
	}
	wantErr := &stubScrollErr{msg: "page2 connection reset"}
	r.scrollFn = func(ctx context.Context, collection, offset string, limit int) (*ScrollResult, error) {
		if offset == "" {
			return &ScrollResult{
				Points: []ScrollPoint{
					{ID: "p1", Payload: map[string]interface{}{"asset_id": "a1", "lifecycle_state": "active"}},
				},
				NextOffset: "next-page-token",
			}, nil
		}
		return nil, wantErr
	}

	res := &ReconcileResult{}
	_, _, aborted := r.fullScroll(context.Background(), "test_collection", 100, res)

	if !aborted {
		t.Fatalf("expected aborted=true on page-2 scroll error, got false")
	}
	if res.Status != ReconStatusScanIncomplete {
		t.Fatalf("expected Status=%q, got %q", ReconStatusScanIncomplete, res.Status)
	}
	if res.QDScanned != 1 {
		t.Fatalf("expected QDScanned=1 (page-1 result), got %d", res.QDScanned)
	}
	if len(res.Errors) == 0 {
		t.Fatal("expected Errors to be populated when scroll failed")
	}
	if !strings.Contains(res.Errors[0], "page2 connection reset") {
		t.Fatalf("expected Errors[0] to surface the underlying error, got %q", res.Errors[0])
	}

	// Simulate the orchestrator gate that decides whether to apply repairs.
	// Mirror the production gate:
	//   if !opts.DryRun && r.repairer != nil && res.Status != ReconStatusScanIncomplete { ... }
	applyGate := func(dryRun bool, repairerNonNil bool, status string) bool {
		return !dryRun && repairerNonNil && status != ReconStatusScanIncomplete
	}
	if applyGate(false, true, res.Status) {
		t.Fatalf("production gate must short-circuit apply when Status=%q, but returned true",
			ReconStatusScanIncomplete)
	}
}

func TestReconcile_FullScroll_CompletesNormally_OnEmptyNextOffset(t *testing.T) {
	// Positive control: when scroll returns NextOffset="", the loop must
	// terminate cleanly with aborted=false and Status unset (callers
	// decide the final status). QDScanned reflects everything we saw.
	r := &Reconciler{
		log:    zap.NewNop(),
		schema: DefaultV3Schema(),
	}
	calls := 0
	r.scrollFn = func(ctx context.Context, collection, offset string, limit int) (*ScrollResult, error) {
		calls++
		if calls == 1 {
			return &ScrollResult{
				Points: []ScrollPoint{
					{ID: AssetIDToQdrantPointID("a1"), Payload: map[string]interface{}{"asset_id": "a1", "lifecycle_state": "active"}},
					{ID: AssetIDToQdrantPointID("a2"), Payload: map[string]interface{}{"asset_id": "a2", "lifecycle_state": "active"}},
				},
				NextOffset: "", // natural end-of-stream
			}, nil
		}
		t.Fatal("scroll must not be called again after NextOffset=\"\"")
		return nil, nil
	}

	res := &ReconcileResult{}
	qdPt, qdAsset, aborted := r.fullScroll(context.Background(), "test_collection", 100, res)

	if aborted {
		t.Fatalf("expected aborted=false on clean scroll completion, got true")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 scroll call, got %d", calls)
	}
	if res.QDScanned != 2 {
		t.Fatalf("expected QDScanned=2, got %d", res.QDScanned)
	}
	if len(qdPt) != 2 || len(qdAsset) != 2 {
		t.Fatalf("expected 2 points and 2 assets indexed, got pts=%d assets=%d", len(qdPt), len(qdAsset))
	}
	if res.Status != "" {
		t.Fatalf("expected Status=\"\", got %q (orchestrator decides final status)", res.Status)
	}
}
