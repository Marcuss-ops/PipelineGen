package assembly

import (
	"context"
	"testing"

	contract "github.com/Marcuss-ops/PipelineGen/internal/kernel/assembly"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type recordingJobs struct{ enqueued []job.EnqueueRequest }

func (r *recordingJobs) Enqueue(_ context.Context, q *job.EnqueueRequest) (*job.Job, error) {
	r.enqueued = append(r.enqueued, *q)
	return &job.Job{ID: q.ActiveKey, Type: q.Type}, nil
}
func (*recordingJobs) Get(context.Context, string) (*job.Job, error)           { return nil, nil }
func (*recordingJobs) Cancel(context.Context, string) error                    { return nil }
func (*recordingJobs) List(context.Context, job.Filter) ([]job.Job, error)     { return nil, nil }
func (*recordingJobs) IsTerminal(job.Status) bool                              { return false }
func (*recordingJobs) RegisterHandler(string, any) error                       { return nil }
func (*recordingJobs) ListEvents(context.Context, string) ([]job.Event, error) { return nil, nil }
func (*recordingJobs) Retry(context.Context, string) (*job.Job, error)         { return nil, nil }

type fixedAssets struct{}

func (fixedAssets) Resolve(context.Context, []string) ([]contract.AssetRequirement, error) {
	return []contract.AssetRequirement{{AssetID: "clip-1", Kind: "source_clip", Location: "file:///clip-1", Availability: contract.AvailabilityKnown, Required: true}}, nil
}

func TestCoordinatorEnqueuesFinalizeOnlyAfterAllRequiredArtifacts(t *testing.T) {
	j := &recordingJobs{}
	sessions := NewMemorySessionRepository()
	c, err := NewCoordinator(j, fixedAssets{}, sessions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PrepareEarly(context.Background(), PrepareEarlyCommand{AssemblyID: "asm", ParentJobID: "parent", ClipIDs: []string{"clip-1"}}); err != nil {
		t.Fatal(err)
	}
	plan := contract.FinalizeV1{ContractVersion: contract.ContractVersion, AssemblyID: "asm", PreparationID: "prep", Revision: 2, OutputContract: contract.OutputContract, Timeline: []contract.TimelineEntry{{SceneID: "s1", AssetID: "clip-1"}}, RuntimeAssets: []contract.AssetRequirement{{AssetID: "voice", Kind: "voiceover", Required: true, Availability: contract.AvailabilityKnown, SHA256: "sha256:voice"}}}
	if err := c.RegisterFinalizePlan(context.Background(), FinalizePlanCommand{Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if len(j.enqueued) != 1 {
		t.Fatalf("finalize enqueued before runtime artifact: %d jobs", len(j.enqueued))
	}
	if err := c.RegisterArtifact(context.Background(), RegisterArtifactCommand{AssemblyID: "asm", Asset: plan.RuntimeAssets[0]}); err != nil {
		t.Fatal(err)
	}
	if len(j.enqueued) != 2 || j.enqueued[1].Type != contract.FinalizeJobType {
		t.Fatalf("finalize not enqueued after artifact: %+v", j.enqueued)
	}
}
