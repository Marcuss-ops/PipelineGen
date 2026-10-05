package nlp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func TestSpacyNERAdapterUsesSharedContractAndConvertsUnicodeOffsets(t *testing.T) {
	const source = "😀 Elon Musk discussed Tesla in Berlin."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ner/extract" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var req spacyNERRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Version != "ner.v1" || req.Operation != "extract" || req.Language != "en" || req.EntityCount != 4 {
			t.Errorf("request = %+v", req)
		}
		_, _ = w.Write([]byte(`{"version":"ner.v1","model":"xx_ent_wiki_sm","model_load_ms":12.5,"inference_ms":1.5,"sidecar_process_max_rss_mb":42.0,"entities":[{"text":"Elon Musk","label":"PER","start_char":2,"end_char":11},{"text":"Tesla","label":"ORG","start_char":22,"end_char":27},{"text":"Berlin","label":"LOC","start_char":31,"end_char":37}]}`))
	}))
	defer server.Close()

	adapter, err := NewSpacyNERAdapter(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	entities, err := adapter.Extract(context.Background(), "en", source, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []scriptgen.VisualEntity{
		{Text: "Elon Musk", Type: "PERSON", Start: strings.Index(source, "Elon Musk"), End: strings.Index(source, "Elon Musk") + len("Elon Musk"), Evidence: "Elon Musk"},
		{Text: "Tesla", Type: "ORG", Start: strings.Index(source, "Tesla"), End: strings.Index(source, "Tesla") + len("Tesla"), Evidence: "Tesla"},
		{Text: "Berlin", Type: "GPE", Start: strings.Index(source, "Berlin"), End: strings.Index(source, "Berlin") + len("Berlin"), Evidence: "Berlin"},
	}
	if timing := adapter.LastInferenceTiming(); timing.Model != "xx_ent_wiki_sm" || timing.ModelLoadMS != 12.5 || timing.InferenceMS != 1.5 || timing.SidecarMaxRSSMB != 42.0 {
		t.Fatalf("timing/model = %+v", timing)
	}
	if len(entities) != len(want) {
		t.Fatalf("got %#v", entities)
	}
	for i := range want {
		if entities[i] != want[i] {
			t.Errorf("entity[%d] = %#v, want %#v", i, entities[i], want[i])
		}
	}
}

func TestSpacyNERAdapterRejectsInvalidOrUnanchoredOffsets(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "out of range", body: `{"version":"ner.v1","entities":[{"text":"Tesla","label":"ORG","start_char":50,"end_char":55}]}`},
		{name: "wrong text", body: `{"version":"ner.v1","entities":[{"text":"Elon","label":"PER","start_char":0,"end_char":3}]}`},
		{name: "empty label", body: `{"version":"ner.v1","model":"xx_ent_wiki_sm","entities":[{"text":"Tesla","label":"","start_char":20,"end_char":25}]}`},
		{name: "unknown version", body: `{"version":"ner.v2","model":"xx_ent_wiki_sm","entities":[]}`},
		{name: "missing model identity", body: `{"version":"ner.v1","entities":[]}`},
		{name: "negative timing", body: `{"version":"ner.v1","model":"xx_ent_wiki_sm","inference_ms":-1,"entities":[]}`},
		{name: "nonfinite timing", body: `{"version":"ner.v1","model":"xx_ent_wiki_sm","inference_ms":1e999,"entities":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			adapter, err := NewSpacyNERAdapter(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Extract(context.Background(), "en", "Elon Musk discussed Tesla.", 3); err == nil {
				t.Fatal("expected fail-closed invalid entity error")
			}
		})
	}
}

func TestSpacyNERAdapterRejectsBadInputAndOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"ner.v1","model":"xx_ent_wiki_sm","entities":[]}`))
	}))
	defer server.Close()
	adapter, err := NewSpacyNERAdapter(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		language string
		text     string
		count    int
	}{
		{language: "en", text: "   ", count: 1},
		{language: "en", text: "Tesla", count: -1},
		{language: "en", text: "Tesla", count: 1001},
		{language: "en", text: strings.Repeat("a", 100_001), count: 1},
		{language: "en", text: string([]byte{0xff}), count: 1},
	} {
		if _, err := adapter.Extract(context.Background(), tc.language, tc.text, tc.count); err == nil {
			t.Errorf("Extract(%q, text bytes=%d, count=%d) succeeded", tc.language, len(tc.text), tc.count)
		}
	}
	overflowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"ner.v1","model":"xx_ent_wiki_sm","entities":[]}` + strings.Repeat(" ", maxNERResponseBytes)))
	}))
	defer overflowServer.Close()
	adapter, err = NewSpacyNERAdapter(overflowServer.URL, overflowServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Extract(context.Background(), "en", "Tesla", 1); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
	countServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req spacyNERRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.EntityCount != 0 {
			t.Errorf("requested entity count = %d, want zero to select shared default", req.EntityCount)
		}
		_, _ = w.Write([]byte(`{"version":"ner.v1","model":"xx_ent_wiki_sm","entities":[{"text":"Tesla","label":"ORG","start_char":0,"end_char":5}]}`))
	}))
	defer countServer.Close()
	adapter, err = NewSpacyNERAdapter(countServer.URL, countServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Extract(context.Background(), "en", "Tesla", 0); err != nil {
		t.Fatalf("zero entity count must use the shared top-three default: %v", err)
	}
	tooManyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"ner.v1","model":"xx_ent_wiki_sm","entities":[{"text":"Tesla","label":"ORG","start_char":0,"end_char":5},{"text":"Tesla","label":"ORG","start_char":0,"end_char":5},{"text":"Tesla","label":"ORG","start_char":0,"end_char":5},{"text":"Tesla","label":"ORG","start_char":0,"end_char":5}]}`))
	}))
	defer tooManyServer.Close()
	adapter, err = NewSpacyNERAdapter(tooManyServer.URL, tooManyServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Extract(context.Background(), "en", "Tesla", 0); err == nil || !strings.Contains(err.Error(), "requested entity count") {
		t.Fatalf("zero entity-count over-limit response = %v, want limit violation", err)
	}
}

func TestSpacyNERAdapterRejectsBadConfigLanguageAndHTTPStatus(t *testing.T) {
	for _, invalidURL := range []string{"file:///tmp/ner", "https://user:secret@example.test", "http://example.test?token=secret", "http://example.test/#fragment"} {
		if _, err := NewSpacyNERAdapter(invalidURL, nil); err == nil {
			t.Errorf("URL %q should be rejected", invalidURL)
		}
	}
	adapter, err := NewSpacyNERAdapter("http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Extract(context.Background(), " ", "Tesla", 1); err == nil {
		t.Fatal("expected missing-language error")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model is not installed", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	adapter, err = NewSpacyNERAdapter(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Extract(context.Background(), "en", "Tesla", 1); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("got %v, want HTTP 503", err)
	}
}
