package assembly

import (
	"context"
	"database/sql"
	"testing"

	assembly "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assembly"
	contract "github.com/Marcuss-ops/PipelineGen/internal/kernel/assembly"
	_ "github.com/mattn/go-sqlite3"
)

func TestRepositoryRoundTripFinalizePlanAndArtifacts(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	s := &assembly.Session{AssemblyID: "a", ParentJobID: "p", Status: assembly.StatusWaitingRuntime, Revision: 1, RuntimeAssets: []contract.AssetRequirement{{AssetID: "voice", Kind: "voiceover", SHA256: "sha256:v", Availability: contract.AvailabilityKnown, Required: true}}, FinalizePlan: &contract.FinalizeV1{ContractVersion: contract.ContractVersion, AssemblyID: "a", PreparationID: "prep", Revision: 2, OutputContract: contract.OutputContract, Timeline: []contract.TimelineEntry{{SceneID: "s", AssetID: "clip"}}, RuntimeAssets: []contract.AssetRequirement{{AssetID: "voice", Kind: "voiceover", SHA256: "sha256:v", Availability: contract.AvailabilityKnown, Required: true}}}, Project: "proj"}
	if err := r.Put(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "proj" || got.FinalizePlan == nil || len(got.RuntimeAssets) != 1 {
		t.Fatalf("round trip lost state: %+v", got)
	}
}
