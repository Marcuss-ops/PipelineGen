package wiring

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
)

type recordingImagesSemanticWriter struct {
	request semantic.WriteRequest
}

func (r *recordingImagesSemanticWriter) GeneratePayload(context.Context, semantic.WriteRequest) (*semantic.Payload, string, error) {
	return nil, "", nil
}

func (r *recordingImagesSemanticWriter) Write(_ context.Context, req semantic.WriteRequest) (*semantic.WriteResult, error) {
	r.request = req
	return &semantic.WriteResult{LocalPath: req.LocalPath, Payload: &semantic.Payload{AssetID: req.AssetID, Tags: []string{"tag"}}}, nil
}

func TestImagesSemanticAdapterPreservesContract(t *testing.T) {
	inner := &recordingImagesSemanticWriter{}
	port := newImagesSemanticAdapter(inner)
	result, err := port.Write(context.Background(), imgservice.SemanticWriteRequest{
		AssetID: "asset-1", SourceType: "retrieved", Generator: "wikipedia",
		Prompt: "a cat", LocalPath: "/tmp/cat.jpg", Extensions: []map[string]any{{"type": "image"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inner.request.AssetID != "asset-1" || inner.request.SourceType != "retrieved" || inner.request.LocalPath != "/tmp/cat.jpg" {
		t.Fatalf("request mapping lost fields: %+v", inner.request)
	}
	if result == nil || result.LocalPath != "/tmp/cat.jpg" || result.Payload == nil || result.Payload.Tags[0] != "tag" {
		t.Fatalf("result mapping incorrect: %+v", result)
	}
}
