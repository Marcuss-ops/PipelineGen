package script

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/entityimages"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
)

type queryImageFinder struct{}

func (queryImageFinder) Find(query string) string {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.ReplaceAll(trimmed, " ", "_")
	trimmed = strings.ReplaceAll(trimmed, "/", "_")
	if trimmed == "" {
		trimmed = "image"
	}
	return "https://example.com/" + strings.ToLower(trimmed) + ".jpg"
}

func loadGervontaFixture(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "testdata", "scriptdocs", "gervonta_davis_from_nothing.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	return string(data)
}

func findWorkspaceFile(t *testing.T, name string) string {
	t.Helper()

	paths := []string{
		name,
		filepath.Join("..", name),
		filepath.Join("..", "..", name),
		filepath.Join("..", "..", "..", name),
		filepath.Join("..", "..", "..", "..", name),
		filepath.Join("..", "..", "..", "..", "..", name),
		filepath.Join("data", name),
		filepath.Join("..", "data", name),
		filepath.Join("..", "..", "data", name),
		filepath.Join("..", "..", "..", "data", name),
		filepath.Join("..", "..", "..", "..", "data", name),
		filepath.Join("..", "..", "..", "..", "..", "data", name),
		filepath.Join("src", "go-master", "data", name),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatalf("could not find %s in workspace", name)
	return ""
}

func buildFixtureChapterJSON(t *testing.T, topic, language, text string) string {
	t.Helper()

	sentences := scriptdocs.ExtractSentences(text)
	if len(sentences) == 0 {
		t.Fatal("fixture produced no sentences")
	}

	chapterCount := 4
	if len(sentences) < chapterCount {
		chapterCount = len(sentences)
	}
	if chapterCount == 0 {
		chapterCount = 1
	}

	step := (len(sentences) + chapterCount - 1) / chapterCount
	chapters := make([]map[string]interface{}, 0, chapterCount)
	for start := 0; start < len(sentences); start += step {
		end := start + step - 1
		if end >= len(sentences) {
			end = len(sentences) - 1
		}
		chapters = append(chapters, map[string]interface{}{
			"title":             fmt.Sprintf("Chapter %d", len(chapters)+1),
			"start_sentence":    start,
			"end_sentence":      end,
			"dominant_entities": []string{"Gervonta Davis", "Baltimore"},
			"summary":           sentences[start],
			"confidence":        0.88,
		})
	}

	payload := map[string]interface{}{
		"topic":    topic,
		"language": language,
		"chapters": chapters,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal chapter JSON: %v", err)
	}
	return string(raw)
}

func newGervontaFixtureScriptDocsService(t *testing.T) *scriptdocs.ScriptDocService {
	t.Helper()

	fixture := loadGervontaFixture(t)
	chapterJSON := buildFixtureChapterJSON(t, "Gervonta Davis", "english", fixture)
	scriptdocs.SetGlobalImageFinderForTests(queryImageFinder{})
	t.Cleanup(func() {
		scriptdocs.SetGlobalImageFinderForTests(entityimages.New())
	})

	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()

		var payload struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode ollama payload: %v", err)
		}

		response := fixture
		if strings.Contains(payload.Prompt, "Return ONLY valid JSON") || strings.Contains(payload.Prompt, "semantic chapter planner") {
			response = chapterJSON
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"response": response,
			"done":     true,
		})
	}))
	t.Cleanup(ollamaServer.Close)

	client := ollama.NewClient(ollamaServer.URL, "test-model")
	gen := ollama.NewGenerator(client)

	stockPath := findWorkspaceFile(t, "stock.db.sqlite")
	db, err := stockdb.Open(stockPath)
	if err != nil {
		t.Fatalf("failed to open stock db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	artlistPath := findWorkspaceFile(t, "artlist_stock_index.json")
	artlistIndex, err := scriptdocs.LoadArtlistIndex(artlistPath)
	if err != nil {
		t.Fatalf("failed to load artlist index: %v", err)
	}

	svc := scriptdocs.NewScriptDocService(
		gen,
		nil,
		artlistIndex,
		db,
		nil,
		nil,
		nil,
		nil,
	)
	svc.SetImageFinder(queryImageFinder{})
	return svc
}

func newGervontaFixtureScriptDocsServiceWithDocClient(t *testing.T) *scriptdocs.ScriptDocService {
	t.Helper()

	credsPath := findWorkspaceFile(t, "credentials.json")
	tokenPath := findWorkspaceFile(t, "token.json")
	docClient, err := drive.NewDocClient(
		context.Background(),
		nil,
		credsPath,
		tokenPath,
	)
	if err != nil {
		t.Fatalf("failed to create docs client: %v", err)
	}

	fixture := loadGervontaFixture(t)
	chapterJSON := buildFixtureChapterJSON(t, "Gervonta Davis", "english", fixture)
	scriptdocs.SetGlobalImageFinderForTests(queryImageFinder{})
	t.Cleanup(func() {
		scriptdocs.SetGlobalImageFinderForTests(entityimages.New())
	})

	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()

		var payload struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode ollama payload: %v", err)
		}

		response := fixture
		if strings.Contains(payload.Prompt, "Return ONLY valid JSON") || strings.Contains(payload.Prompt, "semantic chapter planner") {
			response = chapterJSON
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"response": response,
			"done":     true,
		})
	}))
	t.Cleanup(ollamaServer.Close)

	client := ollama.NewClient(ollamaServer.URL, "test-model")
	gen := ollama.NewGenerator(client)

	stockPath := findWorkspaceFile(t, "stock.db.sqlite")
	db, err := stockdb.Open(stockPath)
	if err != nil {
		t.Fatalf("failed to open stock db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	artlistPath := findWorkspaceFile(t, "artlist_stock_index.json")
	artlistIndex, err := scriptdocs.LoadArtlistIndex(artlistPath)
	if err != nil {
		t.Fatalf("failed to load artlist index: %v", err)
	}

	svc := scriptdocs.NewScriptDocService(
		gen,
		docClient,
		artlistIndex,
		db,
		nil,
		nil,
		nil,
		nil,
	)
	svc.SetImageFinder(queryImageFinder{})
	return svc
}

func setupGervontaFixtureRouter(t *testing.T) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	apiGroup := router.Group("/api")
	scriptDocsGroup := apiGroup.Group("/script-docs")
	handler := NewScriptDocsHandler(newGervontaFixtureScriptDocsService(t))
	handler.RegisterRoutes(scriptDocsGroup)
	return router
}

func TestScriptDocsGervontaFixtureEndpoints(t *testing.T) {
	router := setupGervontaFixtureRouter(t)

	tests := []struct {
		name         string
		path         string
		expectedMode string
		imageMode    bool
	}{
		{name: "default", path: "/api/script-docs/generate", expectedMode: scriptdocs.AssociationModeDefault},
		{name: "stock", path: "/api/script-docs/generate/stock", expectedMode: scriptdocs.AssociationModeDefault},
		{name: "fullartlist", path: "/api/script-docs/generate/fullartlist", expectedMode: scriptdocs.AssociationModeFullArtlist},
		{name: "imagesfull", path: "/api/script-docs/generate/imagesfull", expectedMode: scriptdocs.AssociationModeImagesFull, imageMode: true},
		{name: "imagesonly", path: "/api/script-docs/generate/imagesonly", expectedMode: scriptdocs.AssociationModeImagesOnly, imageMode: true},
		{name: "mixed", path: "/api/script-docs/generate/mixed", expectedMode: scriptdocs.AssociationModeMixed},
		{name: "jitstock", path: "/api/script-docs/generate/jitstock", expectedMode: scriptdocs.AssociationModeJITStock},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"topic":"Gervonta Davis","duration":210,"languages":["en"],"template":"biography","preview_only":true}`
			if tt.expectedMode != scriptdocs.AssociationModeDefault {
				body = strings.Replace(body, `"preview_only":true`, `"preview_only":true,"association_mode":"`+tt.expectedMode+`"`, 1)
			}

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

			previewPath, _ := resp["preview_path"].(string)
			if previewPath == "" {
				t.Fatalf("expected preview_path, got %#v", resp["preview_path"])
			}
			if u, err := url.Parse(previewPath); err == nil && u.Scheme == "file" {
				previewPath = u.Path
			}
			content, err := os.ReadFile(previewPath)
			if err != nil {
				t.Fatalf("failed to read preview file %s: %v", previewPath, err)
			}
			text := string(content)
			if !strings.Contains(text, "From Nothing") || !strings.Contains(text, "Gervonta Bryant Davis") {
				t.Fatalf("preview did not use fixture text; path=%s", previewPath)
			}

			languages, ok := resp["languages"].([]interface{})
			if !ok || len(languages) == 0 {
				t.Fatalf("expected languages array, got %#v", resp["languages"])
			}

			l0, ok := languages[0].(map[string]interface{})
			if !ok {
				t.Fatalf("expected language object, got %#v", languages[0])
			}
			t.Logf("%s => frasi=%v nomi=%v parole=%v associations=%v artlist=%v images=%v avg=%.2f",
				tt.name,
				l0["frasi_importanti"],
				l0["nomi_speciali"],
				l0["parole_importanti"],
				l0["associations"],
				l0["artlist_matches"],
				l0["image_associations"],
				l0["avg_confidence"],
			)

			if tt.imageMode {
				if resp["image_plan"] == nil {
					t.Fatalf("expected image_plan for mode %s", tt.expectedMode)
				}
			}
		})
	}
}

func TestScriptDocsGervontaFixtureCreatesRealDoc(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	apiGroup := router.Group("/api")
	scriptDocsGroup := apiGroup.Group("/script-docs")
	handler := NewScriptDocsHandler(newGervontaFixtureScriptDocsServiceWithDocClient(t))
	handler.RegisterRoutes(scriptDocsGroup)

	body := `{"topic":"Gervonta Davis","duration":210,"languages":["en"],"template":"biography","preview_only":false,"association_mode":"images_full"}`
	req := httptest.NewRequest(http.MethodPost, "/api/script-docs/generate/imagesfull", strings.NewReader(body))
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
	if resp["doc_id"] == "" || resp["doc_id"] == "local_file" {
		t.Fatalf("expected real google doc id, got %#v", resp["doc_id"])
	}
	if resp["doc_url"] == "" {
		t.Fatalf("expected doc_url, got %#v", resp["doc_url"])
	}
	t.Logf("created doc_id=%v doc_url=%v", resp["doc_id"], resp["doc_url"])
}
