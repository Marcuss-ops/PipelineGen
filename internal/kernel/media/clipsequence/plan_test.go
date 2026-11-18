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
// light leaks and the xfade looks have a local program; shakes are Chronon's,
// and the plan must not pretend otherwise.
func TestLocallyExecutableNeverClaimsAnApproximation(t *testing.T) {
	for _, preset := range []Preset{PresetShakeLight, PresetShakeStrong} {
		spec, ok := LookupPreset(preset)
		if !ok {
			t.Fatalf("missing %q", preset)
		}
		if spec.LocallyExecutable() {
			t.Fatalf("%q must not claim a local program", preset)
		}
	}
	for _, preset := range []Preset{
		PresetFlashWhite, PresetWhipLeft, PresetWhipRight, PresetZoomPunch,
		PresetCrossfadeFast, PresetSqueezeIn, PresetLightLeakSoft, PresetLightLeakHard,
	} {
		spec, _ := LookupPreset(preset)
		if !spec.LocallyExecutable() {
			t.Fatalf("%q should have a local program", preset)
		}
	}
}

// TestLightLeaksCarryASweepProgram pins the light leaks' local definition: each
// has a colour, a peak inside (0,1], a normalised band width and a direction,
// and no xfade program to silently take over.
func TestLightLeaksCarryASweepProgram(t *testing.T) {
	for _, preset := range []Preset{PresetLightLeakSoft, PresetLightLeakHard} {
		spec, ok := LookupPreset(preset)
		if !ok {
			t.Fatalf("missing %q", preset)
		}
		if spec.Leak == nil {
			t.Fatalf("%q has no sweep program", preset)
		}
		if spec.Xfade != "" {
			t.Fatalf("%q must not also claim an xfade program (%q)", preset, spec.Xfade)
		}
		leak := *spec.Leak
		if len(leak.Color) != 7 || leak.Color[0] != '#' {
			t.Fatalf("%q colour = %q, want #RRGGBB", preset, leak.Color)
		}
		if leak.PeakAlpha <= 0 || leak.PeakAlpha > 1 {
			t.Fatalf("%q peak alpha = %v", preset, leak.PeakAlpha)
		}
		if leak.BandWidth <= 0 || leak.BandWidth > 1 {
			t.Fatalf("%q band width = %v", preset, leak.BandWidth)
		}
		if leak.Direction != "right" && leak.Direction != "left" {
			t.Fatalf("%q direction = %q", preset, leak.Direction)
		}
	}
}

// TestFastCeilingIsThreeHundredMilliseconds pins the fast editorial lane:
// transitions between clips never exceed 0.3 s, and the presets' defaults sit
// inside that ceiling.
func TestFastCeilingIsThreeHundredMilliseconds(t *testing.T) {
	if MaxFastTransitionMS != 300 {
		t.Fatalf("MaxFastTransitionMS = %d, want 300", MaxFastTransitionMS)
	}
	for _, spec := range Presets() {
		if spec.DefaultDurationMS > MaxFastTransitionMS {
			t.Fatalf("preset %q default %d ms exceeds the 300 ms ceiling", spec.Preset, spec.DefaultDurationMS)
		}
	}
	sequence := Sequence{Clips: []Clip{
		clip("a", 5_000, nil),
		clip("b", 5_000, &Transition{Preset: PresetLightLeakHard, DurationMS: MaxFastTransitionMS}),
	}}
	if _, err := sequence.Plan(); err != nil {
		t.Fatalf("a 300 ms light leak must be accepted: %v", err)
	}
	sequence.Clips[1].TransitionIn.DurationMS = MaxFastTransitionMS + 1
	if _, err := sequence.Plan(); err == nil || !strings.Contains(err.Error(), "fast transitions only") {
		t.Fatalf("a 301 ms transition must be rejected, got %v", err)
	}
}

// TestLeakOverridesApplyPerTransition pins the per-join parameterisation: the
// plan carries the resolved program, so two joins of the SAME preset can look
// different, and an omitted knob keeps the preset's value.
func TestLeakOverridesApplyPerTransition(t *testing.T) {
	peak := 0.95
	band := 0.08
	sequence := Sequence{Clips: []Clip{
		clip("a", 6_000, nil),
		clip("b", 6_000, &Transition{Preset: PresetLightLeakHard, DurationMS: 240,
			Leak: &LeakOverride{Color: "#ff00aa", PeakAlpha: &peak, BandWidth: &band, Direction: "left"}}),
		clip("c", 6_000, &Transition{Preset: PresetLightLeakHard, DurationMS: 240}),
	}}
	plan, err := sequence.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Groups) != 1 || len(plan.Groups[0].Transitions) != 2 {
		t.Fatalf("groups = %+v, want one merged group with two joins", plan.Groups)
	}
	tuned := plan.Groups[0].Transitions[0].Leak
	if tuned == nil {
		t.Fatal("tuned join has no leak program")
	}
	if tuned.Color != "#FF00AA" || tuned.Direction != "left" {
		t.Fatalf("tuned leak = %+v, want the override (uppercased colour, left sweep)", *tuned)
	}
	if tuned.PeakAlpha != peak || tuned.BandWidth != band {
		t.Fatalf("tuned leak = %+v, want peak %v band %v", *tuned, peak, band)
	}
	untouched := plan.Groups[0].Transitions[1].Leak
	defaults, _ := LookupPreset(PresetLightLeakHard)
	if untouched == nil || defaults.Leak == nil {
		t.Fatal("untouched join has no leak program")
	}
	if *untouched != *defaults.Leak {
		t.Fatalf("untouched leak = %+v, want the preset defaults %+v", *untouched, *defaults.Leak)
	}
}

// TestLeakOverridesAreRejectedWhereTheyCannotApply keeps the contract honest:
// an override on a preset with no band, or with a value the assembler cannot
// render, is a rejected sequence rather than a silently unchanged look.
func TestLeakOverridesAreRejectedWhereTheyCannotApply(t *testing.T) {
	zero := 0.0
	cases := []struct {
		name string
		leak *LeakOverride
		want string
	}{
		{"override on an xfade preset", &LeakOverride{Color: "#FFFFFF"}, "has no light-leak program"},
		{"bad colour", &LeakOverride{Color: "white"}, "must be #RRGGBB"},
		{"zero peak", &LeakOverride{PeakAlpha: &zero}, "peak_alpha"},
		{"zero band", &LeakOverride{BandWidth: &zero}, "band_width"},
		{"bad direction", &LeakOverride{Direction: "up"}, "direction"},
	}
	for _, tc := range cases {
		preset := PresetLightLeakSoft
		if tc.name == "override on an xfade preset" {
			preset = PresetCrossfadeFast
		}
		sequence := Sequence{Clips: []Clip{
			clip("a", 5_000, nil),
			clip("b", 5_000, &Transition{Preset: preset, DurationMS: 200, Leak: tc.leak}),
		}}
		_, err := sequence.Plan()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}
