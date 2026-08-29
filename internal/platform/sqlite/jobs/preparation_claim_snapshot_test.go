package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// snapshotTestSchema mirrors the production preparation tables the
// SnapshotPreparationClaim query reads/writes: preparation_units uses the
// canonical v2 table (migration 242/244) and the claim snapshots table is the
// migration 248 surface.
const snapshotTestSchema = preparationUnitsTableDDL + `
CREATE TABLE preparation_job_units (
  job_id TEXT NOT NULL, unit_id TEXT NOT NULL, fingerprint TEXT NOT NULL,
  required INTEGER NOT NULL DEFAULT 1, adopted INTEGER NOT NULL DEFAULT 0,
  queue_rank INTEGER, planned_at TEXT NOT NULL, adopted_at TEXT,
  PRIMARY KEY (job_id, unit_id)
);
CREATE TABLE preparation_claim_snapshots (
  job_id TEXT NOT NULL, attempt_id TEXT NOT NULL, job_revision INTEGER NOT NULL DEFAULT 0,
  claimed_at TEXT NOT NULL, total_units INTEGER NOT NULL DEFAULT 0,
  required_units INTEGER NOT NULL DEFAULT 0, ready_units INTEGER NOT NULL DEFAULT 0,
  running_units INTEGER NOT NULL DEFAULT 0, missing_units INTEGER NOT NULL DEFAULT 0,
  prepared_ratio REAL NOT NULL DEFAULT 0, estimated_saved_ms INTEGER NOT NULL DEFAULT 0,
  speculative_work_ms INTEGER NOT NULL DEFAULT 0, queue_wait_ms INTEGER NOT NULL DEFAULT 0,
  queue_position_at_plan INTEGER NOT NULL DEFAULT 0, metadata_json TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (job_id, attempt_id)
);`

func newSnapshotTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), snapshotTestSchema); err != nil {
		t.Fatalf("create snapshot schema: %v", err)
	}
	return NewSQLiteStore(db, zap.NewNop())
}

// seedUnit flips a preparation unit into the desired state with a workload.
func seedUnit(t *testing.T, store *SQLiteStore, fp, unitID, jobType, state string, expectedWorkMS int64) {
	t.Helper()
	ctx := context.Background()
	if err := store.PlanPreparationUnit(ctx, job.PreparationPlanInput{Fingerprint: fp, UnitID: unitID, UnitKind: "probe", JobType: jobType}); err != nil {
		t.Fatalf("plan %s: %v", fp, err)
	}
	claim := job.PreparationUnitClaim{Fingerprint: fp, UnitID: unitID, UnitKind: "probe", JobType: jobType, LeaseOwner: "owner-a", LeaseDuration: time.Minute}
	if _, owned, err := store.AcquirePreparationUnit(ctx, claim); err != nil || !owned {
		t.Fatalf("acquire %s: owned=%v err=%v", fp, owned, err)
	}
	switch state {
	case "READY":
		if err := store.MarkPreparationReady(ctx, job.PreparationReadyUpdate{Fingerprint: fp, LeaseOwner: "owner-a", ArtifactID: "a-" + fp}); err != nil {
			t.Fatalf("ready %s: %v", fp, err)
		}
	case "RUNNING":
		// leave as acquired
	default:
		t.Fatalf("unsupported seed state %q", state)
	}
	if expectedWorkMS > 0 {
		if _, err := store.db.ExecContext(context.Background(), `UPDATE preparation_units SET expected_work_ms=? WHERE fingerprint=?`, expectedWorkMS, fp); err != nil {
			t.Fatalf("set expected_work_ms %s: %v", fp, err)
		}
	}
}

func TestPreparationStore_SnapshotPreparationClaim_KPI(t *testing.T) {
	store := newSnapshotTestStore(t)
	ctx := context.Background()

	// Job 901: 4 required units. 2 READY (with known work), 1 RUNNING, 1 MISSING.
	jobs := map[string]string{
		"tts-ready":   "READY",
		"clip-ready":  "READY",
		"render-run":  "RUNNING",
		"nlp-missing": "", // not seeded → MISSING
	}
	seeded := map[string]int64{
		"fp-tts-ready":  3500,
		"fp-clip-ready": 5000,
		"fp-render-run": 12000,
		"fp-nlp-missing": 0,
	}
	// register every superidentity as a job unit; all required except the last.
	i := 0
	for legacyID, state := range jobs {
		fp := "fp-" + legacyID
		unitID := "unit-" + legacyID
		required := legacyID != "nlp-missing"
		if err := store.RegisterPreparationJobUnit(ctx, job.RegisterPreparationJobUnitInput{
			JobID: "job-901", UnitID: unitID, Fingerprint: fp, Required: required, QueueRank: func(i int) *int { return &i }(i),
		}); err != nil {
			t.Fatalf("register %s: %v", legacyID, err)
		}
		if state == "READY" || state == "RUNNING" {
			seedUnit(t, store, fp, unitID, "script.generate", state, seeded[fp])
		}
		i++
	}

	snapshot, err := store.SnapshotPreparationClaim(ctx, job.PreparationClaimInput{
		JobID: "job-901", AttemptID: "att-1", JobRevision: 7, ClaimedAt: time.Now().UTC(),
		QueueWaitMS: 2500, QueuePositionAtPlan: 41,
	})
	if err != nil {
		t.Fatalf("SnapshotPreparationClaim: %v", err)
	}
	if snapshot == nil {
		t.Fatal("snapshot is nil")
	}

	// required = 3 (nlp-missing is optional), so prepared_at_claim = ready/required = 2/3.
	if snapshot.RequiredUnits != 3 {
		t.Fatalf("RequiredUnits = %d, want 3", snapshot.RequiredUnits)
	}
	if snapshot.TotalUnits != 4 {
		t.Fatalf("TotalUnits = %d, want 4", snapshot.TotalUnits)
	}
	if snapshot.ReadyUnits != 2 {
		t.Fatalf("ReadyUnits = %d, want 2", snapshot.ReadyUnits)
	}
	if snapshot.RunningUnits != 1 {
		t.Fatalf("RunningUnits = %d, want 1", snapshot.RunningUnits)
	}
	if snapshot.MissingUnits != 1 {
		t.Fatalf("MissingUnits = %d, want 1 (nlp-missing optional counts as missing)", snapshot.MissingUnits)
	}
	wantRatio := 2.0 / 3.0
	if snapshot.PreparedAtClaimRatio < wantRatio-1e-9 || snapshot.PreparedAtClaimRatio > wantRatio+1e-9 {
		t.Fatalf("PreparedAtClaimRatio = %v, want ~%v", snapshot.PreparedAtClaimRatio, wantRatio)
	}
	if snapshot.EstimatedSavedMS != 8500 {
		t.Fatalf("EstimatedSavedMS = %d, want 8500 (3500+5000 READY required work)", snapshot.EstimatedSavedMS)
	}
	if snapshot.SpeculativeWorkMS != 12000 {
		t.Fatalf("SpeculativeWorkMS = %d, want 12000 (RUNNING required work)", snapshot.SpeculativeWorkMS)
	}
	if snapshot.JobRevision != 7 || snapshot.AttemptID != "att-1" {
		t.Fatalf("identity/revision not carried: %#v", snapshot)
	}

	// Band classification: 0.667 → "normal" (50-80%).
	if band := job.PreparationClaimBandName(snapshot.PreparedAtClaimRatio); band != "normal" {
		t.Fatalf("band(%v) = %q, want normal", snapshot.PreparedAtClaimRatio, band)
	}

	// Re-snapshot same attempt is an upsert (still one row), a new revision inserts fresh.
	if _, err := store.SnapshotPreparationClaim(ctx, job.PreparationClaimInput{JobID: "job-901", AttemptID: "att-1", JobRevision: 7, ClaimedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("re-snapshot: %v", err)
	}
	if _, err := store.SnapshotPreparationClaim(ctx, job.PreparationClaimInput{JobID: "job-901", AttemptID: "att-2", JobRevision: 8, ClaimedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("revision snapshot: %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM preparation_claim_snapshots WHERE job_id='job-901'`).Scan(&count); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if count != 2 {
		t.Fatalf("snapshot rows = %d, want 2 (upsert + new revision)", count)
	}
}

func TestPreparationClaim_Bands(t *testing.T) {
	cases := []struct {
		ratio float64
		want  string
	}{
		{0.0, "cold"},
		{0.19, "cold"},
		{0.20, "normal"},
		{0.79, "normal"},
		{0.80, "speculative"},
		{0.949, "speculative"},
		{0.95, "warm"},
		{1.0, "warm"},
	}
	for _, c := range cases {
		if got := job.PreparationClaimBandName(c.ratio); got != c.want {
			t.Fatalf("band(%v) = %q, want %q", c.ratio, got, c.want)
		}
	}
}