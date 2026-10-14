package scriptgeneration

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The catalog kill-switch must travel envelope → request → canvas so
// motion-catalog runs keep operator-supplied grounded phrases in the run
// budget instead of being crowded out by auto-projected titles/bullets.
// Default (absent key) keeps the established editorial behaviour.
func TestBuildGenerateRequestCarriesDisableEditorialOverlays(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"catalog","language":"it","source":{"type":"text","topic":"topic"},"disable_editorial_overlays":true}]}`), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Items[0].DisableEditorialOverlays {
		t.Fatal("envelope did not decode disable_editorial_overlays")
	}
	request, err := BuildGenerateRequest(&env, "editorial-off-key")
	if err != nil {
		t.Fatal(err)
	}
	if !request.DisableEditorialOverlays {
		t.Fatal("request lost disable_editorial_overlays")
	}
	canvas := OverlayCanvasSpec{}
	canvas.DisableEditorialOverlays = request.DisableEditorialOverlays
	if !canvas.DisableEditorialOverlays {
		t.Fatal("canvas lost disable_editorial_overlays")
	}

	var plain scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"normal","language":"it","source":{"type":"text","topic":"topic"}}]}`), &plain); err != nil {
		t.Fatal(err)
	}
	plainRequest, err := BuildGenerateRequest(&plain, "editorial-default-key")
	if err != nil {
		t.Fatal(err)
	}
	if plainRequest.DisableEditorialOverlays {
		t.Fatal("editorial projection must stay enabled by default")
	}
}
