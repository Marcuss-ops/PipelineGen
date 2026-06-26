package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	reindex  []string
	deletes  []string
	failNext bool
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

// stubMetrics captures every Metrics interface call so tests can
// assert exactly what the reconciler emitted. All 6 method shapes
// (findings map, per-channel map, action+key+n, mode+dur) are
// recorded; slice fields accumulate in call order.
type stubMetrics struct {
	findings        []map[ClassificationKind]int
	versionChannels []map[string]int
	dispatches      []stubDispatchCall
	legacyStrips    []stubLegacyCall
	errors          []int
	runCompletes    []stubRunCompleteCall
}

type stubDispatchCall struct {
	action string
	n      int
}

type stubLegacyCall struct {
	legacyKey string
	n         int
}

type stubRunCompleteCall struct {
	mode           string
	durationSeconds float64
}

func (s *stubMetrics) RecordFindings(counts map[ClassificationKind]int) {
	// Defensive copy so further Service edits don't mutate the map.
	dup := make(map[ClassificationKind]int, len(counts))
	for k, v := range counts {
		dup[k] = v
	}
	s.findings = append(s.findings, dup)
}

func (s *stubMetrics) RecordVersionMismatchPerChannel(perChannel map[string]int) {
	dup := make(map[string]int, len(perChannel))
	for k, v := range perChannel {
		dup[k] = v
	}
	s.versionChannels = append(s.versionChannels, dup)
}

func (s *stubMetrics) RecordDispatch(action string, n int) {
	s.dispatches = append(s.dispatches, stubDispatchCall{action: action, n: n})
}

func (s *stubMetrics) RecordLegacyKeyStripped(legacyKey string, n int) {
	s.legacyStrips = append(s.legacyStrips, stubLegacyCall{legacyKey: legacyKey, n: n})
}

func (s *stubMetrics) RecordErrors(n int) { s.errors = append(s.errors, n) }

func (s *stubMetrics) RecordRunComplete(mode string, durationSeconds float64) {
	s.runCompletes = append(s.runCompletes, stubRunCompleteCall{mode: mode, durationSeconds: durationSeconds})
}

// fixtureService constructs a Service with stub adapters + the
// supplied metrics. Tighter default than NewServiceFromDeps so test
// bodies stay terse: schema + qdrant + sqlite + metrics required;
// outbox/payload/log fall back to stub/no-op defaults.
func fixtureService(t *testing.T, schema SchemaVersions, qd QdrantLister, sl SQLiteReconcileReader, m Metrics, extra ...serviceExtraOpt) *Service {
	t.Helper()
	deps := ServiceDeps{
		Schema:  schema,
		Qdrant:  qd,
		SQLite:  sl,
		Metrics: m,
	}
	if len(extra) == 0 {
		return NewServiceFromDeps(deps)
	}
	for _, opt := range extra {
		opt(&deps)
	}
	return NewServiceFromDeps(deps)
}

type serviceExtraOpt func(*ServiceDeps)

func withOutbox(o OutboxRepairEnqueuer) serviceExtraOpt {
	return func(d *ServiceDeps) { d.Outbox = o }
}

func withPayload(p QdrantPayloadMutator) serviceExtraOpt {
	return func(d *ServiceDeps) { d.Payload = p }
}

func withPointIDFor(fn AssetPointIDFunc) serviceExtraOpt {
	return func(d *ServiceDeps) { d.PointIDFor = fn }
}

func withLog(l *zap.Logger) serviceExtraOpt {
	return func(d *ServiceDeps) { d.Log = l }
}

func withReportWriter(r ReportWriter) serviceExtraOpt {
	return func(d *ServiceDeps) { d.ReportWriter = r }
}

// ── Existing PR1 tests (refactored to NewServiceFromDeps) ───────────

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
				"lifecycle_state":       "ACTIVE",
				"embedding_version_text": "2026-06-16-v1",
			}},
			"stale": {ID: "pt-stale", Payload: map[string]interface{}{
				"asset_id": "stale", "name": "x", "source": "youtube",
				"lifecycle_state":       "ACTIVE",
				"embedding_version_text": "v0",
			}},
			"orphan": {ID: "pt-orphan", Payload: map[string]interface{}{"asset_id": "orphan"}},
			"legacy_status": {ID: "pt-legacy_status", Payload: map[string]interface{}{
				"asset_id": "legacy_status", "name": "x", "source": "youtube",
				"lifecycle_state":       "ACTIVE", "status": "ACTIVE",
				"embedding_version_text": "2026-06-16-v1",
			}},
			"legacy_drive": {ID: "pt-legacy_drive", Payload: map[string]interface{}{
				"asset_id": "legacy_drive", "name": "x", "source": "youtube",
				"lifecycle_state":       "ACTIVE",
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
		"reindex":      2,
		"delete":       1,
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
		"status":      1,
		"drive_link":  1,
		"local_path":  1,
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
	if !report.Applied {
		t.Fatalf("Apply=true even on dispatch failure (partial repair is recorded)")
	}
	if len(report.Errors) == 0 {
		t.Fatalf("dispatch failure must surface in report.Errors")
	}
	if report.RepairSummary.ReindexEnqueued != 0 {
		t.Fatalf("failed enqueue should NOT count; got %d", report.RepairSummary.ReindexEnqueued)
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

type stubBadQdrant struct{ err error }

func (s *stubBadQdrant) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (Points, error) {
	return Points{}, s.err
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

func TestVersionMismatchCounts_PureHelper(t *testing.T) {
	pairs := []Classification{
		{Kind: KindVersionStale, Channel: "text"},
		{Kind: KindVersionStale, Channel: "text"},
		{Kind: KindVersionStale, Channel: "transcript"},
		{Kind: KindMissing},                       // should be ignored
		{Kind: KindVersionStale, Channel: ""},     // empty channel ignored
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
				"asset_id":              "stale_text_a",
				"name":                  "x", "source": "youtube",
				"lifecycle_state":       "ACTIVE",
				"embedding_version_text": "v0",
			}},
			"stale_text_b": {ID: "pt-stale_text_b", Payload: map[string]interface{}{
				"asset_id":              "stale_text_b",
				"name":                  "x", "source": "youtube",
				"lifecycle_state":       "ACTIVE",
				"embedding_version_text": "v0",
			}},
			"stale_trans": {ID: "pt-stale_trans", Payload: map[string]interface{}{
				"asset_id":                  "stale_trans",
				"name":                      "x", "source": "youtube",
				"lifecycle_state":           "ACTIVE",
				"embedding_version_text":     "2026-06-16-v1",
				"embedding_version_transcript": "v0",
			}},
			"clean": {ID: "pt-clean", Payload: map[string]interface{}{
				"asset_id":                  "clean",
				"name":                      "x", "source": "youtube",
				"lifecycle_state":           "ACTIVE",
				"embedding_version_text":     "2026-06-16-v1",
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
				"lifecycle_state":       "ACTIVE",
				"embedding_version_text": "v0",
			}},
			"orphan": {ID: "pt-orphan", Payload: map[string]interface{}{"asset_id": "orphan"}},
			"legacy_status": {ID: "pt-legacy_status", Payload: map[string]interface{}{
				"asset_id": "legacy_status", "name": "x", "source": "youtube",
				"lifecycle_state":       "ACTIVE", "status": "ACTIVE",
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

// ── Defensive: NewServiceFromDeps panic guards ──────────────────────
//
// NewServiceFromDeps panics on the three fail-loud paths (Schema.Version
// empty, Qdrant nil, SQLite nil). Production wire-up MUST NEVER trip
// these guards; they exist to surface misconfiguration at startup, not
// runtime. The test below locks the contract so a future refactor that
// accidentally makes one of these a silent fall-back (e.g. defaulting
// to an identity/sqlite stub) is caught by CI.
// ── LocatorKeys asymmetric-path coverage ─────────────────────────────
//
// Reviewer's only actionable PR2 follow-up: TestReconcile_ApplyDispatchesPerKind
// exercises only the both-keys case. A regression reverting applyRepair
// to blanket-bump per locator point would slip past CI silently. The
// 3-row table below pins drive_link-only / local_path-only / both so
// each causal path is independently verified.
func TestReconcile_LocatorLegacy_AsymmetricKeyCounters(t *testing.T) {
	cases := []struct {
		name           string
		assetID        string
		driveLink      string // empty -> key absent
		localPath      string // empty -> key absent
		wantDriveLinks int
		wantLocalPaths int
	}{
		{
			name:           "drive_link_only",
			assetID:        "asset_d_only",
			driveLink:      "https://drive.example/x",
			localPath:      "",
			wantDriveLinks: 1,
			wantLocalPaths: 0,
		},
		{
			name:           "local_path_only",
			assetID:        "asset_l_only",
			driveLink:      "",
			localPath:      "/local/dump/x.mp4",
			wantDriveLinks: 0,
			wantLocalPaths: 1,
		},
		{
			name:           "both_keys",
			assetID:        "asset_both",
			driveLink:      "https://drive.example/x",
			localPath:      "/local/dump/x.mp4",
			wantDriveLinks: 1,
			wantLocalPaths: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]interface{}{
				"asset_id":              tc.assetID,
				"name":                  "x",
				"source":                "youtube",
				"lifecycle_state":       "ACTIVE",
				"embedding_version_text": "2026-06-16-v1",
			}
			if tc.driveLink != "" {
				payload["drive_link"] = tc.driveLink
			}
			if tc.localPath != "" {
				payload["local_path"] = tc.localPath
			}

			mtr := &stubMetrics{}
			payloadStub := &stubPayload{}
			svc := fixtureService(t,
				versionCheckSchema(),
				&stubQdrant{pointsByID: map[string]pointWithID{
					tc.assetID: {ID: canonicalPointID(tc.assetID), Payload: payload},
				}},
				&stubSQLite{rows: []AssetSnapshot{
					{ID: tc.assetID, LifecycleState: "ACTIVE"},
				}},
				mtr,
				withOutbox(&stubOutbox{}),
				withPayload(payloadStub),
				withPointIDFor(canonicalPointID),
				withLog(zap.NewNop()),
			)
			_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			// Sum per-key RecordLegacyKeyStripped calls (the stub
			// emits one call per key, NOT per DeletePayloadKeys call).
			gotDrive := 0
			gotLocal := 0
			for _, s := range mtr.legacyStrips {
				switch s.legacyKey {
				case "drive_link":
					gotDrive += s.n
				case "local_path":
					gotLocal += s.n
				}
			}
			if gotDrive != tc.wantDriveLinks {
				t.Fatalf("drive_link strips=%d, want %d (all=%+v)", gotDrive, tc.wantDriveLinks, mtr.legacyStrips)
			}
			if gotLocal != tc.wantLocalPaths {
				t.Fatalf("local_path strips=%d, want %d (all=%+v)", gotLocal, tc.wantLocalPaths, mtr.legacyStrips)
			}
		})
	}
}

func TestNewServiceFromDeps_PanicsOnNilCore(t *testing.T) {
	cases := []struct {
		name    string
		mutator func(*ServiceDeps)
	}{
		{
			name:    "empty Schema.Version",
			mutator: func(d *ServiceDeps) { d.Schema.Version = "" },
		},
		{
			name:    "nil Qdrant",
			mutator: func(d *ServiceDeps) { d.Qdrant = nil },
		},
		{
			name:    "nil SQLite",
			mutator: func(d *ServiceDeps) { d.SQLite = nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := ServiceDeps{
				Schema: defaultSchema(),
				Qdrant: &stubQdrant{},
				SQLite: &stubSQLite{},
			}
			tc.mutator(&deps)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic for %s, got none", tc.name)
				}
			}()
			_ = NewServiceFromDeps(deps)
		})
	}
}
