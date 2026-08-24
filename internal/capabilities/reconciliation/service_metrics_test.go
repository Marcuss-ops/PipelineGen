package reconciliation

import (
	"context"
	"os"
	"reflect"
	"testing"

	"go.uber.org/zap"
)

// Metrics and observability contracts emitted by reconciliation.

func TestVersionMismatchCounts_PureHelper(t *testing.T) {
	pairs := []Classification{
		{Kind: KindVersionStale, Channel: "text"},
		{Kind: KindVersionStale, Channel: "text"},
		{Kind: KindVersionStale, Channel: "transcript"},
		{Kind: KindMissing},                        // should be ignored
		{Kind: KindVersionStale, Channel: ""},      // empty channel ignored
		{Kind: KindLifecycleMismatch, Channel: ""}, // wrong kind ignored
	}
	got := versionMismatchCounts(pairs)
	want := map[string]int{"text": 2, "transcript": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("versionMismatchCounts=%+v, want %+v", got, want)
	}

	if nilOnly := versionMismatchCounts(nil); nilOnly != nil {
		t.Fatalf("empty version-stale set should return nil, got %+v", nilOnly)
	}
}

func TestMetricMode_Labels(t *testing.T) {
	if got := metricMode(true); got != "dry_run" {
		t.Fatalf("metricMode(true)=%q, want dry_run", got)
	}
	if got := metricMode(false); got != "apply" {
		t.Fatalf("metricMode(false)=%q, want apply", got)
	}
}

func TestReconcile_VersionMismatchPerChannel_Emitted(t *testing.T) {
	mtr := &stubMetrics{}
	svc := fixtureService(t,
		multiChannelSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
			// Three version-stale pairs across two channels.
			"stale_text_a": {ID: "pt-stale_text_a", Payload: map[string]interface{}{
				"asset_id": "stale_text_a",
				"name":     "x", "source": "youtube",
				"lifecycle_state":              "ACTIVE",
				"embedding_version_text":       "v0",
				"embedding_version_transcript": "2026-06-16-v1",
			}},
			"stale_text_b": {ID: "pt-stale_text_b", Payload: map[string]interface{}{
				"asset_id": "stale_text_b",
				"name":     "x", "source": "youtube",
				"lifecycle_state":              "ACTIVE",
				"embedding_version_text":       "v0",
				"embedding_version_transcript": "2026-06-16-v1",
			}},
			"stale_trans": {ID: "pt-stale_trans", Payload: map[string]interface{}{
				"asset_id": "stale_trans",
				"name":     "x", "source": "youtube",
				"lifecycle_state":              "ACTIVE",
				"embedding_version_text":       "2026-06-16-v1",
				"embedding_version_transcript": "v0",
			}},
			"clean": {ID: "pt-clean", Payload: map[string]interface{}{
				"asset_id": "clean",
				"name":     "x", "source": "youtube",
				"lifecycle_state":              "ACTIVE",
				"embedding_version_text":       "2026-06-16-v1",
				"embedding_version_transcript": "2026-06-16-v1",
			}},
		}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "stale_text_a", LifecycleState: "ACTIVE"},
			{ID: "stale_text_b", LifecycleState: "ACTIVE"},
			{ID: "stale_trans", LifecycleState: "ACTIVE"},
			{ID: "clean", LifecycleState: "ACTIVE"},
		}},
		mtr,
		withOutbox(&stubOutbox{}),
		withPayload(&stubPayload{}),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(mtr.versionChannels) != 1 {
		t.Fatalf("expected 1 RecordVersionMismatchPerChannel call, got %d", len(mtr.versionChannels))
	}
	got := mtr.versionChannels[0]
	if got["text"] != 2 || got["transcript"] != 1 {
		t.Fatalf("versionChannels=%+v, want text=2 transcript=1", got)
	}
}

func TestReconcile_DryRunSuppressesDispatchAndLegacyMetrics(t *testing.T) {
	mtr := &stubMetrics{}
	outbox := &stubOutbox{}
	payload := &stubPayload{}
	svc := fixtureService(t,
		versionCheckSchema(),
		&stubQdrant{pointsByID: map[string]pointWithID{
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
		}},
		&stubSQLite{rows: []AssetSnapshot{
			{ID: "stale", LifecycleState: "ACTIVE"},
			{ID: "legacy_status", LifecycleState: "ACTIVE"},
		}},
		mtr,
		withOutbox(outbox),
		withPayload(payload),
		withPointIDFor(canonicalPointID),
		withLog(zap.NewNop()),
	)
	// DryRun mode (default per QDRANT-005B DoD).
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(outbox.reindex) != 0 || len(outbox.deletes) != 0 {
		t.Fatalf("DryRun should not invoke outbox; got reindex=%v deletes=%v", outbox.reindex, outbox.deletes)
	}
	if len(payload.calls) != 0 {
		t.Fatalf("DryRun should not invoke payload.DeletePayloadKeys")
	}
	if len(mtr.dispatches) != 0 || len(mtr.legacyStrips) != 0 {
		t.Fatalf("DryRun must not emit dispatch or legacy metric calls; got dispatches=%d legacy=%d", len(mtr.dispatches), len(mtr.legacyStrips))
	}
	// Findings + run-complete still emit.
	if len(mtr.findings) != 1 {
		t.Fatalf("DryRun should emit findings; got %d", len(mtr.findings))
	}
	if len(mtr.runCompletes) != 1 {
		t.Fatalf("DryRun should emit run-complete; got %d", len(mtr.runCompletes))
	}
}

func TestReconcile_ReportWriteFailureEmitsErrorMetric(t *testing.T) {
	// Force a write failure to verify errors metric increments.
	oldWrite := writeFileDefault
	writeFileDefault = func(path string, v interface{}) error { return os.ErrPermission }
	defer func() { writeFileDefault = oldWrite }()

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
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: true, ReportPath: "/tmp/forbidden.json"})
	if err != nil {
		t.Fatalf("write failure is non-fatal; got %v", err)
	}
	if len(mtr.errors) != 1 || mtr.errors[0] == 0 {
		t.Fatalf("write failure should bump errors metric; got %+v", mtr.errors)
	}
}

// ── Misc helpers ──────────────────────────────────────────────────────
