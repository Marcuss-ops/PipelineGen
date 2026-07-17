package script

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGenerateShorts_ReturnsRemotionPayloadAndOptionalSFX(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandlerGenerate(nil, nil, nil)
	r.POST("/shorts/generate", h.GenerateShorts)
	req := httptest.NewRequest("POST", "/shorts/generate", strings.NewReader(`{
      "id":"short-1","text":"one two three four five","duration_ms":5000,
      "clips":[{"id":"clip-ai-1"}],"include_sound_effects":true,
      "sound_effects":[{"id":"sfx-1","file":"/assets/sfx/hit.wav","at_ms":1000}]
    }`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	t.Logf("generated shorts payload: %s", rec.Body.String())
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"schema_version":"remotion.shorts.v1"`) || !strings.Contains(rec.Body.String(), `"sfx-1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
