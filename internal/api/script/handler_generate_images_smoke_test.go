// Package script — handler_generate_images_smoke_test.go pins the
// HTTP-level smoke contract for POST /api/script/generate with scene
// images requested in the payload.
//
// The test intentionally stays at the router seam: it verifies the
// endpoint accepts the request, enqueues script.generate, and preserves
// the generate_scene_images flag in the marshaled job payload.
package script

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestHandlerGenerate_SmokeSceneImagesPayload(t *testing.T) {
	t.Parallel()

	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewHandlerGenerate(
		submit,
		zap.NewNop(),
		usecase.NewDefaultPayloadValidator(),
	)

	router := gin.New()
	rg := router.Group("/api/script")
	handler.GenerateRoute(rg)

	raw := []byte(`{
		"version": 2,
		"preset": "custom",
		"items": [
			{
				"id": "smoke-scene-images",
				"title": "Smoke Scene Images",
				"language": "en",
				"script_params": {
					"target_words": 150
				},
				"source": {
					"type": "text",
					"topic": "smoke scene images",
					"source_text": "smoke fixture"
				},
				"output": {
					"generate_scene_images": true
				}
			}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-smoke-scene-images")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.NotNil(t, submit.lastReq, "handler must submit a job")
	require.Equal(t, "script.generate", submit.lastReq.JobType)

	var env scriptpkg.GenerationEnvelopeV2
	payload := submit.lastReq.JobPayload
	require.NoError(t, json.Unmarshal(payload, &env))
	require.Len(t, env.Items, 1)
	require.True(t, env.Items[0].Output.SaveToDB,
		"payload handed to the job queue must preserve generate_scene_images=true")
	_ = deps
}
