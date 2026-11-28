package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditorialCompositionsParticipateInFingerprintButStayOffLegacyWire(t *testing.T) {
	first := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "summary-plan",
		VideoID:       "summary-video",
		EditorialCompositions: []json.RawMessage{
			json.RawMessage(`{"composition_id":"c1","summary":"Grounded phrase one"}`),
		},
	}
	firstFingerprint := first.FingerprintValue()
	second := first
	second.EditorialCompositions = []json.RawMessage{
		json.RawMessage(`{"composition_id":"c1","summary":"Changed source phrase"}`),
	}
	if second.FingerprintValue() != firstFingerprint {
		t.Fatal("disabled planner sidecars must not change the render fingerprint")
	}
	first.EditorialCompositionsEnabled = true
	second.EditorialCompositionsEnabled = true
	if second.FingerprintValue() == first.FingerprintValue() {
		t.Fatal("enabled composition sidecars must change the render fingerprint")
	}
	wire, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "editorial_compositions") {
		t.Fatalf("legacy OverlayPlan wire leaked composition sidecar: %s", wire)
	}
}
