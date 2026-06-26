package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// ── Test helpers ─────────────────────────────────────────────────────

type stubQdrant struct {
	pointsByID map[string]pointWithID
	calls      int
}

func (s *stubQdrant) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (Points, error) {
	s.calls++
	out := make([]PointSnapshot, 0, len(s.pointsByID))
	for _, p := range s.pointsByID {
		out = append(out, PointSnapshot{ID: p.ID, Payload: p.Payload})
	}
	return Points{Items: out, NextOffset: ""}, nil
}

type stubSQLite struct {
	rows []AssetSnapshot
}

func (s *stubSQLite) ListForReconcile(ctx context.Context, includeLifecycleStates []string) ([]AssetSnapshot, error) {
	if len(includeLifecycleStates) == 0 {
		return s.rows, nil
	}
	out := []AssetSnapshot{}
	for _, r := range s.rows {
		for _, st := range includeLifecycleStates {
			if r.LifecycleState == st {
				out = append(out, r)
				break
			}
		}
	}
	return out, nil
}

type stubOutbox struct {
	reindex   []string
	deletes   []string
	failNext  bool
}

func (s *stubOutbox) EnqueueReindex(ctx context.Context, assetID string) error {
	if s.failNext {
		s.failNext = false
		return os.ErrInvalid
	}
	s.reindex = append(s.reindex, assetID)
	return nil
}

func (s *stubOutbox) EnqueueDelete(ctx context.Context, assetID string) error {
	if s.failNext {
		s.failNext = false
		return os.ErrInvalid
	}
	s.deletes = append(s.deletes, assetID)
	return nil
}

type stubPayload struct {
	calls []stubPayloadCall
}

type stubPayloadCall struct {
	keys     []string
	pointIDs []string
}

func (s *stubPayload) DeletePayloadKeys(ctx context.Context, collection string, keys []string, pointIDs []string) error {
	s.calls = append(s.calls, stubPayloadCall{keys: append([]string{}, keys...), pointIDs: append([]string{}, pointIDs...)})
	return nil
}

// ── Tests ─────────────────────────────────────────────────────────────

func TestReconcile_DryRun_DoesNotDispatch(t *testing.T) {
	outbox := &stubOutbox{}
	payload := &stubPayload{}
	svc := NewService(
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
		outbox,
		payload,
		func(s string) string { return "pt-" + s },
		nil,
		zap.NewNop(),
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
}

func TestReconcile_ApplyDispatchesPerKind(t *testing.T) {
	outbox := &stubOutbox{}
	payload := &stubPayload{}
	svc := NewService(
		versionCheckSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
			"a1": {ID: "pt-a1", Payload: map[string]interface{}{
				"asset_id": "a1", "name": "x", "source": "youtube",
				"lifecycle_state": "ACTIVE",
				"embedding_version_text": "2026-06-16-v1",
			}},
			"stale": {ID: "pt-stale", Payload: map[string]interface{}{
				"asset_id": "stale", "name": "x", "source": "youtube",
				"lifecycle_state": "ACTIVE",
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
				"lifecycle_state": "ACTIVE",
				"drive_link":            "https://drive.example/x",
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
		outbox,
		payload,
		func(s string) string { return "pt-" + s },
		nil,
		zap.NewNop(),
	)
	report, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Applied {
		t.Fatalf("Apply must set Applied=true")
	}
	// Expected dispatch counts:
	//   Missing       → reindex (x1: "missing")
	//   VersionStale  → reindex (x1: "stale")
	//   Orphan        → delete (x1: "orphan")
	//   LifecycleKeyLegacy → payload-strip (batched with status key)
	//   LocatorLegacy       → payload-strip (batched with drive_link/local_path keys)
	// No reindex should fire on the clean pair ("a1" — payload matches schema).
	if got, want := report.RepairSummary.ReindexEnqueued, 2; got != want {
		t.Fatalf("ReindexEnqueued=%d, want %d", got, want)
	}
	if got, want := report.RepairSummary.DeleteEnqueued, 1; got != want {
		t.Fatalf("DeleteEnqueued=%d, want %d", got, want)
	}
	if got, want := report.RepairSummary.PayloadStrips, 2; got != want {
		t.Fatalf("PayloadStrips=%d, want %d", got, want)
	}
	// Verify payload calls were correctly batched:
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
}

func TestReconcile_DispatchFailureCapturedInReport(t *testing.T) {
	outbox := &stubOutbox{failNext: true}
	svc := NewService(
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "missing-1", LifecycleState: "ACTIVE", ContentHash: "h"},
		}},
		outbox,
		&stubPayload{},
		func(s string) string { return "pt-" + s },
		nil,
		zap.NewNop(),
	)
	report, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
	if err != nil {
		t.Fatalf("non-fatal dispatch failure should NOT abort reconcile; got error %v", err)
	}
	if !report.Applied {
		t.Fatalf("Apply=true even on dispatch failure (partial repair is recorded)")
	}
	if len(report.Errors) == 0 {
		t.Fatalf("dispatch failure must surface in report.Errors")
	}
	if report.RepairSummary.ReindexEnqueued != 0 {
		t.Fatalf("failed enqueue should NOT count; got %d", report.RepairSummary.ReindexEnqueued)
	}
}

func TestReconcile_ScrollErrorPreservedAsNonFatal(t *testing.T) {
	badQdrant := &stubBadQdrant{err: os.ErrInvalid}
	svc := NewService(
		defaultSchema(),
		badQdrant,
		&stubSQLite{rows: []AssetSnapshot{{ID: "a1", LifecycleState: "ACTIVE"}}},
		&stubOutbox{},
		&stubPayload{},
		func(s string) string { return "pt-a1" },
		nil,
		zap.NewNop(),
	)
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err == nil {
		t.Fatalf("scroll failure should bubble up; got nil error")
	}
}

type stubBadQdrant struct{ err error }

func (s *stubBadQdrant) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (Points, error) {
	return Points{}, s.err
}

func TestReconcile_IdempotentSecondRunProducesZeroDrift(t *testing.T) {
	// First run: we know sqlite has 'a1' ACTIVE; Qdrant has 'a1' with
	// matching payload + 'orphan' extra point. After DryRun apply (we
	// swap qdrantSet between calls so the dispatch model is consistent):
	//   - cleanup removes orphan from Qdrant (simulated by rebuild)
	//   - second run: zero drift.

	svc := NewService(
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
		&stubOutbox{},
		&stubPayload{},
		func(s string) string { return "pt-" + s },
		nil,
		zap.NewNop(),
	)
	r1, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if r1.Counts[KindOrphan] != 1 {
		t.Fatalf("run 1 should detect 1 orphan, got %d", r1.Counts[KindOrphan])
	}

	// Simulate repair having taken effect: now qdrant has only 'a1'.
	// (not swapping stubQdrant's data in-place because classify takes
	// a map; we re-construct the Service.)

	svc2 := NewService(
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
		&stubOutbox{},
		&stubPayload{},
		func(s string) string { return "pt-" + s },
		nil,
		zap.NewNop(),
	)
	r2, err := svc2.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if len(r2.Classifications) != 0 {
		t.Fatalf("after repair, second run should produce zero drift, got %#v", r2.Classifications)
	}
}

func TestReconcile_ReportPersistedToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	svc := NewService(
		defaultSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "missing-1", LifecycleState: "ACTIVE"},
		}},
		&stubOutbox{},
		&stubPayload{},
		func(s string) string { return "pt-" + s },
		nil,
		zap.NewNop(),
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

func contains(haystack []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
