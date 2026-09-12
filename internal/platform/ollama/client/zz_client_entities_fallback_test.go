package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/stretchr/testify/require"
)

func TestExtractEntitiesFromSegment_InvalidStructuredResponseUsesHeuristicFallback(t *testing.T) {
	bootstrapTestLexicon(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, err := json.Marshal(map[string]string{"response": `{"frasi_importanti":["truncated"`})
		require.NoError(t, err)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	result, err := c.ExtractEntitiesFromSegmentWithModel(context.Background(), detail.EntityExtractionRequest{
		SegmentText: "Michael Jordan changed basketball through competitive focus and defensive intensity.",
		EntityCount: 5,
		Language:    "en",
	}, "gemma4:e4b")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "heuristic_fallback", result.Source)
	require.NotEmpty(t, result.FrasiImportanti)
}
