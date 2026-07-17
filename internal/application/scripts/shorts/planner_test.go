package shorts

import (
	"errors"
	"testing"
)

func TestBuildShortsPlan(t *testing.T) {
	got, err := Build(Request{
		ID: "short-1", Text: "one two three four five six", DurationMs: 6000,
		Clips:               []Clip{{ID: "clip-a"}},
		IncludeSoundEffects: true,
		SoundEffects:        []SoundEffect{{ID: "sfx-1", File: "/assets/sfx/hit.wav", AtMs: 1200}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion || len(got.Captions) != 2 || len(got.SoundEffects) != 1 {
		t.Fatalf("unexpected shorts response: %+v", got)
	}
	if got.SoundEffects[0].Volume != 0.5 {
		t.Fatalf("default volume = %v", got.SoundEffects[0].Volume)
	}
}

func TestBuildShortsPlanCanDisableSoundEffects(t *testing.T) {
	got, err := Build(Request{ID: "short-1", Text: "one two", DurationMs: 1000, Clips: []Clip{{ID: "clip-a"}}, SoundEffects: []SoundEffect{{File: "hit.wav", AtMs: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.IncludeSoundEffects || len(got.SoundEffects) != 0 {
		t.Fatalf("sound effects should be disabled: %+v", got)
	}
}

func TestBuildShortsPlanRejectsInvalidSoundEffect(t *testing.T) {
	_, err := Build(Request{ID: "short-1", Text: "one", DurationMs: 1000, Clips: []Clip{{ID: "clip-a"}}, IncludeSoundEffects: true, SoundEffects: []SoundEffect{{File: "hit.wav", AtMs: 1000}}})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v", err)
	}
}
