package mediamemory

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// TestToResolvePlanDTO_IncludesBrainDebugFields verifies that the
// brain's intent, trace and decision fingerprint are forwarded into
// the wire DTO.
func TestToResolvePlanDTO_IncludesBrainDebugFields(t *testing.T) {
	plan := mediamemory.SceneVisualPlan{
		ProjectID: "proj-1",
		SceneID:   "scene-1",
		Text:      "I Maya osservavano Venere",
		Language:  "it",
		Intent: mediamemory.SceneIntent{
			Entities: []string{"maya", "venere"},
			Concepts: []string{"astronomia maya"},
			Actions:  []string{"osservare"},
			Keywords: []string{"maya", "venere", "osservare"},
		},
		Trace: mediamemory.SceneResolutionTrace{
			NormalizedText: "i maya osservavano venere",
			BackendCalls: []mediamemory.SceneBackendCall{
				{Backend: "search", Hits: 10},
			},
			Reasons: []string{"ok"},
		},
		DecisionFingerprint: "abc123",
	}

	dto := toResolvePlanDTO(plan)

	if dto.SceneID != "scene-1" {
		t.Errorf("scene_id mismatch: got %q", dto.SceneID)
	}
	if len(dto.Intent.Entities) != 2 || dto.Intent.Entities[0] != "maya" {
		t.Errorf("intent.entities mismatch: got %v", dto.Intent.Entities)
	}
	if dto.Trace.NormalizedText != "i maya osservavano venere" {
		t.Errorf("trace.normalized_text mismatch: got %q", dto.Trace.NormalizedText)
	}
	if len(dto.Trace.BackendCalls) != 1 || dto.Trace.BackendCalls[0].Backend != "search" {
		t.Errorf("trace.backend_calls mismatch: got %v", dto.Trace.BackendCalls)
	}
	if dto.DecisionFingerprint != "abc123" {
		t.Errorf("decision_fingerprint mismatch: got %q", dto.DecisionFingerprint)
	}
}

// TestToResolvePlanDTO_EmptyBrainDebugFieldsOmitted verifies that
// empty intent/trace/fingerprint fields are omitted from the JSON
// response thanks to `omitempty`.
func TestToResolvePlanDTO_EmptyBrainDebugFieldsOmitted(t *testing.T) {
	plan := mediamemory.SceneVisualPlan{
		ProjectID: "proj-1",
		SceneID:   "scene-2",
		Text:      "hello",
		Language:  "it",
	}
	dto := toResolvePlanDTO(plan)
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal dto: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal dto: %v", err)
	}

	if _, ok := out["intent"]; ok {
		t.Errorf("intent should be omitted when empty")
	}
	if _, ok := out["trace"]; ok {
		t.Errorf("trace should be omitted when empty")
	}
	if _, ok := out["decision_fingerprint"]; ok {
		t.Errorf("decision_fingerprint should be omitted when empty")
	}
}
