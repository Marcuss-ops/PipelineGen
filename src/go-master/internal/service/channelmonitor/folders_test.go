package channelmonitor

import "testing"

func TestExtractProtagonist(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{
			title: "Floyd Mayweather Interviews",
			want:  "Floyd Mayweather",
		},
		{
			title: "Floyd Mayweather training highlights and best moments",
			want:  "Floyd Mayweather",
		},
		{
			title: "Gervonta Davis vs Ryan Garcia Full Fight Highlights",
			want:  "Gervonta Davis",
		},
		{
			title: "Nikola Tesla documentary",
			want:  "Nikola Tesla",
		},
	}

	for _, tc := range tests {
		got := extractProtagonist(tc.title)
		if got != tc.want {
			t.Fatalf("extractProtagonist(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

func TestNameSimilarityScore(t *testing.T) {
	if s := nameSimilarityScore("Floyd Mayweather", "Floyd Mayweather training highlights"); s < 0.90 {
		t.Fatalf("expected high similarity score, got %.4f", s)
	}
	if s := nameSimilarityScore("Floyd Mayweather", "Mike Tyson"); s >= 0.70 {
		t.Fatalf("expected low similarity score, got %.4f", s)
	}
}

func TestNormalizeProtagonistKey(t *testing.T) {
	got := normalizeProtagonistKey("Floyd Mayweather training highlights")
	if got != "floyd mayweather" {
		t.Fatalf("normalizeProtagonistKey mismatch: got %q", got)
	}
}

func TestParseCategoryFromGemmaResponse(t *testing.T) {
	candidates := []string{"Boxe", "Crime", "Discovery", "HipHop", "Music", "Various", "Wwe"}
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "json", raw: `{"category":"Music","reason":"artist interview"}`, want: "Music"},
		{name: "plain", raw: "Boxe", want: "Boxe"},
		{name: "text", raw: "The best category is Wwe.", want: "Wwe"},
		{name: "various", raw: `{"category":"Various","reason":"misc content"}`, want: "Various"},
	}
	for _, tc := range tests {
		got, _ := parseCategoryFromGemmaResponse(tc.raw, candidates)
		if got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestApplyCategoryGuardrails_HipHopArtistInterviewToMusic(t *testing.T) {
	got := applyCategoryGuardrails("HipHop", "50 Cent full interview 2026", "50 Cent")
	if got != "Music" {
		t.Fatalf("expected Music, got %q", got)
	}
}

func TestFallbackCategory(t *testing.T) {
	tests := []struct {
		title       string
		protagonist string
		want        string
	}{
		{title: "Floyd Mayweather training highlights", protagonist: "Floyd Mayweather", want: "Boxe"},
		{title: "WWE Raw Roman Reigns returns", protagonist: "Roman Reigns", want: "Wwe"},
		{title: "El Chapo court case documentary", protagonist: "El Chapo", want: "Crime"},
		{title: "50 Cent full interview 2026", protagonist: "50 Cent", want: "Music"},
		{title: "Random behind the scenes clip", protagonist: "Unknown", want: "Various"},
	}
	for _, tc := range tests {
		if got := fallbackCategory(tc.title, tc.protagonist); got != tc.want {
			t.Fatalf("fallbackCategory(%q,%q) got %q want %q", tc.title, tc.protagonist, got, tc.want)
		}
	}
}
