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

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestHandlerGenerate_SmokeSceneImagesPayload(t *testing.T) {
	t.Parallel()

	jobsSvc, fake := newTestJobsService(t)
	handler := NewHandlerGenerate(
		jobsSvc,
		zap.NewNop(),
		nil,
		PreflightCaps{
			VoiceoverEnabled: true,
			ImagesEnabled:    true,
			DocumentEnabled:  true,
		},
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
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, fake.lastReq, "handler must enqueue a job")
	require.Equal(t, "script.generate", fake.lastReq.Type)

	var env scriptpkg.GenerationEnvelopeV2
	payload, ok := fake.lastReq.Payload.(json.RawMessage)
	require.True(t, ok, "payload handed to the job queue must be json.RawMessage")
	require.NoError(t, json.Unmarshal(payload, &env))
	require.Len(t, env.Items, 1)
	require.True(t, env.Items[0].Output.GenerateSceneImages.AsBool(),
		"payload handed to the job queue must preserve generate_scene_images=true")
}
