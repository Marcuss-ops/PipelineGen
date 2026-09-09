package semantic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	transcripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/transcripts"
	transcript "github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

type trumpSubtitleSource struct{}

func (trumpSubtitleSource) Fetch(context.Context, string) (transcript.Document, error) {
	entries := make([]transcript.Entry, 0, 6)
	for i := 0; i < 6; i++ {
		entries = append(entries, transcript.Entry{
			Start: float64(i * 10), End: float64((i + 1) * 10),
			Text: "Donald Trump discusses the presidency, policy, and public life.",
		})
	}
	return transcript.Document{VideoID: "trump-test", Text: "Donald Trump discusses politics.", Entries: entries}, nil
}

var _ transcripts.SubtitleSource = trumpSubtitleSource{}

func TestFindSegments_AllowsTrumpPoliticalContent(t *testing.T) {
	var prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode Ollama request: %v", err)
			return
		}
		prompt = body.Prompt
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"[{\"start\":\"00:00:10\",\"end\":\"00:00:30\",\"name\":\"Trump policy discussion\",\"summary\":\"A substantive discussion of policy\"}]"}`))
	}))
	defer server.Close()

	analyzer := NewOllamaAnalyzer(Deps{
		OllamaClient: ollamaclient.NewClient(server.URL, "test-model", 5),
		Subtitles:    trumpSubtitleSource{},
	})
	segments, err := analyzer.FindSegments(context.Background(), "https://youtube.test/trump", "Donald Trump discusses politics.", "", 1)
	if err != nil {
		t.Fatalf("FindSegments: %v", err)
	}
	if len(segments) != 1 || segments[0].Name != "Trump policy discussion" {
		t.Fatalf("segments = %+v, want one substantive Trump segment", segments)
	}
	if strings.Contains(strings.ToLower(prompt), "never select any segments that mention") ||
		strings.Contains(strings.ToLower(prompt), "skip those segments entirely") {
		t.Fatalf("Trump exclusion rule still present in prompt: %q", prompt)
	}
}
