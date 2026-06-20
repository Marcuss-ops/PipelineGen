// Tests for the canonical PR3-5b IndexHealth cross-check (Task 3 surface:
// QdrantHealthy + ChecksComplete + Degraded + SampleLimit + SampleSaturated +
// CountsAreLowerBounds). The shape of report.OK (requires all of:
// QdrantHealthy + ChecksComplete + zero drift + zero dead_letter) is also
// pinned here so future refactors cannot silently regress it.
package realtime

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	// drive alias matches internal/application/scripts/clip_source_test.go
	// which uses `drive.CanonicalMediaAssetsSchema`. The package's actual
	// name is `storage` (see internal/infrastructure/database/canonical.go::package storage),
	// so explicit aliasing is required.
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// indexHealthStore is a minimal fake of vectorstore.Store that lets the
// tests steer Health / OperationCollectionInfo / ListPointIDs return values
// without dragging in a real Qdrant. Every other Store method is a no-op.
type indexHealthStore struct {
	healthErr atomic.Value // error
	info      vectorstore.CollectionInfo
	infoErr   error
	ids       []string
	idsErr    error
}

func (s *indexHealthStore) setHealthErr(err error) { s.healthErr.Store(err) }

func (s *indexHealthStore) EnsureCollection(context.Context) error { return nil }
func (s *indexHealthStore) UpsertAsset(context.Context, vectorstore.VectorAsset) error {
	return nil
}
func (s *indexHealthStore) UpsertAssets(context.Context, []vectorstore.VectorAsset) error {
	return nil
}
func (s *indexHealthStore) Search(context.Context, vectorstore.SearchRequest) ([]vectorstore.SearchResult, error) {
	return nil, nil
}
func (s *indexHealthStore) DeleteAsset(context.Context, string) error { return nil }
func (s *indexHealthStore) Health(ctx context.Context) error {
	if v := s.healthErr.Load(); v != nil {
		if err, _ := v.(error); err != nil {
			return err
		}
	}
	return nil
}
func (s *indexHealthStore) CollectionInfo(context.Context) (*vectorstore.CollectionInfo, error) {
	return &s.info, s.infoErr
}
func (s *indexHealthStore) OperationCollectionInfo(context.Context) (*vectorstore.CollectionInfo, error) {
	return &s.info, s.infoErr
}
func (s *indexHealthStore) PhysicalCollectionInfo(context.Context) (*vectorstore.CollectionInfo, error) {
	return &s.info, s.infoErr
}
func (s *indexHealthStore) Close() error { return nil }
func (s *indexHealthStore) HybridSearch(context.Context, vectorstore.HybridSearchRequest) ([]vectorstore.SearchResult, error) {
	return nil, nil
}
func (s *indexHealthStore) IndexHealth(context.Context) (*vectorstore.IndexHealthReport, error) {
	return nil, nil
}
func (s *indexHealthStore) ListPointIDs(ctx context.Context, limit int) ([]string, error) {
	if s.idsErr != nil {
		return nil, s.idsErr
	}
	if limit > 0 && len(s.ids) > limit {
		return s.ids[:limit], nil
	}
	return s.ids, nil
}
func (s *indexHealthStore) DeletePoints(ctx context.Context, ids []string) error { return nil }
func (s *indexHealthStore) ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error { return nil }
func (s *indexHealthStore) CleanupStalePoints(context.Context, func(string, string, string) (bool, error)) (int, error) {
	return 0, nil
}

// newIndexHealthServiceWithStore returns a Service wired with the fake
// store wrapped in vectorstore.NewService (production wiring is
// *vectorstore.Service, not the Store interface directly).
// clips and outbox are nil — when nil, IndexHealth treats the dependent
// fields as zero (no drift, no pending/dead-letter) and the per-source
// success flags track the qdrant leg only.
func newIndexHealthServiceWithStore(vs vectorstore.Store) *Service {
	log := zap.NewNop()
	vectorSvc := vectorstore.NewService(vs, vectorstore.Config{}, log)
	return &Service{
		vectorSvc: vectorSvc,
		log:       log,
	}
}

// TestIndexHealth_ReportsQdrantHealthyFromProbe pins that Health() success
// populates QdrantHealthy=true. Pre-Task-3 this probe ran but its result
// was silently absorbed (the only path that needed it was the legacy raw-SQL
// fallback, which never read report.QdrantHealthy).
func TestIndexHealth_ReportsQdrantHealthyFromProbe(t *testing.T) {
	store := &indexHealthStore{}
	store.info = vectorstore.CollectionInfo{PointsCount: 0}
	svc := newIndexHealthServiceWithStore(store)

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}
	if !report.QdrantHealthy {
		t.Fatalf("QdrantHealthy=false on healthy probe; want true")
	}
	if !report.ChecksComplete {
		t.Fatalf("ChecksComplete=false on fully healthy probe+info+empty diff; want true")
	}
	if report.Degraded {
		t.Fatalf("Degraded=true on healthy probe; want false")
	}
	if !report.OK {
		t.Fatalf("OK=false on healthy probe with empty diff and outbox-disabled; want true")
	}
}

// TestIndexHealth_OfflineQdrantProducesDegraded pins the regression target:
// when the /readyz probe fails, IndexHealth must surface OK=false AND
// Degraded=true (so on-call sees the "fix Qdrant" cue) AND QdrantHealthy=false.
func TestIndexHealth_OfflineQdrantProducesDegraded(t *testing.T) {
	store := &indexHealthStore{}
	store.setHealthErr(errors.New("qdrant offline"))
	store.info = vectorstore.CollectionInfo{PointsCount: 0}
	svc := newIndexHealthServiceWithStore(store)

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}
	if report.QdrantHealthy {
		t.Fatalf("QdrantHealthy=true despite failing probe; want false")
	}
	if report.OK {
		t.Fatalf("OK=true despite failing qdrant probe; want false")
	}
	if !report.Degraded {
		t.Fatalf("Degraded=false on operational failure; want true (fix-Qdrant cue)")
	}
	if report.ChecksComplete {
		t.Fatalf("ChecksComplete=true despite probing failure; want false")
	}
}

// TestIndexHealth_SampleSaturatedWhenIdsCapReached pins the saturation
// contract: if the qdrant scroll fills the sample cap AND the total
// point count exceeds the cap, SampleSaturated and CountsAreLowerBounds
// are both true so operators do not trust the diff numbers as absolute.
func TestIndexHealth_SampleSaturatedWhenIdsCapReached(t *testing.T) {
	store := &indexHealthStore{}
	ids := make([]string, 0, IndexHealthSampleCap+1)
	for i := 0; i <= IndexHealthSampleCap; i++ {
		ids = append(ids, "id_"+strconv.Itoa(i))
	}
	store.ids = ids
	store.info = vectorstore.CollectionInfo{PointsCount: int64(IndexHealthSampleCap + 50)}
	svc := newIndexHealthServiceWithStore(store)

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}
	if !report.SampleSaturated {
		t.Fatalf("SampleSaturated=false when sample at cap and PointsCount > cap; want true")
	}
	if !report.CountsAreLowerBounds {
		t.Fatalf("CountsAreLowerBounds=false under saturation; want true")
	}
	if report.SampleLimit != IndexHealthSampleCap {
		t.Fatalf("SampleLimit=%d; want %d", report.SampleLimit, IndexHealthSampleCap)
	}
}

// TestIndexHealth_OperationCollectionInfoFailureFailsChecksComplete pins
// that when qdrant responds to the probe but OperationCollectionInfo errors,
// ChecksComplete=false and OK=false (but QdrantHealthy=true, so this is
// not the "fix Qdrant" path — Degraded must still be true because
// checks_complete drops).
func TestIndexHealth_OperationCollectionInfoFailureFailsChecksComplete(t *testing.T) {
	store := &indexHealthStore{}
	store.infoErr = errors.New("collection metadata fetch failed")
	store.ids = []string{}
	svc := newIndexHealthServiceWithStore(store)

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}
	if !report.QdrantHealthy {
		t.Fatalf("QdrantHealthy=false despite probe OK; want true")
	}
	if report.ChecksComplete {
		t.Fatalf("ChecksComplete=true despite OperationCollectionInfo failing; want false")
	}
	if !report.Degraded {
		t.Fatalf("Degraded=false on degraded source path; want true")
	}
	if report.OK {
		t.Fatalf("OK=true despite ChecksComplete=false; want false")
	}
}

// TestIndexHealth_OKGateWithRealClipsAndOutbox exercises the OK-gate with
// real on-disk repos for clips + outbox seeded via in-memory SQLite, so the
// drift / dead_letter branches are actually proven:
//   - qdrant ids empty (the sample is empty, simulating a freshly-emptied
//     alias during backfill) AND sqlite has indexed rows → recoverable
//     drift in MissingInQdrant, OK=false, Degraded=false (drift is ingestion,
//     not operational).
//   - three dead_letter rows seeded → OK=false even with zero drift.
func TestIndexHealth_OKGateWithRealClipsAndOutbox(t *testing.T) {
	// sqlite ":memory:" databases are PER-CONNECTION in the go-sqlite3
	// driver — without shared cache each repository call would land on a
	// fresh empty DB and reads wouldn't see seeded rows. The DSN forces
	// one shared in-memory DB, and a long-lived keeper connection keeps
	// it alive for the test lifetime.
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open shared-cache in-memory sqlite: %v", err)
	}
	keeper, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire keeper conn: %v", err)
	}
	t.Cleanup(func() { _ = keeper.Close(); _ = db.Close() })

	// Schema — sqlite.ClipsRepository and outbox.Repository read IndexHealth
	// (CountAll / CountIndexed / ListIndexedIDs / CountByStatus). The
	// media_assets block is composed from
	// internal/storage/canonical.go::CanonicalMediaAssetsSchema so the
	// 39-column projection in sqlite.ClipsRepository.mediaAssetColumns matches
	// the schema verbatim. The outbox_events block stays inline because
	// the realtime tests don't go through outbox.Repository.NewRepository.
	schema := drive.CanonicalMediaAssetsSchema + "\n" + `
CREATE TABLE outbox_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type      TEXT NOT NULL DEFAULT '',
    aggregate_id    TEXT NOT NULL DEFAULT '',
    aggregate_type  TEXT NOT NULL DEFAULT '',
    payload_json    TEXT NOT NULL DEFAULT '',
    event_key       TEXT NOT NULL DEFAULT '',
    status          TEXT,
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 10,
    last_error      TEXT,
    next_attempt_at TEXT,
    worker_id       TEXT NOT NULL DEFAULT '',
    lease_id        TEXT NOT NULL DEFAULT '',
    lease_expiry    TEXT,
    completed_at    TEXT,
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT ''
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	// Seed: one media_assets row with embedding_json (indexed), and three
	// dead_letter rows in the outbox so OK=false via the dead_letter gate.
	if _, err := db.Exec(`INSERT INTO media_assets(id, source, name, metadata_json, embedding_json) VALUES (?,?,?,?,?)`,
		"asset_a", "youtube", "Test", "{}", `{"text":[0.1]}`); err != nil {
		t.Fatalf("insert media_assets: %v", err)
	}
	deadLetterRows := []string{"dead 1", "dead 2", "dead 3"}
	for _, d := range deadLetterRows {
		if _, err := db.Exec(`INSERT INTO outbox_events(event_type, aggregate_id, status, last_error) VALUES (?,?,?,?)`,
			"asset.index.requested", d, "dead_letter", "test"); err != nil {
			t.Fatalf("insert dead_letter: %v", err)
		}
	}

	log := zap.NewNop()
	clipsRepo := sqlite.NewClipsRepository(db, log)
	outboxRepo := outboxevents.NewRepository(db)

	// qdrant sample is empty — so asset_a is missing-in-qdrant.
	store := &indexHealthStore{
		info: vectorstore.CollectionInfo{PointsCount: 0},
		ids:  []string{},
	}
	vectorSvc := vectorstore.NewService(store, vectorstore.Config{}, log)
	svc := &Service{
		vectorSvc: vectorSvc,
		clips:     clipsRepo,
		outbox:    outboxRepo,
		log:       log,
	}

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}
	if !report.QdrantHealthy {
		t.Fatalf("QdrantHealthy=false on healthy probe; want true")
	}
	if !report.ChecksComplete {
		t.Fatalf("ChecksComplete=false with all sources; want true")
	}
	if report.OK {
		t.Fatalf("OK=true despite MissingInQdrant=1 and DeadLetter=3; want false")
	}
	if report.MissingInQdrant == 0 {
		t.Fatalf("MissingInQdrant=0; want >0 (asset_a not in qdrant sample)")
	}
	if report.DeadLetter != int64(len(deadLetterRows)) {
		t.Fatalf("DeadLetter=%d; want %d", report.DeadLetter, len(deadLetterRows))
	}
	// drift is ingestion, not operational — Degraded must NOT be set when
	// QdrantHealthy=true and ChecksComplete=true.
	if report.Degraded {
		t.Fatalf("Degraded=true on data drift; want false (drift != operational)")
	}
}

// fakeIndexHealthClips satisfies realtime.IndexHealthClips. Each method
// returns caller-supplied seed values so tests can swap in a failing
// leg without touching the concrete *sqlite.ClipsRepository.
type fakeIndexHealthClips struct {
	countAllFn       func(context.Context) (int64, error)
	countIndexedFn   func(context.Context) (int64, error)
	listIndexedIDsFn func(context.Context, int) ([]string, error)
}

func (f *fakeIndexHealthClips) CountAll(ctx context.Context) (int64, error) {
	if f.countAllFn != nil {
		return f.countAllFn(ctx)
	}
	return 0, nil
}
func (f *fakeIndexHealthClips) CountIndexed(ctx context.Context) (int64, error) {
	if f.countIndexedFn != nil {
		return f.countIndexedFn(ctx)
	}
	return 0, nil
}
func (f *fakeIndexHealthClips) ListIndexedIDs(ctx context.Context, limit int) ([]string, error) {
	if f.listIndexedIDsFn != nil {
		return f.listIndexedIDsFn(ctx, limit)
	}
	return nil, nil
}

// fakeIndexHealthOutbox satisfies realtime.IndexHealthOutbox.
type fakeIndexHealthOutbox struct {
	countByStatusFn func(context.Context, string) (int64, error)
}

func (f *fakeIndexHealthOutbox) CountByStatus(ctx context.Context, status string) (int64, error) {
	if f.countByStatusFn != nil {
		return f.countByStatusFn(ctx, status)
	}
	return 0, nil
}

// TestIndexHealth_ClipsListingFailureAttribution pins the Task 6/7 split:
// when clips.ListIndexedIDs fails but the qdrant leg succeeds, ONLY
// "clips_listing" should appear in DegradedSources (NOT "qdrant_info").
// Guards against future refactors that collapse the (qdrantOK,
// sqliteListOK) tuple back into a single bool or swap the early-return
// guards. With the new IndexHealthClips interface seam (Task 7) the
// failing leg is injected without touching the real *sqlite.ClipsRepository.
func TestIndexHealth_ClipsListingFailureAttribution(t *testing.T) {
	store := &indexHealthStore{}
	store.info = vectorstore.CollectionInfo{PointsCount: 7}
	store.ids = []string{"q_a", "q_b", "q_c", "q_d", "q_e", "q_f", "q_g"}
	log := zap.NewNop()
	vectorSvc := vectorstore.NewService(store, vectorstore.Config{}, log)

	clipsFake := &fakeIndexHealthClips{
		countAllFn:     func(_ context.Context) (int64, error) { return 10, nil },
		countIndexedFn: func(_ context.Context) (int64, error) { return 8, nil },
		listIndexedIDsFn: func(_ context.Context, _ int) ([]string, error) {
			return nil, errors.New("simulated ListIndexedIDs failure")
		},
	}
	outboxFake := &fakeIndexHealthOutbox{
		countByStatusFn: func(_ context.Context, _ string) (int64, error) { return 0, nil },
	}

	svc := &Service{
		vectorSvc: vectorSvc,
		clips:     clipsFake,
		outbox:    outboxFake,
		log:       log,
	}

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}
	if !report.QdrantHealthy {
		t.Fatalf("QdrantHealthy=false on healthy probe; want true")
	}
	if report.QdrantPoints != 7 {
		t.Fatalf("QdrantPoints=%d; want 7 (OperationCollectionInfo returned it)", report.QdrantPoints)
	}
	if report.SQLiteAssets != 10 {
		t.Fatalf("SQLiteAssets=%d; want 10 (CountAll succeeded)", report.SQLiteAssets)
	}
	if report.SQLiteIndexed != 8 {
		t.Fatalf("SQLiteIndexed=%d; want 8 (CountIndexed succeeded)", report.SQLiteIndexed)
	}
	if report.MissingInQdrant != 0 {
		t.Fatalf("MissingInQdrant=%d despite ListIndexedIDs error; want 0 (diff not computed)", report.MissingInQdrant)
	}
	if report.OrphanInQdrant != 0 {
		t.Fatalf("OrphanInQdrant=%d despite ListIndexedIDs error; want 0 (diff not computed)", report.OrphanInQdrant)
	}
	if report.ChecksComplete {
		t.Fatalf("ChecksComplete=true despite clips.ListIndexedIDs failing; want false")
	}
	if !report.Degraded {
		t.Fatalf("Degraded=false on per-leg failure; want true")
	}
	if report.OK {
		t.Fatalf("OK=true despite ChecksComplete=false; want false")
	}
	if !slices.Contains(report.DegradedSources, "clips_listing") {
		t.Fatalf("DegradedSources=%v lacks \"clips_listing\"; want it", report.DegradedSources)
	}
	if slices.Contains(report.DegradedSources, "qdrant_info") {
		t.Fatalf("DegradedSources=%v erroneously contains \"qdrant_info\"; the qdrant leg succeeded", report.DegradedSources)
	}
	if slices.Contains(report.DegradedSources, "qdrant") {
		t.Fatalf("DegradedSources=%v erroneously contains \"qdrant\"; the probe succeeded", report.DegradedSources)
	}
	if slices.Contains(report.DegradedSources, "sqlite") {
		t.Fatalf("DegradedSources=%v erroneously contains \"sqlite\"; CountAll+CountIndexed succeeded", report.DegradedSources)
	}
	if slices.Contains(report.DegradedSources, "outbox") {
		t.Fatalf("DegradedSources=%v erroneously contains \"outbox\"; CountByStatus succeeded", report.DegradedSources)
	}
}

// TestIndexHealth_ClipsCountsFailureAttribution pins the Task 6/7 inverse
// leg of the clips split: when clips.CountAll/CountIndexed fail but
// ListIndexedIDs succeeds, ONLY "sqlite" should appear in
// DegradedSources (NOT "clips_listing"). Together with
// TestIndexHealth_ClipsListingFailureAttribution, the two tests prove
// that the per-leg attribution is genuinely separable — collapsing the
// flags back into a shared "clips_*" entry would be caught by either
// failing leg's test.
func TestIndexHealth_ClipsCountsFailureAttribution(t *testing.T) {
	store := &indexHealthStore{}
	store.info = vectorstore.CollectionInfo{PointsCount: 4}
	store.ids = []string{"q_a", "q_b", "q_c", "q_d"}
	log := zap.NewNop()
	vectorSvc := vectorstore.NewService(store, vectorstore.Config{}, log)

	clipsFake := &fakeIndexHealthClips{
		countAllFn:     func(_ context.Context) (int64, error) { return 0, errors.New("simulated CountAll failure") },
		countIndexedFn: func(_ context.Context) (int64, error) { return 0, errors.New("simulated CountIndexed failure") },
		listIndexedIDsFn: func(_ context.Context, _ int) ([]string, error) {
			// mirror the qdrant sample so the diff is empty even though
			// ListIndexedIDs succeeded; this isolates the failure to the
			// count leg without conflating it with drift.
			return []string{"q_a", "q_b", "q_c", "q_d"}, nil
		},
	}
	outboxFake := &fakeIndexHealthOutbox{
		countByStatusFn: func(_ context.Context, _ string) (int64, error) { return 0, nil },
	}

	svc := &Service{
		vectorSvc: vectorSvc,
		clips:     clipsFake,
		outbox:    outboxFake,
		log:       log,
	}

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}
	if !report.QdrantHealthy {
		t.Fatalf("QdrantHealthy=false on healthy probe; want true")
	}
	if report.SQLiteAssets != 0 {
		t.Fatalf("SQLiteAssets=%d despite CountAll failure; want 0", report.SQLiteAssets)
	}
	if report.SQLiteIndexed != 0 {
		t.Fatalf("SQLiteIndexed=%d despite CountIndexed failure; want 0", report.SQLiteIndexed)
	}
	if !slices.Contains(report.DegradedSources, "sqlite") {
		t.Fatalf("DegradedSources=%v lacks \"sqlite\"; want it", report.DegradedSources)
	}
	if slices.Contains(report.DegradedSources, "clips_listing") {
		t.Fatalf("DegradedSources=%v erroneously contains \"clips_listing\"; ListIndexedIDs succeeded", report.DegradedSources)
	}
	if slices.Contains(report.DegradedSources, "qdrant_info") {
		t.Fatalf("DegradedSources=%v erroneously contains \"qdrant_info\"; the qdrant leg succeeded", report.DegradedSources)
	}
	if !report.Degraded {
		t.Fatalf("Degraded=false on per-leg failure; want true")
	}
	if report.ChecksComplete {
		t.Fatalf("ChecksComplete=true despite CountAll/CountIndexed failures; want false")
	}
	if report.OK {
		t.Fatalf("OK=true despite ChecksComplete=false; want false")
	}
}

// TestIndexHealth_DegradedSourcesAppendOrderScenarioClipsDegraded pins
// the append order when the data-path leg clips_listing fails AFTER
// the qdrant info leg succeeded. Expected sub-order slice of
// DegradedSources: [clips_listing, sqlite, outbox] (or any subset
// preserving relative order between those three). qdrant_info is
// absent because qdrant_info succeeded (otherwise clips_listing would
// be unprobed — fetchQdrantScene short-circuits).
func TestIndexHealth_DegradedSourcesAppendOrderScenarioClipsDegraded(t *testing.T) {
	store := &indexHealthStore{}
	store.info = vectorstore.CollectionInfo{PointsCount: 4}
	store.ids = []string{"q_a", "q_b", "q_c", "q_d"}
	log := zap.NewNop()
	vectorSvc := vectorstore.NewService(store, vectorstore.Config{}, log)

	clipsFake := &fakeIndexHealthClips{
		countAllFn:       func(_ context.Context) (int64, error) { return 0, errors.New("counts down") },
		countIndexedFn:   func(_ context.Context) (int64, error) { return 0, errors.New("counts down") },
		listIndexedIDsFn: func(_ context.Context, _ int) ([]string, error) { return nil, errors.New("listing down") },
	}
	outboxFake := &fakeIndexHealthOutbox{
		countByStatusFn: func(_ context.Context, _ string) (int64, error) { return 0, errors.New("outbox down") },
	}

	svc := &Service{
		vectorSvc: vectorSvc,
		clips:     clipsFake,
		outbox:    outboxFake,
		log:       log,
	}

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}

	want := []string{"clips_listing", "sqlite", "outbox"}
	if len(report.DegradedSources) != len(want) {
		t.Fatalf("DegradedSources len=%d (%v); want %d (%v)", len(report.DegradedSources), report.DegradedSources, len(want), want)
	}
	for i, name := range want {
		if report.DegradedSources[i] != name {
			t.Fatalf("DegradedSources[%d]=%q; want %q (full: %v)", i, report.DegradedSources[i], name, report.DegradedSources)
		}
	}
}

// TestIndexHealth_DegradedSourcesAppendOrderScenarioQdrantInfoFailed
// pins the append order when the qdrant_info leg fails BEFORE reaching
// the clips.ListIndexedIDs call. Expected sub-order slice: [qdrant_info,
// qdrant, sqlite, outbox] (clips_listing is UNPROBED — fetchQdrantScene
// early-returned; it does not appear in DegradedSources). This is the
// complement of the clips-degraded scenario above; together they pin
// the full canonical append order across both branches of the qdrant
// leg's success/failure control flow.
func TestIndexHealth_DegradedSourcesAppendOrderScenarioQdrantInfoFailed(t *testing.T) {
	store := &indexHealthStore{}
	store.setHealthErr(errors.New("qdrant probe down"))
	store.infoErr = errors.New("qdrant info down")
	log := zap.NewNop()
	vectorSvc := vectorstore.NewService(store, vectorstore.Config{}, log)

	clipsFake := &fakeIndexHealthClips{
		countAllFn:     func(_ context.Context) (int64, error) { return 0, errors.New("counts down") },
		countIndexedFn: func(_ context.Context) (int64, error) { return 0, errors.New("counts down") },
		listIndexedIDsFn: func(_ context.Context, _ int) ([]string, error) {
			return nil, errors.New("listing down (UNPROBED path)")
		},
	}
	outboxFake := &fakeIndexHealthOutbox{
		countByStatusFn: func(_ context.Context, _ string) (int64, error) { return 0, errors.New("outbox down") },
	}

	svc := &Service{
		vectorSvc: vectorSvc,
		clips:     clipsFake,
		outbox:    outboxFake,
		log:       log,
	}

	report, err := svc.IndexHealth(context.Background())
	if err != nil {
		t.Fatalf("IndexHealth error: %v", err)
	}

	want := []string{"qdrant_info", "qdrant", "sqlite", "outbox"}
	if len(report.DegradedSources) != len(want) {
		t.Fatalf("DegradedSources len=%d (%v); want %d (%v)", len(report.DegradedSources), report.DegradedSources, len(want), want)
	}
	for i, name := range want {
		if report.DegradedSources[i] != name {
			t.Fatalf("DegradedSources[%d]=%q; want %q (full: %v)", i, report.DegradedSources[i], name, report.DegradedSources)
		}
	}
	if slices.Contains(report.DegradedSources, "clips_listing") {
		t.Fatalf("DegradedSources contains clips_listing: %v (clips_listing should be UNPROBED when qdrant_info failed early)", report.DegradedSources)
	}
}
