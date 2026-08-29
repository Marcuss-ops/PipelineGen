package jobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// preparationUnitsTableDDL matches the migration 242 canonical table surface
// (fingerprint keyed; v2 Control-Plane columns populated at plan/ready time).
const preparationUnitsTableDDL = `CREATE TABLE preparation_units (
  fingerprint TEXT PRIMARY KEY,
  unit_fingerprint TEXT NOT NULL DEFAULT '',
  unit_id TEXT NOT NULL DEFAULT '',
  unit_kind TEXT NOT NULL,
  job_type TEXT NOT NULL DEFAULT '',
  fingerprint_version TEXT NOT NULL DEFAULT '',
  processor_version TEXT NOT NULL DEFAULT '',
  input_manifest_json TEXT NOT NULL DEFAULT '{}',
  state TEXT NOT NULL DEFAULT 'PLANNED',
  resource_class TEXT NOT NULL DEFAULT 'CPU_LIGHT',
  cost_class TEXT NOT NULL DEFAULT 'MEDIUM',
  speculation_level INTEGER NOT NULL DEFAULT 0,
  expected_work_ms INTEGER NOT NULL DEFAULT 0,
  actual_work_ms INTEGER NOT NULL DEFAULT 0,
  result_kind TEXT NOT NULL DEFAULT 'NONE',
  result_ref TEXT NOT NULL DEFAULT '',
  result_metadata_json TEXT NOT NULL DEFAULT '{}',
  artifact_id TEXT NOT NULL DEFAULT '',
  cache_key TEXT NOT NULL DEFAULT '',
  result_json TEXT NOT NULL DEFAULT '{}',
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_expires_at TEXT,
  reusable INTEGER NOT NULL DEFAULT 1,
  preemptible INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  ready_at TEXT,
  expires_at TEXT,
  error TEXT NOT NULL DEFAULT ''
);`

const preparationUnitsTestSchema = preparationUnitsTableDDL + `
CREATE TABLE preparation_job_units (
  job_id TEXT NOT NULL, unit_id TEXT NOT NULL, fingerprint TEXT NOT NULL,
  required INTEGER NOT NULL DEFAULT 1, adopted INTEGER NOT NULL DEFAULT 0,
  queue_rank INTEGER, planned_at TEXT NOT NULL, adopted_at TEXT,
  PRIMARY KEY (job_id, unit_id)
);`

func newPreparationStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), preparationUnitsTestSchema); err != nil {
		t.Fatalf("create preparation schema: %v", err)
	}
	return db
}

func newPreparationTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	return NewSQLiteStore(newPreparationStoreTestDB(t), zap.NewNop())
}

// TestPreparationStore_PlannedToRunningToReady covers the full canonical
// state machine: PlanPreparationUnit seeds PLANNED, AcquirePreparationUnit
// flips it to RUNNING with a lease, MarkPreparationReady lands on READY, and
// a later acquire adopts the READY result (singleflight reuse).
func TestPreparationStore_PlannedToRunningToReady(t *testing.T) {
	store := newPreparationTestStore(t)
	ctx := context.Background()

	if err := store.PlanPreparationUnit(ctx, job.PreparationPlanInput{
		Fingerprint: "fp-planned", UnitID: "probe-1", UnitKind: "probe", JobType: "clip.render",
	}); err != nil {
		t.Fatalf("PlanPreparationUnit: %v", err)
	}

	planned, err := store.GetPreparationUnit(ctx, "fp-planned")
	if err != nil {
		t.Fatalf("GetPreparationUnit: %v", err)
	}
	if planned == nil || planned.State != job.PreparationPlanned {
		t.Fatalf("planned unit = %#v, want PLANNED", planned)
	}
	if planned.LeaseOwner != "" || planned.LeaseExpires != nil {
		t.Fatalf("planned unit must not hold a lease: %#v", planned)
	}

	claim := job.PreparationUnitClaim{
		Fingerprint: "fp-planned", UnitID: "probe-1", UnitKind: "probe",
		JobType: "clip.render", LeaseOwner: "owner-a", LeaseDuration: time.Minute,
	}
	first, owned, err := store.AcquirePreparationUnit(ctx, claim)
	if err != nil {
		t.Fatalf("AcquirePreparationUnit: %v", err)
	}
	if !owned || first.State != job.PreparationRunning || first.LeaseOwner != "owner-a" {
		t.Fatalf("acquire after plan: unit=%#v owned=%v", first, owned)
	}

	if err := store.MarkPreparationReady(ctx, job.PreparationReadyUpdate{
		Fingerprint: "fp-planned", LeaseOwner: "owner-a",
		ArtifactID: "artifact-planned", CacheKey: "cache-planned", Result: []byte(`{"ok":true}`),
	}); err != nil {
		t.Fatalf("MarkPreparationReady: %v", err)
	}

	adopted, ok, err := store.AcquirePreparationUnit(ctx, job.PreparationUnitClaim{
		Fingerprint: "fp-planned", UnitID: "probe-1", UnitKind: "probe",
		JobType: "clip.render", LeaseOwner: "owner-b", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("adopt acquire: %v", err)
	}
	if !ok || adopted.State != job.PreparationReady || adopted.ArtifactID != "artifact-planned" {
		t.Fatalf("adopt = unit=%#v ok=%v, want READY artifact-planned", adopted, ok)
	}
}

// TestPreparationStore_V2MetadataPersistsThroughPlanAndReady verifies the
// Control-Plane v2 columns (resource_class, cost_class, speculation_level,
// expected_work_ms, fingerprint/processor versions, generated result_kind/ref)
// are persisted at plan time and read back after the run completes.
func TestPreparationStore_V2MetadataPersistsThroughPlanAndReady(t *testing.T) {
	store := newPreparationTestStore(t)
	ctx := context.Background()

	err := store.PlanPreparationUnit(ctx, job.PreparationPlanInput{
		Fingerprint:        "fp-v2",
		UnitID:             "scene:1",
		UnitKind:           "tts.synthesize",
		JobType:            "script.generate",
		FingerprintVersion: "fingerprint/v2",
		ProcessorVersion:   "elevenlabs-v4",
		Inputs: job.InputManifest{
			"language":   "en",
			"voice_id":   "voice-42",
			"char_count": 250,
		},
		ResourceClass:    job.ResourceTTS,
		CostClass:        job.CostExpensive,
		SpeculationLevel: job.SpeculationLevel(3),
		ExpectedWorkMS:   3500,
		Reusable:         true,
		Preemptible:      true,
	})
	if err != nil {
		t.Fatalf("PlanPreparationUnit: %v", err)
	}

	planned, err := store.GetPreparationUnit(ctx, "fp-v2")
	if err != nil {
		t.Fatalf("GetPreparationUnit (planned): %v", err)
	}
	if planned == nil {
		t.Fatal("planned unit is nil")
	}
	if planned.ResourceClass != job.ResourceTTS {
		t.Fatalf("planned ResourceClass = %q, want TTS", planned.ResourceClass)
	}
	if planned.CostClass != job.CostExpensive {
		t.Fatalf("planned CostClass = %q, want EXPENSIVE", planned.CostClass)
	}
	if planned.SpeculationLevel != 3 {
		t.Fatalf("planned SpeculationLevel = %d, want 3", planned.SpeculationLevel)
	}
	if planned.ExpectedWorkMS != 3500 {
		t.Fatalf("planned ExpectedWorkMS = %d, want 3500", planned.ExpectedWorkMS)
	}
	if planned.ProcessorVersion != "elevenlabs-v4" || planned.FingerprintVersion != "fingerprint/v2" {
		t.Fatalf("planned versions = %q / %q", planned.ProcessorVersion, planned.FingerprintVersion)
	}

	_, owned, err := store.AcquirePreparationUnit(ctx, job.PreparationUnitClaim{
		Fingerprint: "fp-v2", UnitID: "scene:1", UnitKind: "tts.synthesize",
		JobType: "script.generate", LeaseOwner: "owner-a", LeaseDuration: time.Minute,
	})
	if err != nil || !owned {
		t.Fatalf("acquire: owned=%v err=%v", owned, err)
	}

	if err := store.MarkPreparationReady(ctx, job.PreparationReadyUpdate{
		Fingerprint: "fp-v2", LeaseOwner: "owner-a",
		ArtifactID: "artifact-v2", CacheKey: "cache-v2",
		ActualWorkMS: 3922,
		ResultKind:   job.ResultArtifactCache,
		ResultRef:    "cache-v2",
		Result:       []byte(`{"done":true}`),
	}); err != nil {
		t.Fatalf("MarkPreparationReady: %v", err)
	}

	ready, ok, err := store.AcquirePreparationUnit(ctx, job.PreparationUnitClaim{
		Fingerprint: "fp-v2", UnitID: "scene:1", UnitKind: "tts.synthesize",
		JobType: "script.generate", LeaseOwner: "owner-b", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("adopt acquire: %v", err)
	}
	if !ok || ready.State != job.PreparationReady {
		t.Fatalf("adopt = ok=%v state=%v", ok, ready.State)
	}
	if ready.ResultKind != job.ResultArtifactCache || ready.ResultRef != "cache-v2" {
		t.Fatalf("ready ResultKind/Ref = %q / %q, want ARTIFACT_CACHE / cache-v2", ready.ResultKind, ready.ResultRef)
	}
	// v2 planning metadata survives through ready + readback.
	if ready.ResourceClass != job.ResourceTTS || ready.CostClass != job.CostExpensive || ready.ExpectedWorkMS != 3500 {
		t.Fatalf("ready v2 metadata degraded: %#v", ready)
	}
}

// TestPreparationStore_PlanIsIdempotent verifies PlanPreparationUnit never
// regresses an existing row (singleflight win): planning a fingerprint that
// is already RUNNING or READY is a no-op, not a reset to PLANNED.
func TestPreparationStore_PlanIsIdempotent(t *testing.T) {
	store := newPreparationTestStore(t)
	ctx := context.Background()

	claim := job.PreparationUnitClaim{
		Fingerprint: "fp-idem", UnitID: "u", UnitKind: "tts", JobType: "script.generate",
		LeaseOwner: "owner-a", LeaseDuration: time.Minute,
	}
	if _, owned, err := store.AcquirePreparationUnit(ctx, claim); err != nil || !owned {
		t.Fatalf("first acquire: owned=%v err=%v", owned, err)
	}
	// Planning the same fingerprint must not reset the RUNNING row.
	if err := store.PlanPreparationUnit(ctx, job.PreparationPlanInput{
		Fingerprint: "fp-idem", UnitID: "u", UnitKind: "tts", JobType: "script.generate",
	}); err != nil {
		t.Fatalf("PlanPreparationUnit on RUNNING row: %v", err)
	}
	got, err := store.GetPreparationUnit(ctx, "fp-idem")
	if err != nil {
		t.Fatalf("GetPreparationUnit: %v", err)
	}
	if got == nil || got.State != job.PreparationRunning || got.LeaseOwner != "owner-a" {
		t.Fatalf("plan regressed RUNNING row: %#v", got)
	}
}

// TestPreparationStore_ExpireReadyUnitsMarksStale covers the expiry sweep:
// READY rows with expires_at <= now flip to STALE (lease cleared) and count
// is returned; unexpired and non-READY rows are untouched. A STALE row is
// reclaimable by a new AcquirePreparationUnit.
func TestPreparationStore_ExpireReadyUnitsMarksStale(t *testing.T) {
	store := newPreparationTestStore(t)
	ctx := context.Background()

	plan := func(fingerprint string) {
		t.Helper()
		if err := store.PlanPreparationUnit(ctx, job.PreparationPlanInput{
			Fingerprint: fingerprint, UnitID: fingerprint, UnitKind: "tts", JobType: "script.generate",
		}); err != nil {
			t.Fatalf("plan %s: %v", fingerprint, err)
		}
	}
	runReady := func(fingerprint string, expiresAt *time.Time) {
		t.Helper()
		plan(fingerprint)
		if _, owned, err := store.AcquirePreparationUnit(ctx, job.PreparationUnitClaim{
			Fingerprint: fingerprint, UnitID: fingerprint, UnitKind: "tts",
			JobType: "script.generate", LeaseOwner: "owner-a", LeaseDuration: time.Minute,
		}); err != nil || !owned {
			t.Fatalf("acquire %s: owned=%v err=%v", fingerprint, owned, err)
		}
		if err := store.MarkPreparationReady(ctx, job.PreparationReadyUpdate{
			Fingerprint: fingerprint, LeaseOwner: "owner-a", ArtifactID: "a-" + fingerprint,
			ExpiresAt: expiresAt,
		}); err != nil {
			t.Fatalf("ready %s: %v", fingerprint, err)
		}
	}

	now := time.Now().UTC()
	runReady("fp-expired", timePtr(now.Add(-time.Minute)))
	runReady("fp-future", timePtr(now.Add(time.Hour)))
	runReady("fp-no-expiry", nil)

	expired, err := store.ExpirePreparationUnits(ctx, now)
	if err != nil {
		t.Fatalf("ExpirePreparationUnits: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}

	got, err := store.GetPreparationUnit(ctx, "fp-expired")
	if err != nil {
		t.Fatalf("GetPreparationUnit fp-expired: %v", err)
	}
	if got == nil || got.State != job.PreparationStale || got.LeaseOwner != "" {
		t.Fatalf("expired row = %#v, want STALE without lease", got)
	}
	for _, fp := range []string{"fp-future", "fp-no-expiry"} {
		got, err := store.GetPreparationUnit(ctx, fp)
		if err != nil {
			t.Fatalf("GetPreparationUnit %s: %v", fp, err)
		}
		if got == nil || got.State != job.PreparationReady {
			t.Fatalf("%s = %#v, want READY (untouched)", fp, got)
		}
	}

	// STALE row is reclaimable: a new acquire restarts it as RUNNING.
	reclaimed, owned, err := store.AcquirePreparationUnit(ctx, job.PreparationUnitClaim{
		Fingerprint: "fp-expired", UnitID: "fp-expired", UnitKind: "tts",
		JobType: "script.generate", LeaseOwner: "owner-b", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("reclaim stale: %v", err)
	}
	if !owned || reclaimed.State != job.PreparationRunning || reclaimed.LeaseOwner != "owner-b" {
		t.Fatalf("reclaim = unit=%#v owned=%v, want RUNNING owned by owner-b", reclaimed, owned)
	}
}

// TestPreparationJobUnits_RegisterListAdopt covers the job→unit mapping
// surface: idempotent registration, deterministic ordering, and the
// adopted flag flip with adopted_at timestamp.
func TestPreparationJobUnits_RegisterListAdopt(t *testing.T) {
	store := newPreparationTestStore(t)
	ctx := context.Background()

	rank := func(i int) *int { return &i }
	inputs := []job.RegisterPreparationJobUnitInput{
		{JobID: "job-1", UnitID: "tts:1", Fingerprint: "fp-tts-1", Required: true, QueueRank: rank(2)},
		{JobID: "job-1", UnitID: "probe:1", Fingerprint: "fp-probe-1", Required: true, QueueRank: rank(1)},
		{JobID: "job-1", UnitID: "research:1", Fingerprint: "fp-research-1", Required: false, QueueRank: nil},
	}
	for _, in := range inputs {
		if err := store.RegisterPreparationJobUnit(ctx, in); err != nil {
			t.Fatalf("RegisterPreparationJobUnit %s: %v", in.UnitID, err)
		}
	}
	// Duplicate registration is a no-op (INSERT OR IGNORE).
	if err := store.RegisterPreparationJobUnit(ctx, inputs[0]); err != nil {
		t.Fatalf("re-register: %v", err)
	}

	units, err := store.ListPreparationJobUnits(ctx, "job-1")
	if err != nil {
		t.Fatalf("ListPreparationJobUnits: %v", err)
	}
	if len(units) != 3 {
		t.Fatalf("len(units) = %d, want 3", len(units))
	}
	wantOrder := []string{"probe:1", "tts:1", "research:1"} // rank 1, rank 2, NULL last
	for i, want := range wantOrder {
		if units[i].UnitID != want {
			t.Fatalf("units[%d].UnitID = %q, want %q (order)", i, units[i].UnitID, want)
		}
	}
	if !units[0].Required || units[2].Required {
		t.Fatalf("required flags wrong: %#v", units)
	}
	if units[0].Adopted {
		t.Fatal("fresh mapping must not be adopted")
	}
	if units[0].QueueRank == nil || *units[0].QueueRank != 1 {
		t.Fatalf("queue rank = %#v, want 1", units[0].QueueRank)
	}

	if err := store.MarkPreparationJobUnitAdopted(ctx, "job-1", "probe:1"); err != nil {
		t.Fatalf("MarkPreparationJobUnitAdopted: %v", err)
	}
	units, err = store.ListPreparationJobUnits(ctx, "job-1")
	if err != nil {
		t.Fatalf("ListPreparationJobUnits after adopt: %v", err)
	}
	if !units[0].Adopted || units[0].AdoptedAt == nil {
		t.Fatalf("adopted flag/at not set: %#v", units[0])
	}
	if units[1].Adopted {
		t.Fatal("sibling unit must stay unadopted")
	}

	// Adoption of an unknown mapping surfaces sql.ErrNoRows for strict callers.
	if err := store.MarkPreparationJobUnitAdopted(ctx, "job-1", "never-planned"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("adopt unknown = %v, want sql.ErrNoRows", err)
	}
	// Unknown job lists empty, not nil.
	empty, err := store.ListPreparationJobUnits(ctx, "job-nope")
	if err != nil {
		t.Fatalf("ListPreparationJobUnits unknown job: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("unknown job units = %#v, want empty non-nil", empty)
	}
}

// TestPreparationJobUnits_SharedFingerprintAcrossJobs verifies the cross-job
// singleflight projection: two jobs referencing the same fingerprint see one
// global prepared unit (via the store) but independent job→unit rows.
func TestPreparationJobUnits_SharedFingerprintAcrossJobs(t *testing.T) {
	store := newPreparationTestStore(t)
	ctx := context.Background()

	for _, in := range []job.RegisterPreparationJobUnitInput{
		{JobID: "job-a", UnitID: "tts:1", Fingerprint: "shared-fp"},
		{JobID: "job-b", UnitID: "tts:1", Fingerprint: "shared-fp"},
	} {
		if err := store.RegisterPreparationJobUnit(ctx, in); err != nil {
			t.Fatalf("register %s: %v", in.JobID, err)
		}
	}
	for _, jobID := range []string{"job-a", "job-b"} {
		units, err := store.ListPreparationJobUnits(ctx, jobID)
		if err != nil {
			t.Fatalf("list %s: %v", jobID, err)
		}
		if len(units) != 1 || units[0].Fingerprint != "shared-fp" {
			t.Fatalf("%s units = %#v, want one shared-fp mapping", jobID, units)
		}
	}
}

func timePtr(t time.Time) *time.Time { return &t }
