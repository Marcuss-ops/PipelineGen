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

// The gate that used to stop at the top-level struct now reaches inside the
// custom UnmarshalJSON blocks (output, audio, media_plan). A key that only
// exists under output.render must be a 400, never a silently dropped field.
func TestGenerate_RejectsUnknownFieldInsideOutputBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100},"output":{"drive_subfolder_name":"job-r25"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/script/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "unknown-output-key")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), `unknown field \"drive_subfolder_name\"`)
	require.Contains(t, w.Body.String(), "items[].output.render.drive_subfolder_name")
	require.Equal(t, 0, submit.submitCount)
}

// Version 3 is the canonical wire: the duplicate owners that version 2 still
// reconciles (and that payloadlint only warns about) are rejected at ingress.
// The SAME body keeps working as legacy V2, so the recorded corpus replays.
func TestGenerate_RejectsV3DuplicateOwnerAndAcceptsV2(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	item := `{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100},"output":{"audio":{"mode":"COMBINED_TIMELINE"}}}`

	v3 := httptest.NewRequest(http.MethodPost, "/api/script/generate", strings.NewReader(`{"version":3,"preset":"custom","items":[`+item+`]}`))
	v3.Header.Set("Content-Type", "application/json")
	v3.Header.Set("Idempotency-Key", "v3-duplicate-owner")
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, v3)

	require.Equal(t, http.StatusBadRequest, w3.Code)
	require.Contains(t, w3.Body.String(), "items[0].output.audio")
	require.Equal(t, 0, submit.submitCount)

	v2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", strings.NewReader(`{"version":2,"preset":"custom","items":[`+item+`]}`))
	v2.Header.Set("Content-Type", "application/json")
	v2.Header.Set("Idempotency-Key", "v2-duplicate-owner")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, v2)

	require.Equal(t, http.StatusAccepted, w2.Code, "legacy V2 keeps the duplicate spelling readable")
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
