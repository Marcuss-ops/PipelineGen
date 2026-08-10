package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/require"
)

func TestExtractEntitiesFromSegment_UsesBoundedOperationBudget(t *testing.T) {
	var request struct {
		Model   string         `json:"model"`
		Options map[string]any `json:"options"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"## frasi_importanti\n- test phrase\n## entity_senza_testo\n## nomi_speciali\n- PERSON: Ada Lovelace\n## parole_importanti\n- test\n## artlist_phrases\n- testing"}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	result, err := c.ExtractEntitiesFromSegmentWithModel(context.Background(), asset.EntityExtractionRequest{
		SegmentText:  "A test segment.",
		SegmentIndex: 3,
		EntityCount:  5,
	}, "gemma4:e4b")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gemma4:e4b", request.Model)
	require.Equal(t, float64(entityExtractionNumPredict), request.Options["num_predict"])
}

func TestExtractEntitiesFromBatch_PreservesEverySegment(t *testing.T) {
	var request struct {
		Prompt  string         `json:"prompt"`
		Options map[string]any `json:"options"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"### SEGMENT_INDEX: 0\n## frasi_importanti\n- phrase zero\n## entity_senza_testo\n## nomi_speciali\n- PERSON: Ada Lovelace\n## parole_importanti\n- machine\n## artlist_phrases\n- analytical engine\n### END_SEGMENT\n### SEGMENT_INDEX: 1\n## frasi_importanti\n- phrase one\n## entity_senza_testo\n## nomi_speciali\n- PLACE: London\n## parole_importanti\n- science\n## artlist_phrases\n- Victorian street\n### END_SEGMENT"}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma3:1b", 5)
	results, err := c.ExtractEntitiesFromBatchWithModel(context.Background(), []string{"scene zero", "scene one"}, 5, "gemma3:1b")

	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "Ada Lovelace", strings.TrimPrefix(results[0].NomiSpeciali[0], "PERSON: "))
	require.Equal(t, "PLACE: London", results[1].NomiSpeciali[0])
	require.Equal(t, float64(entityExtractionNumPredict*2), request.Options["num_predict"])
	require.Contains(t, request.Prompt, "SEGMENT_INPUT_0")
	require.Contains(t, request.Prompt, "SEGMENT_INPUT_1")
}
