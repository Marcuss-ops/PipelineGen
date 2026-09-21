package script

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFriendlyGenerateJSONErrorSuggestsCanonicalRenderPath(t *testing.T) {
	err := friendlyGenerateJSONError(errors.New(`json: unknown field "render"`))
	if !strings.Contains(err, "items[].output.render") {
		t.Fatalf("error = %q, want canonical output.render suggestion", err)
	}
}

func TestGenerate_RejectsRemovedAssembleFinalField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100},"output":{"render":{"enabled":true,"assemble_final":true}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/script/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "removed-assemble-final")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "assemble_final")
	require.Equal(t, 0, submit.submitCount)
}
