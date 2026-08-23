package overlays

import (
	"testing"
)

func validPrepareRequest() PrepareRequest {
	return PrepareRequest{
		SchemaVersion: SchemaVersionPrepare,
		PlanID:        "run-001",
		VideoID:       "run-001",
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		Intents: []OverlayIntent{{
			Version:     OverlayIntentVersion,
			IntentID:    "intent-scene-0-tom-hanks",
			SceneID:     "scene-0",
			SceneIndex:  0,
			Entity:      EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
			Source:      IntentSourceEntity,
			Kind:        string(KindEntityCard),
			TemplateID:  "person_default",
			Payload:     IntentPayload{Name: "Tom Hanks"},
			TimingState: TimingStatePending,
		}},
	}
}

func TestPrepareRequest_ValidatePass(t *testing.T) {
	req := validPrepareRequest()
	if err := req.Validate(); err != nil {
		t.Fatalf("valid prepare request rejected: %v", err)
	}
}

func TestPrepareRequest_ValidateRejectsWrongSchema(t *testing.T) {
	req := validPrepareRequest()
	req.SchemaVersion = SchemaVersionPlan
	if err := req.Validate(); err == nil {
		t.Fatal("wrong schema version must fail validation")
	}
}

func TestPrepareRequest_ValidateRejectsEmptyIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*PrepareRequest){
		"empty plan_id":  func(r *PrepareRequest) { r.PlanID = "" },
		"empty video_id": func(r *PrepareRequest) { r.VideoID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			req := validPrepareRequest()
			mutate(&req)
			if err := req.Validate(); err == nil {
				t.Fatal("empty identity must fail validation")
			}
		})
	}
}

func TestPrepareRequest_ValidateRejectsZeroCanvas(t *testing.T) {
	req := validPrepareRequest()
	req.Width = 0
	if err := req.Validate(); err == nil {
		t.Fatal("zero canvas must fail validation")
	}
}

func TestPrepareRequest_ValidateRejectsNoIntents(t *testing.T) {
	req := validPrepareRequest()
	req.Intents = nil
	if err := req.Validate(); err == nil {
		t.Fatal("no intents must fail validation (prepare is never an empty no-op)")
	}
}

func TestPrepareRequest_ValidateRejectsFrozenIntent(t *testing.T) {
	req := validPrepareRequest()
	req.Intents[0].TimingState = TimingStateFrozen
	if err := req.Validate(); err == nil {
		t.Fatal("a FROZEN intent must never be re-prepared")
	}
}

func TestPrepareRequest_ValidateRejectsInvalidIntent(t *testing.T) {
	req := validPrepareRequest()
	req.Intents[0].TemplateID = ""
	if err := req.Validate(); err == nil {
		t.Fatal("an invalid intent must fail validation")
	}
}
