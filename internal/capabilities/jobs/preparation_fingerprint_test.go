package jobs

import (
	"testing"
)

func TestBuildPreparationFingerprint_IsDeterministicAcrossMapAndDependencyOrder(t *testing.T) {
	first, err := BuildPreparationFingerprint(PreparationFingerprintInput{
		ContractVersion:  1,
		Kind:             " tts ",
		JobType:          " script.generate ",
		Payload:          []byte(`{"text":"hello"}`),
		Inputs:           map[string]string{"voice": "en", "rate": "1.0"},
		DependsOn:        []string{"scene-2", "scene-1"},
		ProcessorVersion: " tts-v1 ",
	})
	if err != nil {
		t.Fatalf("first fingerprint: %v", err)
	}
	second, err := BuildPreparationFingerprint(PreparationFingerprintInput{
		ContractVersion:  1,
		Kind:             "tts",
		JobType:          "script.generate",
		Payload:          []byte(`{"text":"hello"}`),
		Inputs:           map[string]string{"rate": "1.0", "voice": "en"},
		DependsOn:        []string{"scene-1", "scene-2"},
		ProcessorVersion: "tts-v1",
	})
	if err != nil {
		t.Fatalf("second fingerprint: %v", err)
	}
	if first != second {
		t.Fatalf("fingerprints differ: %q != %q", first, second)
	}
}

func TestBuildPreparationFingerprint_ChangesWhenIdentityChanges(t *testing.T) {
	base := PreparationFingerprintInput{Kind: "probe", JobType: "clip.render", ProcessorVersion: "probe-v1"}
	first, err := BuildPreparationFingerprint(base)
	if err != nil {
		t.Fatalf("base fingerprint: %v", err)
	}
	base.ProcessorVersion = "probe-v2"
	second, err := BuildPreparationFingerprint(base)
	if err != nil {
		t.Fatalf("changed fingerprint: %v", err)
	}
	if first == second {
		t.Fatal("processor version change did not change fingerprint")
	}
}

func TestNewPreparationUnitAndPlanValidate(t *testing.T) {
	unit, err := NewPreparationUnit("scene-1-tts", "tts", "script.generate", []byte(`{"text":"hello"}`), nil, nil, "tts-v1")
	if err != nil {
		t.Fatalf("NewPreparationUnit: %v", err)
	}
	if unit.Fingerprint == "" {
		t.Fatal("NewPreparationUnit returned empty fingerprint")
	}
	plan := PreparationPlan{JobID: "job-1", Units: []PreparationUnit{unit}}
	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestPreparationPlanValidateRejectsDuplicateIDs(t *testing.T) {
	unit := PreparationUnit{ID: "same", Kind: "probe", Fingerprint: "abc"}
	if err := (PreparationPlan{JobID: "job-1", Units: []PreparationUnit{unit, unit}}).Validate(); err == nil {
		t.Fatal("Validate accepted duplicate unit IDs")
	}
}
