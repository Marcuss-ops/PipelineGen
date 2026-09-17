package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	"github.com/stretchr/testify/require"
)

func TestExtractEntitiesFromSegment_UsesBoundedOperationBudget(t *testing.T) {
	var request struct {
		Model   string         `json:"model"`
		Think   *bool          `json:"think"`
		Options map[string]any `json:"options"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"## frasi_importanti\n- test phrase\n## entity_senza_testo\n## nomi_speciali\n- PERSON: Ada Lovelace\n## parole_importanti\n- test\n## artlist_phrases\n- testing"}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	result, err := c.ExtractEntitiesFromSegmentWithModel(context.Background(), detail.EntityExtractionRequest{
		SegmentText:  "A test segment.",
		SegmentIndex: 3,
		EntityCount:  5,
	}, "gemma4:e4b")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gemma4:e4b", request.Model)
	require.NotNil(t, request.Think)
	require.False(t, *request.Think)
	require.Equal(t, float64(entityExtractionNumPredict), request.Options["num_predict"])
	// Entity/phrase extraction is the workload that used to omit num_ctx and
	// therefore tore down the warmed 8192 runner on every call.
	require.Equal(t, float64(types.ProductionRunnerContext), request.Options["num_ctx"])
}

func TestParseEntityExtractionResult_AcceptsGroupedSpecialNames(t *testing.T) {
	result, err := parseEntityExtractionResult(`{"frasi_importanti":["first computer program"],"entity_senza_testo":{},"nomi_speciali":{"PERSON":["Ada Lovelace"]},"parole_importanti":["computing"],"artlist_phrases":["analytical engine"],"noun_chunks":["analytical computing"]}`, 0)

	require.NoError(t, err)
	require.Equal(t, []string{"first computer program"}, result.FrasiImportanti)
	require.Equal(t, []string{"PERSON: Ada Lovelace"}, result.NomiSpeciali)
}

func TestParseEntityExtractionResultStripsListMarkerFromSpecialNames(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "- PLACE: Лас-Вегасе", want: "PLACE: Лас-Вегасе"},
		{input: "• PERSON: Майка Тайсона", want: "PERSON: Майка Тайсона"},
	} {
		result, err := parseEntityExtractionResult(`{"frasi_importanti":[],"entity_senza_testo":{},"nomi_speciali":["`+tc.input+`"],"parole_importanti":[],"artlist_phrases":[],"noun_chunks":[]}`, 0)
		require.NoError(t, err)
		require.Equal(t, []string{tc.want}, result.NomiSpeciali)
	}
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
	results, err := c.ExtractEntitiesFromBatchWithModel(context.Background(), []string{"scene zero", "scene one"}, 5, "gemma3:1b", "")

	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "Ada Lovelace", strings.TrimPrefix(results[0].NomiSpeciali[0], "PERSON: "))
	require.Equal(t, "PLACE: London", results[1].NomiSpeciali[0])
	require.Equal(t, float64(entityExtractionNumPredict*2), request.Options["num_predict"])
	require.Equal(t, float64(types.ProductionRunnerContext), request.Options["num_ctx"])
	require.Contains(t, request.Prompt, "SEGMENT_INPUT_0")
	require.Contains(t, request.Prompt, "SEGMENT_INPUT_1")
	require.NotContains(t, request.Prompt, "Subject: precise visual search description")
	require.NotContains(t, request.Prompt, "concrete keyword")
}

func TestExtractEntitiesFromBatch_FallbackPreservesLanguage(t *testing.T) {
	var requests atomic.Int64
	var fallbackPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Prompt string `json:"prompt"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			// Force ExtractEntitiesFromBatchWithModel through its documented
			// per-segment fallback path.
			_, _ = w.Write([]byte(`{"response":"malformed batch response"}`))
			return
		}
		fallbackPrompt = request.Prompt
		_, _ = w.Write([]byte(`{"response":"## frasi_importanti\n## entity_senza_testo\n## nomi_speciali\n- PLACE: Лас-Вегас\n## parole_importanti\n## artlist_phrases\n## noun_chunks\n- Лас-Вегас"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "gemma4:e4b", 5)
	results, err := client.ExtractEntitiesFromBatchWithModel(
		context.Background(), []string{"Лас-Вегас встретил Тайсона."}, 5, "gemma4:e4b", "ru",
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, int64(2), requests.Load())
	require.Contains(t, fallbackPrompt, "SOURCE_LANGUAGE: ru")
	require.Contains(t, results[0].NomiSpeciali, "PLACE: Лас-Вегас")
}
