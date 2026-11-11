package model_test

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func TestSameSubmitRequestCanonicalizesAssetSetAndIgnoresSourceURL(t *testing.T) {
	first := client.Job{
		ID: "job-1", IdempotencyKey: "request-1", JobType: client.JobTypeRenderSegment,
		RenderPlan: json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1"}`),
		Assets: []client.AssetRef{
			{Hash: "a", LogicalPath: "a.mp4", SourceURL: "https://signed.example/old-a"},
			{Hash: "b", LogicalPath: "b.mp4", SourceURL: "https://signed.example/old-b"},
		},
	}
	retry := client.Job{
		ID: "job-2", IdempotencyKey: first.IdempotencyKey, JobType: first.JobType,
		RenderPlan: first.RenderPlan,
		Assets: []client.AssetRef{
			{Hash: "b", LogicalPath: "b.mp4", SourceURL: "https://signed.example/new-b"},
			{Hash: "a", LogicalPath: "a.mp4", SourceURL: "https://signed.example/new-a"},
		},
	}
	if !model.SameSubmitRequest(first, retry) {
		t.Fatal("asset order and refreshed source URLs must not change immutable request equivalence")
	}

	retry.Assets[0].LogicalPath = "other.mp4"
	if model.SameSubmitRequest(first, retry) {
		t.Fatal("a changed asset workspace path must change immutable request equivalence")
	}
}
