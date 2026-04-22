package script

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/scriptdocs"
)

type staticImageFinder struct {
	url string
}

func (f staticImageFinder) Find(string) string {
	return f.url
}

func newTestScriptDocsService(t *testing.T) *scriptdocs.ScriptDocService {
	t.Helper()

	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		var payload struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode ollama request: %v", err)
		}

		response := "Andrew Tate appears in London. He speaks about business and training. The crowd reacts."
		if strings.Contains(payload.Prompt, "Return ONLY valid JSON") {
			response = `{"topic":"Andrew Tate","language":"english","chapters":[{"title":"Intro","start_sentence":0,"end_sentence":1,"dominant_entities":["Andrew Tate"],"summary":"Opening","confidence":0.9},{"title":"Body","start_sentence":2,"end_sentence":3,"dominant_entities":["London"],"summary":"Middle","confidence":0.8}]}`
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"response": response,
			"done":     true,
		})
	}))
	t.Cleanup(ollamaServer.Close)

	client := ollama.NewClient(ollamaServer.URL, "test-model")
	gen := ollama.NewGenerator(client)

	svc := scriptdocs.NewScriptDocService(
		gen,
		nil,
		nil,
		nil,
		map[string]scriptdocs.StockFolder{},
		nil,
		nil,
		nil,
	)
	svc.SetImageFinder(staticImageFinder{url: "https://example.com/test-image.jpg"})
	return svc
}

func setupScriptDocsEndpointsRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	apiGroup := router.Group("/api")
	scriptDocsGroup := apiGroup.Group("/script-docs")
	handler := NewScriptDocsHandler(newTestScriptDocsService(t))
	handler.RegisterRoutes(scriptDocsGroup)
	return router
}

func TestScriptDocsGenerateEndpoints(t *testing.T) {
	router := setupScriptDocsEndpointsRouter(t)

	tests := []struct {
		name            string
		path            string
		expectedMode    string
		expectImagePlan bool
	}{
		{name: "default", path: "/api/script-docs/generate", expectedMode: scriptdocs.AssociationModeDefault},
		{name: "stock", path: "/api/script-docs/generate/stock", expectedMode: scriptdocs.AssociationModeDefault},
		{name: "fullartlist", path: "/api/script-docs/generate/fullartlist", expectedMode: scriptdocs.AssociationModeFullArtlist},
		{name: "imagesfull", path: "/api/script-docs/generate/imagesfull", expectedMode: scriptdocs.AssociationModeImagesFull, expectImagePlan: true},
		{name: "imagesonly", path: "/api/script-docs/generate/imagesonly", expectedMode: scriptdocs.AssociationModeImagesOnly, expectImagePlan: true},
		{name: "mixed", path: "/api/script-docs/generate/mixed", expectedMode: scriptdocs.AssociationModeMixed},
		{name: "jitstock", path: "/api/script-docs/generate/jitstock", expectedMode: scriptdocs.AssociationModeJITStock},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"topic":"Andrew Tate","duration":45}`
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d. body=%s", w.Code, w.Body.String())
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if resp["ok"] != true {
				t.Fatalf("expected ok=true, got %#v", resp["ok"])
			}
			if resp["mode"] != tt.expectedMode {
				t.Fatalf("expected mode %q, got %#v", tt.expectedMode, resp["mode"])
			}
			if resp["doc_id"] == "" {
				t.Fatalf("expected non-empty doc_id, body=%s", w.Body.String())
			}
			if resp["doc_url"] == "" {
				t.Fatalf("expected non-empty doc_url, body=%s", w.Body.String())
			}

			languages, ok := resp["languages"].([]interface{})
			if !ok || len(languages) == 0 {
				t.Fatalf("expected languages array, got %#v", resp["languages"])
			}

			if tt.expectImagePlan {
				if resp["image_plan"] == nil {
					t.Fatalf("expected image_plan for mode %s", tt.expectedMode)
				}
				if resp["image_plan_path"] == "" {
					t.Fatalf("expected image_plan_path for mode %s", tt.expectedMode)
				}
			}
		})
	}
}
