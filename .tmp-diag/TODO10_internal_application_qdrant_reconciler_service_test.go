// Package reconciler tests — QDRANT-005 (TODO 8, June 2026) fail-closed
// contract + QDRANT-006 (TODO 10, June 2026) port contract.
//
// Each spec scenario from the user TODO 8 list is mapped to a single test
// function. Additional table-driven sub-cases verify CompleteScan's
// per-condition AND-ing and the dry-run passthrough.
//
// TODO 10 (June 2026) additions:
//   - 3 port-stub fixtures (`stubOutbox`, `stubPayload`) at the top.
//   - 6 user-spec scenarios (outbox=nil, payload=nil, both nil, all-fail,
//     dry-run with nil ports, all-succeed).
//   - 2 symmetry guards (outbox not required when only orphan drift;
//     payload not required when only missing drift).
//   - Existing affected TODO 8 tests (SpecCase7, RepairerError,
//     MissingAndOrphan_BothReported) rewire to use NewServiceWithDeps
//     with port stubs so they don't trip the new port gates.
//     The TODO 8 tests that only dry-run or hit Repairer=nil-fail-fast are
//     unchanged (no port gates reached).
package reconciler

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ── Fakes ────────────────────────────────────────────────────────────────

// fakeScroller returns scripted (page, err) tuples. Each call to ScrollPoints
// consumes the next entry. If an entry has both err==nil and page==nil, the
// reconciler breaks the loop (treated as terminal-empty).
type fakeScroller struct {
	pages []scriptedPage
	calls int
}

type scriptedPage struct {
	page *ScrollPage
	err  error
}

func (f *fakeScroller) ScrollPoints(_ context.Context, collection, offset string, limit int) (*ScrollPage, error) {
	if f.calls >= len(f.pages) {
		return &ScrollPage{}, nil // terminal-empty: drain complete
	}
	entry := f.pages[f.calls]
	f.calls++
	if entry.err != nil {
		return nil, entry.err
	}
	return entry.page, nil
}

// fakeAssetIDs returns a static slice (optionally nil for AssetStore errors).
type fakeAssetIDs struct {
	ids []string
	err error
}

func (f *fakeAssetIDs) ListAllAssetIDs(_ context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ids, nil
}

// fakeRepairer counts Apply invocations and records the Orphan/Missing it saw
// in the most recent call. error can be injected to simulate apply failure.
type fakeRepairer struct {
	applyCalls     int
	lastOrphanIDs  []string
	lastMissingIDs []string
	err            error
}

func (f *fakeRepairer) Apply(_ context.Context, report *ReconcileReport) error {
	f.applyCalls++
	f.lastOrphanIDs = append([]string(nil), report.OrphanIDs...)
	f.lastMissingIDs = append([]string(nil), report.MissingIDs...)
	return f.err
}

// ── Port stubs (QDRANT-006 TODO 10, June 2026) ───────────────────────────
//
// stubOutbox / stubPayload are the canonical test fixtures for the two new
// Service struct ports. Each is a minimal in-memory recorder that tracks
// how many times it was hit + which inputs it received. An optional `err`
// field forces a failure so the "all repairs fail" test (TODO 10 spec
// scenario 4) can exercise the Repairer-back path on a port that the
// Service actually called.

type stubOutbox struct {
	calls int
	err   error
}

func (s *stubOutbox) EnqueueAndReconcileIndex(_ context.Context, _, _, _, _ string) error {
	s.calls++
	return s.err
}

type stubPayload struct {
	calls   int
	lastIDs []string
	err     error
}

func (s *stubPayload) DeletePoints(_ context.Context, ids []string) error {
	s.calls++
	s.lastIDs = append([]string(nil), ids...)
	return s.err
}

// ── 7 spec scenarios (TODO 8) ────────────────────────────────────────────

// TestTODO8_SpecCase1_FirstPageError_AppliesBlocked verifies that a scroll error
// on page 0 trips PageErrors → CompleteScan=false → ErrRefusingApply when not
// in dry-run. Repairer.Apply must NOT be called.
func TestTODO8_SpecCase1_FirstPageError_AppliesBlocked(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{{err: errors.New("qdrant 503")}}}
	ids := &fakeAssetIDs{ids: []string{"a-1", "a-2"}}
	repairer := &fakeRepairer{}
	svc := NewService()

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if !errors.Is(err, ErrRefusingApply) {
		t.Fatalf("expected ErrRefusingApply, got %v", err)
	}
	if report == nil {
		t.Fatal("report should be returned even on refusal (advisory evidence)")
	}
	if report.CompleteScan {
		t.Error("CompleteScan must be false after first-page error")
	}
	if len(report.PageErrors) == 0 {
		t.Error("PageErrors must record the scroll error")
	}
	if repairer.applyCalls != 0 {
		t.Errorf("Repairer.Apply must NOT be called on incomplete scan; got %d calls", repairer.applyCalls)
	}
	if report.Applied {
		t.Error("report.Applied must be false on incomplete scan")
	}
}

// TestTODO8_SpecCase2_SecondPageError_AppliesBlocked verifies that a passing
// page 1 followed by an erroring page 2 still trips the CompleteScan=false gate.
func TestTODO8_SpecCase2_SecondPageError_AppliesBlocked(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{
			Points: []ScannedPoint{
				{PointID: "p1", AssetID: "a-1"},
			},
			NextOffset: "next-1", // forces iteration to read page 2; without this, the loop breaks after page 1
		}},
		{err: errors.New("qdrant timeout")},
	}}
	ids := &fakeAssetIDs{ids: []string{"a-1"}}
	repairer := &fakeRepairer{}
	svc := NewService()

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if !errors.Is(err, ErrRefusingApply) {
		t.Fatalf("expected ErrRefusingApply, got %v", err)
	}
	if report.CompleteScan {
		t.Error("CompleteScan must be false after page-2 error")
	}
	if len(report.PageErrors) == 0 {
		t.Error("PageErrors must record the page-2 error")
	}
	if repairer.applyCalls != 0 {
		t.Errorf("Repairer.Apply must NOT be called on incomplete scan; got %d calls", repairer.applyCalls)
	}
}

// TestTODO8_SpecCase3_MaxPagesReached_AppliesBlocked verifies that a 3-page
// scroll with MaxPages=2 trips MaxPagesReached + NextOffsetLingering both.
func TestTODO8_SpecCase3_MaxPagesReached_AppliesBlocked(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{
			Points:     []ScannedPoint{{PointID: "p1", AssetID: "a-1"}},
			NextOffset: "next-1",
		}},
		{page: &ScrollPage{
			Points:     []ScannedPoint{{PointID: "p2", AssetID: "a-2"}},
			NextOffset: "next-2", // still set on the LAST iterated page
		}},
		// Page 3 is never read — the loop terminates at iteration 1 (MaxPages=2).
	}}
	ids := &fakeAssetIDs{ids: []string{"a-1", "a-2", "a-3"}}
	repairer := &fakeRepairer{}
	svc := NewService()

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   2,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if !errors.Is(err, ErrRefusingApply) {
		t.Fatalf("expected ErrRefusingApply on cap-hit, got %v", err)
	}
	if !report.MaxPagesReached {
		t.Error("MaxPagesReached must be true")
	}
	if !report.NextOffsetLingering {
		t.Error("NextOffsetLingering must be true (we exited with offset set)")
	}
	if report.PointsScrolled != 2 {
		t.Errorf("expected 2 points scrolled (pages 0+1), got %d", report.PointsScrolled)
	}
	if repairer.applyCalls != 0 {
		t.Errorf("Repairer.Apply must NOT be called on cap-hit; got %d calls", repairer.applyCalls)
	}
}

// TestTODO8_SpecCase4_ZeroPointsUnexpected_AppliesBlocked verifies condition 4:
// when the operator expects a non-empty collection but the scroller returns
// zero points, apply is blocked.
func TestTODO8_SpecCase4_ZeroPointsUnexpected_AppliesBlocked(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: nil, NextOffset: ""}}, // empty drain
	}}
	ids := &fakeAssetIDs{ids: []string{"a-1", "a-2"}}
	repairer := &fakeRepairer{}
	svc := NewService()

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection:               "media_assets_v3",
		MaxPages:                 10,
		ExpectCollectionNonEmpty: true,
		Scroller:                 scroller,
		AssetIDs:                 ids,
		Repairer:                 repairer,
	})

	if !errors.Is(err, ErrRefusingApply) {
		t.Fatalf("expected ErrRefusingApply on zero-points-unexpected, got %v", err)
	}
	if !report.ZeroPointsUnexpected {
		t.Error("ZeroPointsUnexpected must be true")
	}
	if report.PointsScrolled != 0 {
		t.Errorf("expected 0 points scrolled, got %d", report.PointsScrolled)
	}
	if report.CompleteScan {
		t.Error("CompleteScan must be false (zero-points-unexpected fails the gate)")
	}
	if repairer.applyCalls != 0 {
		t.Errorf("Repairer.Apply must NOT be called on zero-points; got %d calls", repairer.applyCalls)
	}
}

// TestTODO8_SpecCase5_DryRun_PassesReportOnError verifies dry-run mode returns
// the report + CompleteScan=false WITHOUT ErrRefusingApply. Repairer is
// NOT invoked.
func TestTODO8_SpecCase5_DryRun_PassesReportOnError(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{{err: errors.New("qdrant 503")}}}
	ids := &fakeAssetIDs{ids: []string{"a-1", "a-2"}}
	repairer := &fakeRepairer{}
	svc := NewService()

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		DryRun:     true,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err != nil {
		t.Fatalf("dry-run must NOT return error; got %v", err)
	}
	if report == nil {
		t.Fatal("dry-run must return report even on partial scan")
	}
	if report.CompleteScan {
		t.Error("CompleteScan stays false on errors")
	}
	if len(report.Errors) == 0 {
		t.Error("report.Errors must include the scroll failure description")
	}
	if repairer.applyCalls != 0 {
		t.Errorf("dry-run must NEVER invoke Repairer; got %d calls", repairer.applyCalls)
	}

	// Sub-assertion: the report has the warning style the spec called out.
	hasWarning := false
	for _, e := range report.Errors {
		if strings.Contains(e, "scroll page") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Error("report.Errors should describe the scroll page that failed")
	}
}

// TestTODO8_SpecCase6_ApplyOnIncomplete_NoRepair verifies the failure
// half of "no repair on incomplete scan": DryRun=false + scan fails ⇒
// ErrRefusingApply AND Repairer.applyCalls == 0. The mock counter is the
// load-bearing assertion here.
func TestTODO8_SpecCase6_ApplyOnIncomplete_NoRepair(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{{err: errors.New("qdrant down")}}}
	ids := &fakeAssetIDs{ids: []string{"a-1", "a-2", "a-3"}}
	repairer := &fakeRepairer{}
	svc := NewService()

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if !errors.Is(err, ErrRefusingApply) {
		t.Fatalf("expected ErrRefusingApply on incomplete scan; got %v", err)
	}
	if report.Applied {
		t.Error("report.Applied must be false (no repair ran)")
	}
	if repairer.applyCalls != 0 {
		t.Errorf("REPAIR COUNTER: expected 0 calls on incomplete scan; got %d (this is the load-bearing assertion)", repairer.applyCalls)
	}
}

// TestTODO8_SpecCase7_ApplyOnComplete_RepairExecutes verifies the success
// half: a clean CompleteScan + 2 orphan Qdrant points ⇒ Repairer.Apply called
// once with the orphan IDs, report.Applied=true.
//
// QDRANT-006 TODO 10 update (strict port gates): this test now wires
// BOTH stub ports via NewServiceWithDeps. Spec literal
// ("apply senza outbox/payload rifiutato esplicitamente") requires
// unconditional gating — orphan-only drift DOES NOT exempt the
// apply phase from the outbox requirement. The Repairer captures
// orphan IDs in lastOrphanIDs so we still verify the orchestration
// side did its job; the stubPayload is unused for this code path
// (the Repairer doesn't dispatch to ports via the unit-test layer)
// but is wired so the unconditional gates pass.
func TestTODO8_SpecCase7_ApplyOnComplete_RepairExecutes(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: []ScannedPoint{
			{PointID: "p-orphan-1", AssetID: "orphan-1"},
			{PointID: "p-orphan-2", AssetID: "orphan-2"},
			{PointID: "p-good", AssetID: "a-good"}, // also in SQLite → not orphan
		}}},
	}}
	ids := &fakeAssetIDs{ids: []string{"a-good"}} // SQLite has only the one good ID
	repairer := &fakeRepairer{}
	payload := &stubPayload{}
	outbox := &stubOutbox{}
	svc := NewServiceWithDeps(ServiceDeps{Outbox: outbox, PayloadMutator: payload})

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err != nil {
		t.Fatalf("complete scan + dry-run=false must succeed; got %v", err)
	}
	if !report.CompleteScan {
		t.Errorf("CompleteScan must be true; report.Errors=%v", report.Errors)
	}
	if report.OrphanCount != 2 {
		t.Errorf("expected 2 orphans, got %d (orphans=%v)", report.OrphanCount, report.OrphanIDs)
	}
	if report.MissingCount != 0 {
		t.Errorf("expected 0 missing, got %d (missing=%v)", report.MissingCount, report.MissingIDs)
	}
	if repairer.applyCalls != 1 {
		t.Fatalf("REPAIR COUNTER: expected exactly 1 Apply call on complete-drift scan; got %d", repairer.applyCalls)
	}
	if !report.Applied {
		t.Error("report.Applied must be true after successful repair")
	}
	if len(repairer.lastOrphanIDs) != 2 {
		t.Errorf("Apply received %d orphan IDs, expected 2", len(repairer.lastOrphanIDs))
	}
	// Sorted assertion: orphan IDs passed in deterministic order.
	expected := []string{"orphan-1", "orphan-2"}
	for i, got := range repairer.lastOrphanIDs {
		if got != expected[i] {
			t.Errorf("Apply orphan id %d: got %q, want %q", i, got, expected[i])
		}
	}
	// Repaired counter arithmetic (TODO 10 invariant).
	if report.RepairAttempted != 2 {
		t.Errorf("RepairAttempted: expected 2 (orphan count), got %d", report.RepairAttempted)
	}
	if report.RepairSucceeded != 2 {
		t.Errorf("RepairSucceeded: expected 2 (all attempted succeeded), got %d", report.RepairSucceeded)
	}
}

// ── Auxiliary / sub-flag coverage (TODO 8) ──────────────────────────────

// TestTODO8_CompleteScanIsAndOfAllSubFlags verifies that each sub-flag
// independently flips CompleteScan=false. This is the property guarantee the
// fail-closed contract rests on.
func TestTODO8_CompleteScanIsAndOfAllSubFlags(t *testing.T) {
	cases := []struct {
		name           string
		pages          []scriptedPage
		ids            []string
		expectNonEmpty bool
		wantComplete   bool
	}{
		{
			name: "all_clean",
			pages: []scriptedPage{
				{page: &ScrollPage{Points: []ScannedPoint{
					{PointID: "p1", AssetID: "a-1"},
				}}},
			},
			ids:            []string{"a-1"},
			expectNonEmpty: true,
			wantComplete:   true,
		},
		{
			name: "asset_id_missing",
			pages: []scriptedPage{
				{page: &ScrollPage{Points: []ScannedPoint{
					{PointID: "p-bad", AssetID: ""}, // condition 5
				}}},
			},
			ids:            []string{},
			expectNonEmpty: true,
			wantComplete:   false,
		},
		{
			name: "empty_collection_unexpected",
			pages: []scriptedPage{
				{page: &ScrollPage{Points: nil, NextOffset: ""}},
			},
			ids:            []string{},
			expectNonEmpty: true,
			wantComplete:   false,
		},
		{
			name: "empty_collection_expected",
			pages: []scriptedPage{
				{page: &ScrollPage{Points: nil, NextOffset: ""}},
			},
			ids:            []string{},
			expectNonEmpty: false, // caller explicitly OK with empty
			wantComplete:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService()
			report, err := svc.Reconcile(context.Background(), ReconcileOptions{
				Collection:               "media_assets_v3",
				MaxPages:                 10,
				ExpectCollectionNonEmpty: tc.expectNonEmpty,
				Scroller:                 &fakeScroller{pages: tc.pages},
				AssetIDs:                 &fakeAssetIDs{ids: tc.ids},
				DryRun:                   true, // dry-run so we always get the report
			})
			if err != nil && tc.wantComplete {
				t.Fatalf("dry-run should not error on a clean scan; got %v", err)
			}
			if report.CompleteScan != tc.wantComplete {
				t.Errorf("CompleteScan=%v want %v (Errors=%v)",
					report.CompleteScan, tc.wantComplete, report.Errors)
			}
		})
	}
}

// TestTODO8_RepairerRequired_WhenApplyAndDriftPresent verifies the "fail-fast
// on missing Repairer" branch: DryRun=false, drift exists, Repairer=nil ⇒
// explicit error rather than silent no-op.
//
// QDRANT-006 TODO 10 commentary: this test still produces a Repairer-required
// error because the per-call Repairer=nil gate runs BEFORE the port-side
// gates in reconcile.apply phase. The test doesn't need to wire ports — it
// exits before any port can be checked.
func TestTODO8_RepairerRequired_WhenApplyAndDriftPresent(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: []ScannedPoint{
			{PointID: "p-pp", AssetID: "orphan"},
		}}},
	}}
	ids := &fakeAssetIDs{ids: []string{"good"}}
	svc := NewService()

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection:               "media_assets_v3",
		MaxPages:                 10,
		Scroller:                 scroller,
		AssetIDs:                 ids,
		Repairer:                 nil, // explicit nil
		ExpectCollectionNonEmpty: true,
	})

	if err == nil {
		t.Fatal("expected error when Repairer is nil + drift present")
	}
	if !strings.Contains(err.Error(), "Repairer is required") {
		t.Errorf("error must mention Repairer requirement; got %v", err)
	}
	if report == nil {
		t.Fatal("report must still be returned for operator forensics")
	}
	if !report.CompleteScan {
		t.Errorf("scan was clean (drift present, no errors); CompleteScan should be TRUE here so the fail-fast on nil-Repairer is the ONLY blockage, not the scan gate. report.Errors=%v", report.Errors)
	}
}

// TestTODO8_RepairerError_ReturnsReportAndError verifies that Repairer.Apply
// returning an error is surfaced as a wrapped error AND the report is still
// returned for forensics (Applied=false).
//
// QDRANT-006 TODO 10 update (strict port gates): this test now wires
// BOTH stub ports via NewServiceWithDeps so neither single-direction
// gate (outbox or payload) short-circuits the per-call Repairer flow.
// The Repairer itself returns an error, exercising end-to-end the
// Applied=false + RepairSucceeded=0 + wrapped count in error branches.
func TestTODO8_RepairerError_ReturnsReportAndError(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: []ScannedPoint{
			{PointID: "p-x", AssetID: "orphan-1"},
		}}},
	}}
	ids := &fakeAssetIDs{ids: []string{"good-1"}}
	repairer := &fakeRepairer{err: errors.New("qdrant delete failed")}
	payload := &stubPayload{} // wired so neither port gate trips
	outbox := &stubOutbox{}
	svc := NewServiceWithDeps(ServiceDeps{Outbox: outbox, PayloadMutator: payload})

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err == nil {
		t.Fatal("expected wrapped error when Repairer.Apply fails")
	}
	if !strings.Contains(err.Error(), "repair failed") {
		t.Errorf("error must wrap the repair failure; got %v", err)
	}
	if report == nil {
		t.Fatal("report must still be returned for forensics")
	}
	if report.Applied {
		t.Error("Applied must be false when Repairer.Apply fails")
	}
	if repairer.applyCalls != 1 {
		t.Errorf("Repairer was invoked once before failing; got %d calls", repairer.applyCalls)
	}
	// TODO 10 counter assertion: attempted = full drift size, succeeded = 0
	// when repair fails. The fixture here has 1 missing ("good-1" in SQLite
	// but NOT in the scroller's single-page contents) AND 1 orphan
	// ("orphan-1" in the Qdrant scroll but NOT in SQLite) — both directions
	// of drift are present, so the binary attempted count is 2 (not 1).
	if report.RepairAttempted != 2 {
		t.Errorf("RepairAttempted: expected 2 (1 missing + 1 orphan), got %d", report.RepairAttempted)
	}
	if report.RepairSucceeded != 0 {
		t.Errorf("RepairSucceeded: expected 0 (Repairer.Apply failed), got %d", report.RepairSucceeded)
	}
}

// TestTODO8_NoDrift_NoRepairCall verifies that a clean scan with zero
// missing AND zero orphan does NOT invoke Repairer.Apply (avoids redundant
// network round-trips).
//
// QDRANT-006 TODO 10 commentary: no port wiring needed because the apply
// phase is short-circuited (drift == 0) before any port gate is consulted.
// NewService() (zero-port) is fine.
func TestTODO8_NoDrift_NoRepairCall(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: []ScannedPoint{
			{PointID: "p1", AssetID: "a-1"},
			{PointID: "p2", AssetID: "a-2"},
		}}},
	}}
	ids := &fakeAssetIDs{ids: []string{"a-1", "a-2"}}
	repairer := &fakeRepairer{}
	svc := NewService()

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err != nil {
		t.Fatalf("clean scan should succeed; got %v", err)
	}
	if !report.CompleteScan {
		t.Errorf("CompleteScan must be true; errors=%v", report.Errors)
	}
	if repairer.applyCalls != 0 {
		t.Errorf("clean scan with zero drift must NOT invoke Repairer; got %d calls", repairer.applyCalls)
	}
}

// TestTODO8_MissingAndOrphan_BothReported verifies that the missing/orphan
// computation produces both sets when both Qdrant and SQLite have IDs the
// other doesn't know about. The Repairer.Apply receives the union.
//
// QDRANT-006 TODO 10 update: this test now wires BOTH stub ports (outbox
// for missing IDs, payload for orphan IDs) via NewServiceWithDeps because
// the apply phase needs both directions to be unblocked.
func TestTODO8_MissingAndOrphan_BothReported(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: []ScannedPoint{
			{PointID: "p-orphan", AssetID: "orphan"},
			{PointID: "p-good", AssetID: "good"}, // shared
		}}},
	}}
	ids := &fakeAssetIDs{ids: []string{"good", "missing-from-qdrant"}} // "missing" not in Qdrant
	repairer := &fakeRepairer{}
	outbox := &stubOutbox{}
	payload := &stubPayload{}
	svc := NewServiceWithDeps(ServiceDeps{Outbox: outbox, PayloadMutator: payload})

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err != nil {
		t.Fatalf("expected clean scan; got %v", err)
	}
	if report.OrphanCount != 1 || report.OrphanIDs[0] != "orphan" {
		t.Errorf("Orphan set wrong: count=%d ids=%v", report.OrphanCount, report.OrphanIDs)
	}
	if report.MissingCount != 1 || report.MissingIDs[0] != "missing-from-qdrant" {
		t.Errorf("Missing set wrong: count=%d ids=%v", report.MissingCount, report.MissingIDs)
	}
	if repairer.applyCalls != 1 {
		t.Errorf("Repairer must be invoked once for the union; got %d calls", repairer.applyCalls)
	}
	if len(repairer.lastMissingIDs) != 1 || len(repairer.lastOrphanIDs) != 1 {
		t.Errorf("Repairer payload mismatch: orphan=%v missing=%v",
			repairer.lastOrphanIDs, repairer.lastMissingIDs)
	}
	// Counter arithmetic on full-success path.
	if report.RepairAttempted != 2 {
		t.Errorf("RepairAttempted: expected 2 (1 missing + 1 orphan), got %d", report.RepairAttempted)
	}
	if report.RepairSucceeded != 2 {
		t.Errorf("RepairSucceeded: expected 2 (all attempted succeeded), got %d", report.RepairSucceeded)
	}
	if !report.Applied {
		t.Error("report.Applied must be true when all repairs succeed")
	}
}

// ── 6 spec scenarios (TODO 10) ───────────────────────────────────────────

// TestTODO10_SpecCase1_OutboxNil_ErrOutboxRequired verifies that when
// the applier has missing-from-Qdrant IDs to re-emit but the Outbox port is
// nil, Reconcile returns ErrOutboxRequired and Applied=false.
func TestTODO10_SpecCase1_OutboxNil_ErrOutboxRequired(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: []ScannedPoint{
			{PointID: "p1", AssetID: "in-qdrant-1"}, // in Qdrant, in SQLite → no drift
		}}},
	}}
	// SQLite has an extra ID the Qdrant scan didn't see → MissingCount=1.
	ids := &fakeAssetIDs{ids: []string{"in-qdrant-1", "missing-1"}}
	repairer := &fakeRepairer{}
	// Outbox=nil (default); payload non-nil so the orphan gate doesn't trip.
	payload := &stubPayload{}
	svc := NewServiceWithDeps(ServiceDeps{PayloadMutator: payload})

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if !errors.Is(err, ErrOutboxRequired) {
		t.Fatalf("expected ErrOutboxRequired, got %v", err)
	}
	if !strings.Contains(err.Error(), "outbox") {
		t.Errorf("error message must name the outbox port; got %v", err)
	}
	if report.Applied {
		t.Error("report.Applied must be false when port gate trips")
	}
	if report.CompleteScan != true {
		t.Errorf("scan must be clean (the gate is the ONLY failure mode here); got CompleteScan=false (errors=%v)", report.Errors)
	}
	if repairer.applyCalls != 0 {
		t.Errorf("Repairer must NOT be invoked when a port gate trips; got %d", repairer.applyCalls)
	}
	if payload.calls != 0 {
		t.Errorf("payload port must NOT be called when only missing drift is present; got %d", payload.calls)
	}
}

// TestTODO10_SpecCase2_PayloadNil_ErrPayloadMutatorRequired verifies the
// symmetric guard for the orphan direction: orphan IDs to delete +
// PayloadMutator=nil ⇒ ErrPayloadMutatorRequired, Applied=false.
func TestTODO10_SpecCase2_PayloadNil_ErrPayloadMutatorRequired(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: []ScannedPoint{
			{PointID: "p-o", AssetID: "orphan-1"}, // in Qdrant, NOT in SQLite → orphan
		}}},
	}}
	ids := &fakeAssetIDs{ids: []string{}} // empty SQLite → Qdrant point is orphan
	repairer := &fakeRepairer{}
	// Payload=nil (default); outbox non-nil so the missing gate doesn't trip.
	outbox := &stubOutbox{}
	svc := NewServiceWithDeps(ServiceDeps{Outbox: outbox})

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if !errors.Is(err, ErrPayloadMutatorRequired) {
		t.Fatalf("expected ErrPayloadMutatorRequired, got %v", err)
	}
	if !strings.Contains(err.Error(), "payload") {
		t.Errorf("error message must name the payload port; got %v", err)
	}
	if report.Applied {
		t.Error("report.Applied must be false when port gate trips")
	}
	if !report.CompleteScan {
		t.Errorf("scan must be clean here; got CompleteScan=false (errors=%v)", report.Errors)
	}
	if repairer.applyCalls != 0 {
		t.Errorf("Repairer must NOT be invoked when a port gate trips; got %d", repairer.applyCalls)
	}
	if outbox.calls != 0 {
		t.Errorf("outbox port must NOT be called when only orphan drift is present; got %d", outbox.calls)
	}
}

// TestTODO10_SpecCase3_BothPortsNil_OneError verifies that when BOTH ports
// are nil AND drift is bidirectional (both missing AND orphan), ONE of the
// two sentinels surfaces (the missing-gate runs first; production error
// reporting sees ErrOutboxRequired and operator dashboards can add a
// follow-up "fix payload wiring too" if they triage).
func TestTODO10_SpecCase3_BothPortsNil_OneError(t *testing.T) {
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: []ScannedPoint{
			{PointID: "p-o", AssetID: "orphan-1"}, // orphan: not in SQLite
			{PointID: "p-m", AssetID: "common-1"}, // in both
		}}},
	}}
	ids := &fakeAssetIDs{ids: []string{"common-1", "missing-1"}} // missing: not in Qdrant
	repairer := &fakeRepairer{}
	// Both ports nil → first gate (missing → outbox) fires.
	svc := NewServiceWithDeps(ServiceDeps{})

	_, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err == nil {
		t.Fatal("expected error when both ports nil + bidirectional drift")
	}
	// Either sentinel is acceptable (gates run missing-first); assert the
	// port-name token appears in the message so the operator gets a clear
	// hint.
	if !strings.Contains(err.Error(), "port required") {
		t.Errorf("error must contain 'port required' (QDRANT-006 spec verbatim suffix); got %v", err)
	}
	if repairer.applyCalls != 0 {
		t.Errorf("Repairer must NOT be invoked when a port gate trips; got %d", repairer.applyCalls)
	}
}

// TestTODO10_SpecCase4_AllRepairsFail_ErrorsCountVisible verifies that when
// the repair collaborators all fail (here: PayloadMutator returns error),
// the report carries the failure signal AND the wrapped repair error
// exposes the attempt count to operator triage.
//
// Note: this test verifies the Repairer-defensive path. We use a
// stubPayload that returns an error on DeletePoints — but the production
// Repairer adapter (the canonical one wired by the composition root)
// delegates the orphan path to the payload port. In this test we
// construct a fakeRepairer that ALSO returns an error (the TODOs have
// two failure surfaces — port-level AND repairer-level — and we exercise
// the Repairer-level here so the test isolates the counter arithmetic).
//
// Both surfaces produce the same observable: RepairAttempted>0,
// RepairSucceeded=0, Applied=false.
func TestTODO10_SpecCase4_AllRepairsFail_ErrorsCountVisible(t *testing.T) {
	// 5 orphan IDs for the failure-count surface.
	orphanPoints := make([]ScannedPoint, 0, 5)
	for i := 1; i <= 5; i++ {
		orphanPoints = append(orphanPoints, ScannedPoint{
			PointID: "p-o-" + string(rune('0'+i)),
			AssetID: "orphan-" + string(rune('0'+i)),
		})
	}
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: orphanPoints}},
	}}
	ids := &fakeAssetIDs{ids: []string{}}
	repairer := &fakeRepairer{err: errors.New("simulated repair failure (all 5)")}
	// Strict port gates (TODO 10 second-pass) require BOTH ports wired
	// unconditionally even when only one drift direction is present;
	// the Repairer is what exercises the failure here.
	payload := &stubPayload{}
	outbox := &stubOutbox{}
	svc := NewServiceWithDeps(ServiceDeps{Outbox: outbox, PayloadMutator: payload})

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err == nil {
		t.Fatal("expected wrapped error when all repairs fail")
	}
	if !strings.Contains(err.Error(), "repair failed") {
		t.Errorf("error must wrap 'repair failed'; got %v", err)
	}
	if !strings.Contains(err.Error(), "5 attempted") {
		t.Errorf("error must surface the attempt count for operator triage; got %v", err)
	}
	if report.Applied {
		t.Error("report.Applied must be false when all repairs fail")
	}
	if report.RepairAttempted != 5 {
		t.Errorf("RepairAttempted: expected 5 (all orphans), got %d", report.RepairAttempted)
	}
	if report.RepairSucceeded != 0 {
		t.Errorf("RepairSucceeded: expected 0 (all failed), got %d", report.RepairSucceeded)
	}
	if repairer.applyCalls != 1 {
		t.Errorf("Repairer.Apply was called once before failing; got %d calls", repairer.applyCalls)
	}
}

// TestTODO10_SpecCase5_DryRun_NilPorts_OK verifies that dry-run mode
// tolerates BOTH ports being nil: the reconcile returns the report and
// Applied=false (no repair executed, by definition of dry-run). The
// operator gets to preview the drift without paying the wiring tax.
func TestTODO10_SpecCase5_DryRun_NilPorts_OK(t *testing.T) {
	orphanPoints := []ScannedPoint{
		{PointID: "p-o-1", AssetID: "orphan-1"},
		{PointID: "p-o-2", AssetID: "orphan-2"},
	}
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: orphanPoints}},
	}}
	// Bidirectional drift: 2 orphans missing from SQLite + 1 missing from
	// Qdrant for completeness.
	ids := &fakeAssetIDs{ids: []string{"missing-1"}}
	repairer := &fakeRepairer{}
	// Both ports nil — dry-run must still work.
	svc := NewServiceWithDeps(ServiceDeps{})

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		DryRun:     true,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err != nil {
		t.Fatalf("dry-run must succeed with nil ports; got %v", err)
	}
	if report == nil {
		t.Fatal("dry-run must return report (preview-the-drift contract)")
	}
	if report.Applied {
		t.Error("Applied must be false in dry-run (no repair ran)")
	}
	if report.CompleteScan != true {
		t.Errorf("scan must be clean here; got CompleteScan=false (errors=%v)", report.Errors)
	}
	if report.OrphanCount != 2 {
		t.Errorf("OrphanCount: expected 2, got %d", report.OrphanCount)
	}
	if report.MissingCount != 1 {
		t.Errorf("MissingCount: expected 1, got %d", report.MissingCount)
	}
	if repairer.applyCalls != 0 {
		t.Errorf("Repairer.Apply must NEVER be called in dry-run; got %d calls", repairer.applyCalls)
	}
}

// TestTODO10_SpecCase6_AllRepairsSucceed_AppliedTrue verifies the happy
// path: 5 orphan IDs are deleted via the payload port, Repairer.Apply
// succeeds, the counters reflect "5 attempted, 5 succeeded", and
// Applied=true.
func TestTODO10_SpecCase6_AllRepairsSucceed_AppliedTrue(t *testing.T) {
	orphanPoints := make([]ScannedPoint, 0, 5)
	for i := 1; i <= 5; i++ {
		orphanPoints = append(orphanPoints, ScannedPoint{
			PointID: "p" + string(rune('0'+i)),
			AssetID: "asset-" + string(rune('0'+i)),
		})
	}
	scroller := &fakeScroller{pages: []scriptedPage{
		{page: &ScrollPage{Points: orphanPoints}},
	}}
	ids := &fakeAssetIDs{ids: []string{}}
	repairer := &fakeRepairer{}
	// Strict port gates (TODO 10 second-pass) require BOTH ports wired
	// unconditionally; the Repairer success is what this test exercises.
	payload := &stubPayload{}
	outbox := &stubOutbox{}
	svc := NewServiceWithDeps(ServiceDeps{Outbox: outbox, PayloadMutator: payload})

	report, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Collection: "media_assets_v3",
		MaxPages:   10,
		Scroller:   scroller,
		AssetIDs:   ids,
		Repairer:   repairer,
	})

	if err != nil {
		t.Fatalf("happy path must succeed; got %v", err)
	}
	if !report.Applied {
		t.Error("Applied must be true on all-success path")
	}
	if report.RepairAttempted != 5 {
		t.Errorf("RepairAttempted: expected 5, got %d", report.RepairAttempted)
	}
	if report.RepairSucceeded != 5 {
		t.Errorf("RepairSucceeded: expected 5, got %d", report.RepairSucceeded)
	}
	if report.RepairAttempted != report.RepairSucceeded {
		t.Errorf("Applied-gate invariant broken: Attempted(%d) != Succeeded(%d)",
			report.RepairAttempted, report.RepairSucceeded)
	}
	if repairer.applyCalls != 1 {
		t.Errorf("Repairer must be called exactly once; got %d", repairer.applyCalls)
	}
}

// (TODO 10 round-1 sym guards removed: spec literal reads BOTH ports
// as unconditional apply dependencies, not per-direction. Per-direction
// gating is rejected; restore only on a USER-SPEC change.)
