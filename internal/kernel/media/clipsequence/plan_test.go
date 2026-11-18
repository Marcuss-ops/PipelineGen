package clipsequence

import (
	"strings"
	"testing"
)

func clip(id string, duration int64, transition *Transition) Clip {
	return Clip{AssetID: id, Path: "/tmp/" + id + ".mp4", DurationMS: duration, TransitionIn: transition}
}

// TestPresetsAreAllFastAndCertified pins the catalogue: ten fast presets, each
// with a Chronon kind, a default inside the fast window, and no duplicate name.
func TestPresetsAreAllFastAndCertified(t *testing.T) {
	presets := Presets()
	if len(presets) != 10 {
		t.Fatalf("preset count = %d, want 10", len(presets))
	}
	seen := map[Preset]struct{}{}
	for _, spec := range presets {
		if _, duplicate := seen[spec.Preset]; duplicate {
			t.Fatalf("duplicate preset %q", spec.Preset)
		}
		seen[spec.Preset] = struct{}{}
		if spec.ChrononKind == "" {
			t.Fatalf("preset %q has no renderer kind", spec.Preset)
		}
		if spec.DefaultDurationMS < MinTransitionMS || spec.DefaultDurationMS > MaxFastTransitionMS {
			t.Fatalf("preset %q default %d ms is outside the fast window", spec.Preset, spec.DefaultDurationMS)
		}
		if spec.Description == "" {
			t.Fatalf("preset %q has no description", spec.Preset)
		}
	}
	// The three presets the user asked for by name must be present and must
	// map to the renderer kinds that own those looks.
	for preset, wantKind := range map[Preset]string{
		PresetFlashWhite:    "Flash",
		PresetLightLeakSoft: "LightLeak",
		PresetLightLeakHard: "LightLeak",
		PresetShakeLight:    "Shake",
		PresetShakeStrong:   "Shake",
	} {
		spec, ok := LookupPreset(preset)
		if !ok {
			t.Fatalf("missing preset %q", preset)
		}
		if spec.ChrononKind != wantKind {
			t.Fatalf("preset %q kind = %q, want %q", preset, spec.ChrononKind, wantKind)
		}
	}
}

// TestPlanMergesOnlyTransitionLinkedClips is the rule the producer asked for:
// A, B(with transition), C(no transition), D(with transition) becomes two
// artifacts — [A,B] and [C,D] — never one four-clip artifact and never four
// separate ones.
func TestPlanMergesOnlyTransitionLinkedClips(t *testing.T) {
	sequence := Sequence{Clips: []Clip{
		clip("a", 4_000, nil),
		clip("b", 5_000, &Transition{Preset: PresetFlashWhite, DurationMS: 200}),
		clip("c", 3_000, nil),
		clip("d", 6_000, &Transition{Preset: PresetWhipLeft}),
	}}
	plan, err := sequence.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Groups) != 2 {
		t.Fatalf("groups = %+v, want 2", plan.Groups)
	}
	if plan.Groups[0].Merged != true || plan.Groups[0].ID != "group-01" {
		t.Fatalf("first group = %+v", plan.Groups[0])
	}
	if got := plan.Groups[0].ClipIndexes; len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("first group clips = %v", got)
	}
	// 4.0s + 5.0s - 0.2s overlap = 8.8s
	if plan.Groups[0].DurationMS != 8_800 {
		t.Fatalf("merged duration = %d, want 8800", plan.Groups[0].DurationMS)
	}
	if plan.Groups[1].Merged != true || plan.Groups[1].ClipIndexes[0] != 2 || plan.Groups[1].ClipIndexes[1] != 3 {
		t.Fatalf("second group = %+v", plan.Groups[1])
	}
	// whip_left default 160 ms: 3.0s + 6.0s - 0.16s = 8.84s
	if plan.Groups[1].DurationMS != 8_840 {
		t.Fatalf("second group duration = %d, want 8840", plan.Groups[1].DurationMS)
	}
	// 8.8s + 8.84s
	if plan.DurationMS != 17_640 {
		t.Fatalf("plan duration = %d, want 17640", plan.DurationMS)
	}
}

// TestPlanKeepsUntransitionedClipsSeparate pins the other half: with no
// transitions at all, every clip is its own artifact and nothing is re-encoded.
func TestPlanKeepsUntransitionedClipsSeparate(t *testing.T) {
	sequence := Sequence{Clips: []Clip{clip("a", 2_000, nil), clip("b", 2_000, nil)}}
	plan, err := sequence.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Groups) != 2 {
		t.Fatalf("groups = %+v, want 2 singletons", plan.Groups)
	}
	for _, group := range plan.Groups {
		if group.Merged || len(group.ClipIndexes) != 1 {
			t.Fatalf("group %+v must be a standalone clip", group)
		}
	}
}

// TestPlanRejectsTransitionOnFirstClip keeps the rule honest: a transition with
// no previous clip cannot join anything, so it is a payload bug.
func TestPlanRejectsTransitionOnFirstClip(t *testing.T) {
	sequence := Sequence{Clips: []Clip{clip("a", 2_000, &Transition{Preset: PresetFlashWhite})}}
	_, err := sequence.Plan()
	if err == nil || !strings.Contains(err.Error(), "no previous clip") {
		t.Fatalf("error = %v", err)
	}
}

// TestPlanRejectsSlowTransition pins the fast lane: a one-second dissolve is
// rejected instead of being silently accepted as a "fast" preset.
func TestPlanRejectsSlowTransition(t *testing.T) {
	sequence := Sequence{Clips: []Clip{
		clip("a", 5_000, nil),
		clip("b", 5_000, &Transition{Preset: PresetCrossfadeFast, DurationMS: 1_000}),
	}}
	_, err := sequence.Plan()
	if err == nil || !strings.Contains(err.Error(), "fast transitions only") {
		t.Fatalf("error = %v", err)
	}
}

// TestPlanRejectsTransitionThatSwallowsTheClip pins the 40% rule against both
// neighbours.
func TestPlanRejectsTransitionThatSwallowsTheClip(t *testing.T) {
	sequence := Sequence{Clips: []Clip{
		clip("a", 5_000, nil),
		clip("b", 600, &Transition{Preset: PresetFlashWhite, DurationMS: 300}),
	}}
	_, err := sequence.Plan()
	if err == nil || !strings.Contains(err.Error(), "exceeds 40%") {
		t.Fatalf("error = %v", err)
	}
}

// TestPlanRejectsUnknownPreset pins the closed catalogue.
func TestPlanRejectsUnknownPreset(t *testing.T) {
	sequence := Sequence{Clips: []Clip{
		clip("a", 5_000, nil),
		clip("b", 5_000, &Transition{Preset: "star_wipe"}),
	}}
	_, err := sequence.Plan()
	if err == nil || !strings.Contains(err.Error(), `"star_wipe" is not certified`) {
		t.Fatalf("error = %v", err)
	}
}

// TestLocallyExecutableNeverClaimsAnApproximation pins the assembler boundary:
// only presets with a faithful xfade program are locally executable; light
// leaks and shakes are Chronon's, and the plan must not pretend otherwise.
func TestLocallyExecutableNeverClaimsAnApproximation(t *testing.T) {
	for _, preset := range []Preset{PresetLightLeakSoft, PresetLightLeakHard, PresetShakeLight, PresetShakeStrong} {
		spec, ok := LookupPreset(preset)
		if !ok {
			t.Fatalf("missing %q", preset)
		}
		if spec.LocallyExecutable() {
			t.Fatalf("%q must not claim a local program", preset)
		}
	}
	for _, preset := range []Preset{PresetFlashWhite, PresetWhipLeft, PresetWhipRight, PresetZoomPunch, PresetCrossfadeFast, PresetSqueezeIn} {
		spec, _ := LookupPreset(preset)
		if !spec.LocallyExecutable() {
			t.Fatalf("%q should have a local program", preset)
		}
	}
}
