package scripts

import (
	"context"
	"encoding/json"
	"testing"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeCatalogSearch struct {
	results []appsearch.CatalogSearchResult
}

func (f *fakeCatalogSearch) SearchAll(ctx context.Context, query string) ([]appsearch.CatalogSearchResult, error) {
	return append([]appsearch.CatalogSearchResult(nil), f.results...), nil
}

func TestCatalogJobServiceImpl_SelectsClipIDsFromTopic(t *testing.T) {
	t.Parallel()

	svc := NewCatalogJobServiceImpl(
		&ClipSourceBuilder{},
		&Engine{},
		&fakeCatalogSearch{results: []appsearch.CatalogSearchResult{
			{ID: "clip-a", Name: "Alpha"},
			{ID: "clip-b", Name: "Beta"},
		}},
		zap.NewNop(),
	)

	payload := JobPayloadCatalogScript{
		Topic:       "observability",
		Title:       "",
		MaxClips:    2,
		MinCoverage: 0.5,
		Language:    "it",
		Tone:        "clear",
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := svc.HandleCatalogScriptGenerateJob(context.Background(), &job.Job{
		ID:      "job-1",
		Type:    job.TypeCatalogScriptGenerate,
		Payload: raw,
	}, &appjobs.JobTools{})
	require.NoError(t, err)
	require.Equal(t, true, result["ok"])
	require.Equal(t, "catalog_first", result["mode"])
	require.Equal(t, "observability", result["title"])
}
