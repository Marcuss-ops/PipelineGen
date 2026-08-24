package reconciliation

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/zap"
)

// Fail-closed pagination and content-hash propagation gates.

func TestReconcile_ScrollErrorOnSubsequentPage_BlocksApply(t *testing.T) {
	// Gate (a): any scroll page error after the first MUST be fatal.
	// PR 10 closes the QDRANT-005B regression that returned partial
	// data with nil err after the first page failed.
	outbox := &stubOutbox{}
	payloadStub := &stubPayload{}
	mtr := &stubMetrics{}
	pg := &pagingQdrant{
		assetID:    "a1",
		payload:    map[string]interface{}{"asset_id": "a1", "name": "x", "source": "youtube", "lifecycle_state": "ACTIVE"},
		nextOffset: "next-page-not-consumed",
		errAt:      2,
		err:        fmt.Errorf("synthetic scroll page 2 error"),
	}
	svc := fixtureService(t,
		defaultSchema(),
		pg,
		&stubSQLite{rows: []AssetSnapshot{{ID: "a1", LifecycleState: "ACTIVE", ContentHash: "h-a1"}}},
		mtr,
		withOutbox(outbox),
		withPayload(payloadStub),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	report, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err == nil {
		t.Fatalf("scroll error on page 2 must be fatal; got nil err")
	}
	if report.ScannedTotals.CompleteScan {
		t.Fatalf("CompleteScan must be false when a scroll gate fails")
	}
	if report.Applied {
		t.Fatalf("Apply must NOT execute when a scroll gate fails (PR 10 blocking invariant)")
	}
	if len(outbox.reindex) != 0 || len(outbox.deletes) != 0 {
		t.Fatalf("no outbox dispatch expected; got reindex=%+v deletes=%v", outbox.reindex, outbox.deletes)
	}
	if len(payloadStub.calls) != 0 {
		t.Fatalf("no payload mutation expected; got %d calls", len(payloadStub.calls))
	}
}

func TestReconcile_ScrollCap_BlocksApply(t *testing.T) {
	// Gate (b): maxPages cap hit when NextOffset is non-empty after
	// 400 pages. The stub stays on a single point with a non-empty
	// NextOffset forever; the reconciler's safety cap fires.
	outbox := &stubOutbox{}
	payloadStub := &stubPayload{}
	mtr := &stubMetrics{}
	pg := &pagingQdrant{
		assetID:    "a1",
		payload:    map[string]interface{}{"asset_id": "a1", "name": "x", "source": "youtube", "lifecycle_state": "ACTIVE"},
		nextOffset: "never-empty",
	}
	svc := fixtureService(t,
		defaultSchema(),
		pg,
		&stubSQLite{rows: []AssetSnapshot{{ID: "a1", LifecycleState: "ACTIVE", ContentHash: "h-a1"}}},
		mtr,
		withOutbox(outbox),
		withPayload(payloadStub),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err == nil {
		t.Fatalf("scroll cap hit must be fatal; got nil err")
	}
	if pg.calls != 400 {
		t.Fatalf("expected exactly 400 scroll calls (maxPages); got %d", pg.calls)
	}
	if len(outbox.reindex) != 0 {
		t.Fatalf("cap-hit scroll error must not dispatch; got %+v", outbox.reindex)
	}
}

func TestReconcile_MissingAssetID_BlocksApply(t *testing.T) {
	// Gate (e): scrolled points whose payload asset_id is empty/missing
	// are HARD gate failures when SQLite expected > 0. We can't trust
	// the missing-asset_id count — surfacing zero Orphan IDs would
	// falsely reassure the operator.
	outbox := &stubOutbox{}
	payloadStub := &stubPayload{}
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
			"missing-id-point": {ID: "pt-x", Payload: map[string]interface{}{"name": "y", "source": "youtube"}}, // asset_id MISSING
		}},
		&stubSQLite{rows: []AssetSnapshot{{ID: "expected-1", LifecycleState: "ACTIVE", ContentHash: "h-e1"}}}, // SQLite = 1
		mtr,
		withOutbox(outbox),
		withPayload(payloadStub),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err == nil {
		t.Fatalf("missing asset_id in payload (with expected>0) must be fatal (gate e)")
	}
	if len(outbox.reindex) != 0 {
		t.Fatalf("no dispatch expected on gate e; got %+v", outbox.reindex)
	}
}

func TestReconcile_AppliedFalseOnZeroRepairsInApply(t *testing.T) {
	// Apply-mode Blocking invariant: Applied=true ONLY when at least
	// one repair kind executed successfully. A clean re-run with zero
	// actionable pairs (no missing, no orphan, no patches) is a
	// no-op for apply — report.Applied must stay false so the operator
	// knows nothing actually got fixed.
	outbox := &stubOutbox{}
	payloadStub := &stubPayload{}
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
			"a1": {ID: "pt-a1", Payload: map[string]interface{}{
				"asset_id": "a1", "name": "x", "source": "youtube",
				"lifecycle_state": "ACTIVE",
			}},
		}},
		&stubSQLite{rows: []AssetSnapshot{{ID: "a1", LifecycleState: "ACTIVE", ContentHash: "h-a1"}}},
		mtr,
		withOutbox(outbox),
		withPayload(payloadStub),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	report, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Applied {
		t.Fatalf("Apply mode with zero actionable repairs must set Applied=false (PR 10)")
	}
	if len(outbox.reindex) != 0 || len(outbox.deletes) != 0 {
		t.Fatalf("no dispatch expected; got reindex=%v deletes=%v", outbox.reindex, outbox.deletes)
	}
	if len(payloadStub.calls) != 0 {
		t.Fatalf("no payload mutation expected; got %d", len(payloadStub.calls))
	}
	if len(mtr.dispatches) != 0 {
		t.Fatalf("no dispatch metrics expected; got %+v", mtr.dispatches)
	}
	if !report.ScannedTotals.CompleteScan {
		t.Fatalf("CompleteScan must be true when scan completed cleanly")
	}
}

func TestReconcile_Apply_PropagatesContentHashToOutbox(t *testing.T) {
	// PR 10 + PR 11 seam: the scanner-side content_hash rides on
	// Classification.ContentHash into applyRepair → outbox.EnqueueReindex.
	// PR 11 then folds it into the deterministic event_key
	// (assetID:targetSchema:contentHash). This test locks the
	// propagation at the dispatcher boundary.
	outbox := &stubOutbox{}
	payloadStub := &stubPayload{}
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "missing-1", LifecycleState: "ACTIVE", ContentHash: "h-missing-1"},
			{ID: "missing-2", LifecycleState: "ACTIVE", ContentHash: "h-missing-2"},
		}},
		mtr,
		withOutbox(outbox),
		withPayload(payloadStub),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	report, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outbox.reindex) != 2 {
		t.Fatalf("expected 2 reindex calls; got %+v", outbox.reindex)
	}
	want := map[string]string{
		"missing-1": "h-missing-1",
		"missing-2": "h-missing-2",
	}
	for _, call := range outbox.reindex {
		expected, ok := want[call.assetID]
		if !ok {
			t.Fatalf("unexpected reindex call: %+v", call)
		}
		if call.contentHash != expected {
			t.Fatalf("contentHash mismatch for %q: got=%q want=%q", call.assetID, call.contentHash, expected)
		}
	}
	if !report.Applied {
		t.Fatalf("Applied=true expected with 2 successful repairs; got false")
	}
}
