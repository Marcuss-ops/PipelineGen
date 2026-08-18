package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func sampleInput() PresetSampleInput {
	return PresetSampleInput{
		JobFingerprint: "job-fp-123",
		SceneID:        "scene_03",
		SemanticID:     "sem_scene03_person_01",
		PresetFamily:   "person_image",
		Presets:        []string{"portrait_card_right", "portrait_card_left"},
		Animations:     []string{"slide_left", "slide_right", "fade_in"},
	}
}

// TestDeterministicPresetSampler_Deterministic pins the replay contract: the
// same seed always yields the same preset and animation across repeated calls.
func TestDeterministicPresetSampler_Deterministic(t *testing.T) {
	in := sampleInput()
	first := DefaultDeterministicPresetSampler.Sample(in)
	for i := 0; i < 100; i++ {
		got := DefaultDeterministicPresetSampler.Sample(in)
		require.Equal(t, first, got)
	}
}

// TestDeterministicPresetSampler_SelectsValidCandidates pins that the result
// is always drawn from the candidate lists (never an invented id).
func TestDeterministicPresetSampler_SelectsValidCandidates(t *testing.T) {
	for i := 0; i < 200; i++ {
		in := sampleInput()
		in.SemanticID = "sem_" + string(rune('a'+i%26)) + string(rune('0'+i%10)) + "_" + string(rune('a'+(i/26)%26))
		got := DefaultDeterministicPresetSampler.Sample(in)
		require.Contains(t, in.Presets, got.Preset)
		require.Contains(t, in.Animations, got.Animation)
	}
}

func TestDeterministicPresetSampler_SingleCandidate(t *testing.T) {
	in := sampleInput()
	in.Presets = []string{"only_preset"}
	in.Animations = []string{"only_animation"}
	got := DefaultDeterministicPresetSampler.Sample(in)
	require.Equal(t, "only_preset", got.Preset)
	require.Equal(t, "only_animation", got.Animation)
}

func TestDeterministicPresetSampler_EmptyCandidates(t *testing.T) {
	in := sampleInput()
	in.Presets = nil
	in.Animations = nil
	got := DefaultDeterministicPresetSampler.Sample(in)
	require.Equal(t, "", got.Preset)
	require.Equal(t, "", got.Animation)
}

// TestDeterministicPresetSampler_SeedDistributesSelection proves the seed
// actually drives the selection (not a constant first-index): across many
// distinct semantic ids both candidates are eventually selected.
func TestDeterministicPresetSampler_SeedDistributesSelection(t *testing.T) {
	seenPreset := map[string]bool{}
	seenAnimation := map[string]bool{}
	for i := 0; i < 200; i++ {
		in := sampleInput()
		in.SemanticID = "sem_dist_" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('0'+i%10))
		got := DefaultDeterministicPresetSampler.Sample(in)
		seenPreset[got.Preset] = true
		seenAnimation[got.Animation] = true
	}
	require.True(t, seenPreset["portrait_card_right"], "expected both presets to be selected across seeds")
	require.True(t, seenPreset["portrait_card_left"], "expected both presets to be selected across seeds")
	require.True(t, seenAnimation["slide_left"], "expected multiple animations to be selected across seeds")
	require.True(t, seenAnimation["slide_right"], "expected multiple animations to be selected across seeds")
}

// TestDeterministicPresetSampler_SeedComponentsMatter pins that each of the
// four seed components contributes to the selection: changing any one of them
// (while holding the candidate lists fixed) can change the result.
func TestDeterministicPresetSampler_SeedComponentsMatter(t *testing.T) {
	base := sampleInput()
	base.Presets = make([]string, 64)
	for i := range base.Presets {
		base.Presets[i] = "p" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
	}
	base.Animations = make([]string, 64)
	for i := range base.Animations {
		base.Animations[i] = "a" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
	}

	mutations := map[string]func(*PresetSampleInput){
		"job_fingerprint": func(i *PresetSampleInput) { i.JobFingerprint = "different-fp" },
		"scene_id":        func(i *PresetSampleInput) { i.SceneID = "scene_99" },
		"semantic_id":     func(i *PresetSampleInput) { i.SemanticID = "sem_other" },
		"preset_family":   func(i *PresetSampleInput) { i.PresetFamily = "important_phrase" },
	}
	baseline := DefaultDeterministicPresetSampler.Sample(base)
	for name, mutate := range mutations {
		in := base
		mutate(&in)
		got := DefaultDeterministicPresetSampler.Sample(in)
		require.NotEqual(t, baseline, got, "changing %s must affect the selection", name)
	}
}

// TestDeterministicPresetSampler_Golden pins the exact (preset, animation)
// pair for a fixed seed, so any change to the hash/selection math breaks this
// test loudly — the replay-stability guarantee encoded as a golden.
func TestDeterministicPresetSampler_Golden(t *testing.T) {
	got := DefaultDeterministicPresetSampler.Sample(sampleInput())
	require.Equal(t, "portrait_card_left", got.Preset)
	require.Equal(t, "slide_left", got.Animation)
}
