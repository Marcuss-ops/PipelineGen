package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// Core reconciliation behavior: dry-run, apply dispatch, failures,
// idempotency, and report persistence.

func TestReconcile_DryRun_DoesNotDispatch(t *testing.T) {
	outbox := &stubOutbox{}
	payload := &stubPayload{}
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
			"a1": {ID: "pt-a1", Payload: map[string]interface{}{
				"asset_id": "a1", "name": "x", "source": "youtube",
				"lifecycle_state": "ACTIVE",
			}},
		}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "a1", LifecycleState: "ACTIVE", ContentHash: "h1"},
			{ID: "missing", LifecycleState: "ACTIVE"},
		}},
		mtr,
		withOutbox(outbox),
		withPayload(payload),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	report, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Applied {
		t.Fatalf("DryRun must set Applied=false")
	}
	if len(outbox.reindex) != 0 || len(outbox.deletes) != 0 {
		t.Fatalf("DryRun must not dispatch; got reindex=%v deletes=%v", outbox.reindex, outbox.deletes)
	}
	if len(payload.calls) != 0 {
		t.Fatalf("DryRun must not call DeletePayloadKeys; got %d calls", len(payload.calls))
	}
	if report.Counts[KindMissing] != 1 {
		t.Fatalf("expected Counts[Missing]=1, got %d", report.Counts[KindMissing])
	}
	if report.ScannedTotals.SQLiteAssets != 2 || report.ScannedTotals.QdrantPoints != 1 {
		t.Fatalf("scanned totals wrong: %+v", report.ScannedTotals)
	}
	// QDRANT-005C: DryRun emits findings + run-complete but NO dispatch / legacy.
	if len(mtr.dispatches) != 0 {
		t.Fatalf("DryRun must not emit any RecordDispatch calls; got %d", len(mtr.dispatches))
	}
	if len(mtr.legacyStrips) != 0 {
		t.Fatalf("DryRun must not emit any RecordLegacyKeyStripped calls; got %d", len(mtr.legacyStrips))
	}
	if len(mtr.findings) != 1 {
		t.Fatalf("expected exactly 1 RecordFindings call, got %d", len(mtr.findings))
	}
	if mtr.findings[0][KindMissing] != 1 {
		t.Fatalf("expected findings to include Counts[Missing]=1, got %+v", mtr.findings[0])
	}
	if len(mtr.runCompletes) != 1 {
		t.Fatalf("expected exactly 1 RecordRunComplete call, got %d", len(mtr.runCompletes))
	}
	if mtr.runCompletes[0].mode != "dry_run" {
		t.Fatalf("expected RecordRunComplete mode=dry_run, got %q", mtr.runCompletes[0].mode)
	}
}

func TestReconcile_ApplyDispatchesPerKind(t *testing.T) {
	outbox := &stubOutbox{}
	payload := &stubPayload{}
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		versionCheckSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
			"a1": {ID: "pt-a1", Payload: map[string]interface{}{
				"asset_id": "a1", "name": "x", "source": "youtube",
				"lifecycle_state":        "ACTIVE",
				"embedding_version_text": "2026-06-16-v1",
			}},
			"stale": {ID: "pt-stale", Payload: map[string]interface{}{
				"asset_id": "stale", "name": "x", "source": "youtube",
				"lifecycle_state":        "ACTIVE",
				"embedding_version_text": "v0",
			}},
			"orphan": {ID: "pt-orphan", Payload: map[string]interface{}{"asset_id": "orphan"}},
			"legacy_status": {ID: "pt-legacy_status", Payload: map[string]interface{}{
				"asset_id": "legacy_status", "name": "x", "source": "youtube",
				"lifecycle_state": "ACTIVE", "status": "ACTIVE",
				"embedding_version_text": "2026-06-16-v1",
			}},
			"legacy_drive": {ID: "pt-legacy_drive", Payload: map[string]interface{}{
				"asset_id": "legacy_drive", "name": "x", "source": "youtube",
				"lifecycle_state":        "ACTIVE",
				"drive_link":             "https://drive.example/x",
				"local_path":             "/local/dump/x.mp4",
				"embedding_version_text": "2026-06-16-v1",
			}},
		}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "a1", LifecycleState: "ACTIVE", ContentHash: "h-a1"},
			{ID: "stale", LifecycleState: "ACTIVE", ContentHash: "h-stale"},
			{ID: "missing", LifecycleState: "ACTIVE", ContentHash: "h-missing"},
			{ID: "legacy_status", LifecycleState: "ACTIVE", ContentHash: "h-ls"},
			{ID: "legacy_drive", LifecycleState: "ACTIVE", ContentHash: "h-ld"},
		}},
		mtr,
		withOutbox(outbox),
		withPayload(payload),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	report, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Applied {
		t.Fatalf("Apply must set Applied=true")
	}
	if got, want := report.RepairSummary.ReindexEnqueued, 2; got != want {
		t.Fatalf("ReindexEnqueued=%d, want %d", got, want)
	}
	if got, want := report.RepairSummary.DeleteEnqueued, 1; got != want {
		t.Fatalf("DeleteEnqueued=%d, want %d", got, want)
	}
	if got, want := report.RepairSummary.PayloadStrips, 2; got != want {
		t.Fatalf("PayloadStrips=%d, want %d", got, want)
	}
	if len(payload.calls) != 2 {
		t.Fatalf("expected 2 DeletePayloadKeys calls (one per legacy category), got %d", len(payload.calls))
	}
	statusKeyCall := payload.calls[0]
	if len(statusKeyCall.keys) != 1 || statusKeyCall.keys[0] != "status" {
		t.Fatalf("expected first call to strip status key, got %v", statusKeyCall.keys)
	}
	if len(statusKeyCall.pointIDs) != 1 || statusKeyCall.pointIDs[0] != "pt-legacy_status" {
		t.Fatalf("expected first call to point at pt-legacy_status, got %v", statusKeyCall.pointIDs)
	}
	driveCall := payload.calls[1]
	if len(driveCall.keys) != 2 {
		t.Fatalf("expected second call to strip drive_link+local_path, got %v", driveCall.keys)
	}
	if len(driveCall.pointIDs) != 1 || driveCall.pointIDs[0] != "pt-legacy_drive" {
		t.Fatalf("expected second call to point at pt-legacy_drive, got %v", driveCall.pointIDs)
	}
	// QDRANT-005C: Apply emits dispatches + per-key legacy strips.
	if got, want := len(mtr.dispatches), 3; got != want {
		t.Fatalf("expected 3 RecordDispatch calls (reindex, delete, payload_strip), got %d", len(mtr.dispatches))
	}
	wantDispatches := map[string]int{
		"reindex":       2,
		"delete":        1,
		"payload_strip": 2,
	}
	gotDispatches := map[string]int{}
	for _, d := range mtr.dispatches {
		gotDispatches[d.action] += d.n
	}
	for action, want := range wantDispatches {
		if gotDispatches[action] != want {
			t.Fatalf("dispatches[%s]=%d, want %d (all=%+v)", action, gotDispatches[action], want, gotDispatches)
		}
	}
	if got, want := len(mtr.legacyStrips), 3; got != want {
		t.Fatalf("expected 3 RecordLegacyKeyStripped calls (status+drive_link+local_path), got %d", len(mtr.legacyStrips))
	}
	wantStripped := map[string]int{
		"status":     1,
		"drive_link": 1,
		"local_path": 1,
	}
	gotStripped := map[string]int{}
	for _, s := range mtr.legacyStrips {
		gotStripped[s.legacyKey] += s.n
	}
	for key, want := range wantStripped {
		if gotStripped[key] != want {
			t.Fatalf("strips[%s]=%d, want %d (all=%+v)", key, gotStripped[key], want, gotStripped)
		}
	}
	if mtr.runCompletes[0].mode != "apply" {
		t.Fatalf("expected RecordRunComplete mode=apply, got %q", mtr.runCompletes[0].mode)
	}
}

func TestReconcile_DispatchFailureCapturedInReport(t *testing.T) {
	outbox := &stubOutbox{failNext: true}
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "missing-1", LifecycleState: "ACTIVE", ContentHash: "h"},
		}},
		mtr,
		withOutbox(outbox),
		withPayload(&stubPayload{}),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	report, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err != nil {
		t.Fatalf("non-fatal dispatch failure should NOT abort reconcile; got error %v", err)
	}
	// PR 10: Applied = true ONLY when at least one repair kind
	// executed successfully. A single failed dispatch with zero
	// successful repairs yields Applied=false (this is the canonical
	// "you ran apply but nothing actually got fixed" signal).
	if report.Applied {
		t.Fatalf("Apply mode with zero successful repairs must set Applied=false (PR 10); the only repair was the failing enqueue")
	}
	if report.RepairSummary.ReindexEnqueued != 0 {
		t.Fatalf("failed enqueue should NOT count; got %d", report.RepairSummary.ReindexEnqueued)
	}
	if len(outbox.reindex) != 0 {
		t.Fatalf("failed enqueue must not be appended to dispatch log; got %+v", outbox.reindex)
	}
	if len(report.Errors) == 0 {
		t.Fatalf("dispatch failure must surface in report.Errors")
	}
	// QDRANT-005C: dispatch failure is reflected in errors metric.
	if len(mtr.errors) != 1 || mtr.errors[0] == 0 {
		t.Fatalf("RecordErrors should be called once with n>0; got %+v", mtr.errors)
	}
}

func TestReconcile_ScrollErrorPreservedAsNonFatal(t *testing.T) {
	badQdrant := &stubBadQdrant{err: os.ErrInvalid}
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		defaultSchema(),
		badQdrant,
		&stubSQLite{rows: []AssetSnapshot{{ID: "a1", LifecycleState: "ACTIVE"}}},
		mtr,
		withOutbox(&stubOutbox{}),
		withPayload(&stubPayload{}),
		withPointIDFor(func(s string) string { return "pt-a1" }),
		withLog(zap.NewNop()),
	)
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err == nil {
		t.Fatalf("scroll failure should bubble up; got nil error")
	}
	// QDRANT-005C: even on fatal scroll error, run-complete is still
	// emitted so last_success tracks the latest run attempt.
	if len(mtr.runCompletes) != 1 {
		t.Fatalf("expected RecordRunComplete to fire even on fatal scroll error; got %d", len(mtr.runCompletes))
	}
	if len(mtr.errors) != 1 || mtr.errors[0] == 0 {
		t.Fatalf("RecordErrors should fire on scroll fatal; got %+v", mtr.errors)
	}
}

func TestReconcile_IdempotentSecondRunProducesZeroDrift(t *testing.T) {
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
			"a1": {ID: "pt-a1", Payload: map[string]interface{}{
				"asset_id": "a1", "name": "x", "source": "youtube",
				"lifecycle_state": "ACTIVE", "embedding_version_text": "2026-06-16-v1",
			}},
			"orphan": {ID: "pt-orphan", Payload: map[string]interface{}{"asset_id": "orphan"}},
		}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "a1", LifecycleState: "ACTIVE", ContentHash: "h-a1"},
		}},
		mtr,
		withOutbox(&stubOutbox{}),
		withPayload(&stubPayload{}),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	r1, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if r1.Counts[KindOrphan] != 1 {
		t.Fatalf("run 1 should detect 1 orphan, got %d", r1.Counts[KindOrphan])
	}

	svc2 := fixtureService(t,
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
			"a1": {ID: "pt-a1", Payload: map[string]interface{}{
				"asset_id": "a1", "name": "x", "source": "youtube",
				"lifecycle_state": "ACTIVE", "embedding_version_text": "2026-06-16-v1",
			}},
		}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "a1", LifecycleState: "ACTIVE", ContentHash: "h-a1"},
		}},
		mtr,
		withOutbox(&stubOutbox{}),
		withPayload(&stubPayload{}),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	r2, err := svc2.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if len(r2.Classifications) != 0 {
		t.Fatalf("after repair, second run should produce zero drift, got %#v", r2.Classifications)
	}
	// QDRANT-005C: each run emits exactly one RecordRunComplete call
	// (independent of drift findings) — total across two runs + 1
	// from svc2 setup = 2 emissions here.
	if len(mtr.runCompletes) != 2 {
		t.Fatalf("expected 2 RecordRunComplete calls total across two runs, got %d", len(mtr.runCompletes))
	}
}

func TestReconcile_ReportPersistedToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "missing-1", LifecycleState: "ACTIVE"},
		}},
		mtr,
		withOutbox(&stubOutbox{}),
		withPayload(&stubPayload{}),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true, ReportPath: path})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected report at %s, got error %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("report file is empty")
	}
	if !contains(data, "missing") || !contains(data, "schema_version") {
		t.Fatalf("report should contain expected keys; got first 256 bytes: %s", truncate(string(data), 256))
	}
}

// ── New QDRANT-005C metric-emission tests ───────────────────────────
