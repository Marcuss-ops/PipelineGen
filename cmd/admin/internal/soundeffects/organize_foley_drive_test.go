package soundeffects

import (
	"testing"
)

func TestClassifyFoleyDriveName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"camera shutter", "Camera_Shutter_01.wav", "Camera"},
		{"typing keyboard", "Keyboard_Typing_Fast.mp3", "Typing & Paper"},
		{"clock tick", "Clock_Tick_Tock.wav", "Mechanical & Machines"},
		{"crowd applause", "Crowd_Applause_Cheering.wav", "Human & Animal"},
		{"party horn", "Party_Horn_Blast.wav", "Horns & Bells"},
		{"glass break", "Glass_Break_Shatter.wav", "Breakage & Pops"},
		{"unknown", "weird_foley_sound.wav", "Other Foley"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyFoleyDriveName(tc.input)
			if got != tc.want {
				t.Errorf("classifyFoleyDriveName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
