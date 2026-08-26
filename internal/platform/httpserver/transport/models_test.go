package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
	"github.com/gin-gonic/gin"
)

func TestModelsHandlerUsesCanonicalRegistryIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model := models.E5
		if r.URL.Path == "/embed_visual" {
			model = models.SigLIP
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding":     make([]float64, model.Dimensions),
			"dimensions":    model.Dimensions,
			"model":         model.ID,
			"model_version": model.Revision,
		})
	}))
	defer sidecar.Close()

	router := gin.New()
	router.GET("/models", NewModelsHandler(sidecar.URL).Models)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/models", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var payload modelsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK || len(payload.Models) != 2 {
		t.Fatalf("payload = %+v, want two healthy canonical models", payload)
	}
	if payload.Models[0].Model != models.E5.ID || payload.Models[0].Dimensions != models.E5.Dimensions {
		t.Fatalf("E5 diagnostic = %+v, want registry identity", payload.Models[0])
	}
	if payload.Models[1].Model != models.SigLIP.ID || payload.Models[1].Dimensions != models.SigLIP.Dimensions {
		t.Fatalf("SigLIP diagnostic = %+v, want registry identity", payload.Models[1])
	}
}

func TestModelsHandlerRejectsCanonicalModelDrift(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model := models.E5
		if r.URL.Path == "/embed_visual" {
			model = models.SigLIP
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding":     make([]float64, model.Dimensions),
			"dimensions":    model.Dimensions,
			"model":         "different/model",
			"model_version": model.Revision,
		})
	}))
	defer sidecar.Close()

	router := gin.New()
	router.GET("/models", NewModelsHandler(sidecar.URL).Models)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/models", nil))

	var payload modelsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.OK {
		t.Fatal("diagnostics must fail when sidecar model identity drifts")
	}
	for _, result := range payload.Models {
		if result.OK {
			t.Fatalf("drifted model diagnostic unexpectedly healthy: %+v", result)
		}
	}
}
