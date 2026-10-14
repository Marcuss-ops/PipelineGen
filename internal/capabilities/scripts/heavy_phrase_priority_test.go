package scriptgeneration

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// heavy_phrase_priority is an operator override for the prominent-motion
// lane threshold. Catalog runs pin it unreachable; ordinary runs carry zero
// so the runner still fills the certified default.
func TestBuildGenerateRequestCarriesHeavyPhrasePriority(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"catalog","language":"it","source":{"type":"text","topic":"topic"},"heavy_phrase_priority":9999}]}`), &env); err != nil {
		t.Fatal(err)
	}
	request, err := BuildGenerateRequest(&env, "heavy-priority-key")
	if err != nil {
		t.Fatal(err)
	}
	if request.HeavyPhrasePriority != 9999 {
		t.Fatalf("heavy threshold = %v, want the operator override", request.HeavyPhrasePriority)
	}

	var plain scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"normal","language":"it","source":{"type":"text","topic":"topic"}}]}`), &plain); err != nil {
		t.Fatal(err)
	}
	plainRequest, err := BuildGenerateRequest(&plain, "heavy-default-key")
	if err != nil {
		t.Fatal(err)
	}
	if plainRequest.HeavyPhrasePriority != 0 {
		t.Fatalf("heavy threshold = %v, want zero so the certified default applies", plainRequest.HeavyPhrasePriority)
	}
}
