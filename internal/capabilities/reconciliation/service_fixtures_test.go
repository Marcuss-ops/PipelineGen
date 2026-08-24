package reconciliation

import (
	"context"
	"os"
	"testing"

	"go.uber.org/zap"
)

// Shared service fixtures and dependency stubs for reconciler tests.
// Keeping these package-scoped lets each behavior file focus on one
// contract while preserving the exact production dependency wiring.

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
	reindex  []stubReindexCall
	deletes  []string
	failNext bool
}

// stubReindexCall captures one EnqueueReindex call so PR-10 +
// PR-11 tests can verify the content_hash fingerprint is propagated
// through the dispatch path.
type stubReindexCall struct {
	assetID     string
	contentHash string
	force       bool
}

func (s *stubOutbox) EnqueueReindex(ctx context.Context, assetID, contentHash string, force bool) error {
	if s.failNext {
		s.failNext = false
		return os.ErrInvalid
	}
	s.reindex = append(s.reindex, stubReindexCall{assetID: assetID, contentHash: contentHash, force: force})
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
	mode            string
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

type stubBadQdrant struct{ err error }

func (s *stubBadQdrant) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (Points, error) {
	return Points{}, s.err
}

type pagingQdrant struct {
	assetID    string
	payload    map[string]interface{}
	nextOffset string
	errAt      int
	err        error
	calls      int
}

func (p *pagingQdrant) ScrollPoints(ctx context.Context, _ string, _ string, _ int) (Points, error) {
	if p.errAt > 0 && p.calls == p.errAt-1 {
		p.calls++
		return Points{}, p.err
	}
	p.calls++
	return Points{
		Items: []PointSnapshot{{
			ID:      "pt-" + p.assetID,
			Payload: p.payload,
		}},
		NextOffset: p.nextOffset,
	}, nil
}
