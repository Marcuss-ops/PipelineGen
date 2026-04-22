package script

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/service/scriptdocs"
)

func setupScriptDocsModesTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	apiGroup := router.Group("/api")
	scriptDocsGroup := apiGroup.Group("/script-docs")
	handler := NewScriptDocsHandler(nil)
	handler.RegisterRoutes(scriptDocsGroup)

	return router
}

func TestScriptDocsModesEndpoint(t *testing.T) {
	router := setupScriptDocsModesTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/script-docs/modes", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", resp["ok"])
	}

	modes, ok := resp["modes"].([]interface{})
	if !ok {
		t.Fatalf("expected modes array, got %#v", resp["modes"])
	}
	if len(modes) != 6 {
		t.Fatalf("expected 6 modes, got %d", len(modes))
	}

	foundJIT := false
	foundImagesOnly := false
	for _, raw := range modes {
		m, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected mode object, got %#v", raw)
		}
		switch m["mode"] {
		case scriptdocs.AssociationModeJITStock:
			foundJIT = true
			if m["allows_jit"] != true {
				t.Fatalf("expected jitstock to allow JIT, got %#v", m["allows_jit"])
			}
		case scriptdocs.AssociationModeImagesOnly:
			foundImagesOnly = true
			if m["label"] != "images only" {
				t.Fatalf("expected images only label, got %#v", m["label"])
			}
		}
	}

	if !foundJIT {
		t.Fatal("jitstock mode not returned by /modes")
	}
	if !foundImagesOnly {
		t.Fatal("images_only mode not returned by /modes")
	}
}

func TestScriptDocsRoutesRegistered(t *testing.T) {
	router := setupScriptDocsModesTestRouter()

	got := make(map[string]string)
	for _, r := range router.Routes() {
		got[r.Method+" "+r.Path] = r.Handler
	}

	expected := []string{
		"GET /api/script-docs/modes",
		"POST /api/script-docs/generate",
		"POST /api/script-docs/generate/stock",
		"POST /api/script-docs/generate/fullartlist",
		"POST /api/script-docs/generate/imagesfull",
		"POST /api/script-docs/generate/imagesonly",
		"POST /api/script-docs/generate/mixed",
		"POST /api/script-docs/generate/jitstock",
	}

	for _, route := range expected {
		if _, ok := got[route]; !ok {
			t.Fatalf("expected route %q to be registered", route)
		}
	}
}
