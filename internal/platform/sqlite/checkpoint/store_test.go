package checkpoint

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	_ "github.com/mattn/go-sqlite3"
)

const testSchema = `
CREATE TABLE job_checkpoints (
    job_id TEXT NOT NULL,
    stage TEXT NOT NULL,
    unit_id TEXT NOT NULL,
    input_fingerprint TEXT NOT NULL,
    status TEXT NOT NULL,
    artifact_sha256 TEXT NOT NULL DEFAULT '',
    artifact_uri TEXT NOT NULL DEFAULT '',
    processor_version TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    PRIMARY KEY(job_id, stage, unit_id)
);`

func hash64(c byte) string { return strings.Repeat(string(c), 64) }

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:checkpoint-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(testSchema); err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testCheckpoint() capcheckpoint.Checkpoint {
	return capcheckpoint.Checkpoint{
		JobID:            "job_ABC",
		Stage:            capcheckpoint.StageRenderScene,
		UnitID:           "scene_01",
		InputFingerprint: hash64('1'),
		Status:           capcheckpoint.StatusCompleted,
		ArtifactSHA256:   hash64('a'),
		ArtifactURI:      "cas://" + hash64('a'),
		ProcessorVersion: "rust-render/v3",
		CompletedAt:      time.Date(2026, 8, 18, 12, 0, 0, 123456789, time.UTC),
	}
}

func TestGetMissReturnsNil(t *testing.T) {
	store := newTestStore(t)
	got, err := store.Get(context.Background(), "job_ABC", capcheckpoint.StageRenderScene, "scene_01")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("missing checkpoint must return nil, got %+v", got)
	}
}

func TestCompleteThenGetRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	want := testCheckpoint()
	if err := store.Complete(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, want.JobID, want.Stage, want.UnitID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("completed checkpoint must be readable")
	}
	if got.JobID != want.JobID || got.Stage != want.Stage || got.UnitID != want.UnitID ||
		got.InputFingerprint != want.InputFingerprint || got.Status != want.Status ||
		got.ArtifactSHA256 != want.ArtifactSHA256 || got.ArtifactURI != want.ArtifactURI ||
		got.ProcessorVersion != want.ProcessorVersion {
		t.Fatalf("checkpoint round-trip mismatch: got %+v want %+v", got, want)
	}
	if !got.CompletedAt.Equal(want.CompletedAt) {
		t.Fatalf("completed_at round-trip mismatch: got %v want %v", got.CompletedAt, want.CompletedAt)
	}
}

func TestCompleteUpsertsSameUnit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first := testCheckpoint()
	first.InputFingerprint = hash64('1')
	if err := store.Complete(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := testCheckpoint()
	second.InputFingerprint = hash64('2')
	second.ArtifactSHA256 = hash64('b')
	second.CompletedAt = second.CompletedAt.Add(time.Hour)
	if err := store.Complete(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, first.JobID, first.Stage, first.UnitID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.InputFingerprint != hash64('2') || got.ArtifactSHA256 != hash64('b') {
		t.Fatalf("re-completion must converge on the same row: %+v", got)
	}
}

func TestInvalidateRemovesCheckpoint(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	c := testCheckpoint()
	if err := store.Complete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := store.Invalidate(ctx, c.JobID, c.Stage, c.UnitID); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, c.JobID, c.Stage, c.UnitID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("invalidated checkpoint must read as missing, got %+v", got)
	}
}

func TestInvalidateMissingIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	if err := store.Invalidate(context.Background(), "job_ABC", capcheckpoint.StageRenderScene, "scene_99"); err != nil {
		t.Fatalf("invalidating a missing checkpoint must be a no-op: %v", err)
	}
}

func TestStoreKeepsUnitsIndependent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	scene1 := testCheckpoint()
	scene1.UnitID = "scene_01"
	scene2 := testCheckpoint()
	scene2.UnitID = "scene_02"
	scene2.InputFingerprint = hash64('2')
	if err := store.Complete(ctx, scene1); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, scene2); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, scene1.JobID, scene1.Stage, "scene_02")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.InputFingerprint != hash64('2') {
		t.Fatalf("units must be independent: %+v", got)
	}
	if err := store.Invalidate(ctx, scene1.JobID, scene1.Stage, "scene_01"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(ctx, scene1.JobID, scene1.Stage, "scene_01"); err != nil || got != nil {
		t.Fatalf("invalidating one unit must not affect others: got=%+v err=%v", got, err)
	}
}

func TestCompleteRejectsInvalidCheckpoint(t *testing.T) {
	store := newTestStore(t)
	c := testCheckpoint()
	c.InputFingerprint = ""
	if err := store.Complete(context.Background(), c); err == nil {
		t.Fatal("invalid checkpoint must not be persisted")
	}
}

func TestGetAndInvalidateRejectEmptyIdentity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Get(ctx, "", capcheckpoint.StageRenderScene, "scene_01"); err == nil {
		t.Fatal("empty job id must be rejected")
	}
	if err := store.Invalidate(ctx, "job_ABC", "", "scene_01"); err == nil {
		t.Fatal("empty stage must be rejected")
	}
}
