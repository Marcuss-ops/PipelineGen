package script

import (
	"reflect"
	"testing"
)

// TestCollectClipRequirementsMergesBindings pins the rule the CLI and the
// server preflight both depend on: one entry per clip, speech required if ANY
// binding needs subtitles, scenes in binding order, and the protected
// intro/outro media never requiring speech.
func TestCollectClipRequirementsMergesBindings(t *testing.T) {
	env := &GenerationEnvelopeV2{
		Items: []GenerationItemV2{
			{
				Source: SourceSpec{ClipIDs: []string{"clip-source"}},
				ScriptParams: ScriptSpec{
					Segments: []ScriptSegment{
						{ID: "scene-1", ClipIDs: []string{"clip-a"}},
						{ID: "scene-2", ClipIDs: []string{"clip-a", "clip-b"}},
						{ID: "scene-3", ClipIDs: []string{"clip-mute"}, NoVoiceover: true},
					},
				},
				Intro: &FixedSection{ClipIDs: []string{"clip-intro"}},
				Outro: &FixedSection{ClipIDs: []string{"clip-mute"}},
			},
		},
	}
	got := CollectClipRequirements(env)
	want := []ClipRequirement{
		{ClipID: "clip-source", NeedSpeech: true},
		{ClipID: "clip-a", NeedSpeech: true, Scenes: []string{"scene-1", "scene-2"}},
		{ClipID: "clip-b", NeedSpeech: true, Scenes: []string{"scene-2"}},
		{ClipID: "clip-mute", NeedSpeech: false, Scenes: []string{"scene-3"}},
		{ClipID: "clip-intro", NeedSpeech: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requirements = %+v, want %+v", got, want)
	}
}

// TestCollectClipRequirementsDedupesWhitespace guards the small normalisation
// that used to be CLI-only: " clip-a " and "clip-a" are the same asset, and an
// empty id is not a clip at all.
func TestCollectClipRequirementsDedupesWhitespace(t *testing.T) {
	env := &GenerationEnvelopeV2{
		Items: []GenerationItemV2{
			{
				ScriptParams: ScriptSpec{
					Segments: []ScriptSegment{
						{ID: "scene-1", ClipIDs: []string{" clip-a ", "", "   "}},
					},
				},
			},
		},
	}
	got := CollectClipRequirements(env)
	if len(got) != 1 || got[0].ClipID != "clip-a" {
		t.Fatalf("requirements = %+v, want a single trimmed clip-a", got)
	}
}

func TestCollectClipRequirementsNilEnvelope(t *testing.T) {
	if got := CollectClipRequirements(nil); got != nil {
		t.Fatalf("requirements = %+v, want nil", got)
	}
}
