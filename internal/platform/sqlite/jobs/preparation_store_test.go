package jobs

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestPreparationStore_AcquireSingleflightAndReadyAdoption(t *testing.T) {
	db := newBrokerTestDB(t)
	if _, err := db.Exec(preparationUnitsTableDDL); err != nil {
		t.Fatalf("create preparation_units: %v", err)
	}
	store := NewSQLiteStore(db, zap.NewNop())
	claim := job.PreparationUnitClaim{Fingerprint: "fp-1", UnitID: "u-1", UnitKind: "probe", JobType: "clip.render", LeaseOwner: "owner-a", LeaseDuration: time.Minute}

	first, owned, err := store.AcquirePreparationUnit(context.Background(), claim)
	if err != nil || !owned || first.State != job.PreparationRunning {
		t.Fatalf("first acquire: unit=%#v owned=%v err=%v", first, owned, err)
	}
	second, owned, err := store.AcquirePreparationUnit(context.Background(), job.PreparationUnitClaim{Fingerprint: "fp-1", UnitID: "u-1", UnitKind: "probe", JobType: "clip.render", LeaseOwner: "owner-b", LeaseDuration: time.Minute})
	if err != nil || owned || second.LeaseOwner != "owner-a" {
		t.Fatalf("second acquire should join existing singleflight: unit=%#v owned=%v err=%v", second, owned, err)
	}

	if err := store.MarkPreparationReady(context.Background(), job.PreparationReadyUpdate{Fingerprint: "fp-1", LeaseOwner: "owner-a", ArtifactID: "artifact-1", CacheKey: "cache-1", Result: []byte(`{"ok":true}`)}); err != nil {
		t.Fatalf("MarkPreparationReady: %v", err)
	}
	ready, adopted, err := store.AcquirePreparationUnit(context.Background(), job.PreparationUnitClaim{Fingerprint: "fp-1", UnitID: "u-1", UnitKind: "probe", JobType: "clip.render", LeaseOwner: "owner-b", LeaseDuration: time.Minute})
	if err != nil || !adopted || ready.State != job.PreparationReady || ready.ArtifactID != "artifact-1" {
		t.Fatalf("ready adoption: unit=%#v adopted=%v err=%v", ready, adopted, err)
	}
}

func TestPreparationStore_ExpiredLeaseCanBeReclaimedAndOldOwnerCannotComplete(t *testing.T) {
	db := newBrokerTestDB(t)
	if _, err := db.Exec(preparationUnitsTableDDL); err != nil {
		t.Fatalf("create preparation_units: %v", err)
	}
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()
	first, owned, err := store.AcquirePreparationUnit(ctx, job.PreparationUnitClaim{Fingerprint: "fp-2", UnitID: "u-2", UnitKind: "download", JobType: "clip.render", LeaseOwner: "owner-a", LeaseDuration: time.Nanosecond})
	if err != nil || !owned || first == nil {
		t.Fatalf("first acquire: %#v %v %v", first, owned, err)
	}
	time.Sleep(2 * time.Millisecond)
	_, owned, err = store.AcquirePreparationUnit(ctx, job.PreparationUnitClaim{Fingerprint: "fp-2", UnitID: "u-2", UnitKind: "download", JobType: "clip.render", LeaseOwner: "owner-b", LeaseDuration: time.Minute})
	if err != nil || !owned {
		t.Fatalf("expired lease reclaim: owned=%v err=%v", owned, err)
	}
	if err := store.MarkPreparationReady(ctx, job.PreparationReadyUpdate{Fingerprint: "fp-2", LeaseOwner: "owner-a", ArtifactID: "stale"}); err == nil {
		t.Fatal("old owner unexpectedly marked reclaimed unit ready")
	}
	if err := store.MarkPreparationReady(ctx, job.PreparationReadyUpdate{Fingerprint: "fp-2", LeaseOwner: "owner-b", ArtifactID: "fresh"}); err != nil {
		t.Fatalf("new owner MarkPreparationReady: %v", err)
	}
}
