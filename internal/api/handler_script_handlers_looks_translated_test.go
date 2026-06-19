package api

import "testing"

// ──────────────────────────────────────────────
// looksTranslated tests (usata dal batch flow)
// ──────────────────────────────────────────────

func TestLooksTranslated_Italian(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"Italian text with multiple markers", "Il mondo è un posto meraviglioso e la vita è bella", true},
		{"Italian with che/sono/della", "La storia della filosofia è complessa e che richiede studio", true},
		{"Italian short but with markers", "Il cane è nel parco con gli amici", true},
		{"English text for Italian", "The world is a wonderful place and life is beautiful", false},
		{"English-heavy text", "This is the best thing that could happen in the world", false},
		{"Italian with English interference - markers win", "Ora il cane è nel parco e gioca", true},
		{"Empty string", "", false},
		{"Short text under 20 chars", "Ciao mondo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksTranslated(tt.text, "it", ""); got != tt.want {
				t.Errorf("looksTranslated(%q, \"it\", \"\") = %v, want %v", tt.text[:min(len(tt.text), 50)], got, tt.want)
			}
		})
	}
}

func TestLooksTranslated_Spanish(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"Spanish text with multiple markers", "El mundo es un lugar maravilloso y la vida es bella", true},
		{"Spanish with que/por/para", "La historia de la filosofia es compleja y que requiere estudio", true},
		{"English text for Spanish", "The world is a wonderful place and life is beautiful", false},
		{"Short Spanish", "en la casa es bonita", true},
		{"Spanish with English interference", "El the world es un lugar maravilloso and life es bella", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksTranslated(tt.text, "es", ""); got != tt.want {
				t.Errorf("looksTranslated(%q, \"es\", \"\") = %v, want %v", tt.text[:min(len(tt.text), 50)], got, tt.want)
			}
		})
	}
}

func TestLooksTranslated_French(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"French text with multiple markers", "Le monde est un endroit merveilleux et la vie est belle", true},
		{"French with que/dans/avec", "La vie dans la ville est belle avec les amis", true},
		{"English text for French", "The world is a wonderful place and life is beautiful", false},
		{"French with des/ce/cette", "Cette histoire est dans le livre que j'ai lu", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksTranslated(tt.text, "fr", ""); got != tt.want {
				t.Errorf("looksTranslated(%q, \"fr\", \"\") = %v, want %v", tt.text[:min(len(tt.text), 50)], got, tt.want)
			}
		})
	}
}

func TestLooksTranslated_German(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"German text with multiple markers", "Der Hund ist im Park und die Katze ist zu Hause", true},
		{"German with der/die/das", "Das ist ein interessantes Buch über die Geschichte", true},
		{"English text for German", "The world is a wonderful place and life is beautiful", false},
		{"German with wird/werden/nicht", "Es wird nicht einfach sein, aber wir werden es schaffen", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksTranslated(tt.text, "de", ""); got != tt.want {
				t.Errorf("looksTranslated(%q, \"de\", \"\") = %v, want %v", tt.text[:min(len(tt.text), 50)], got, tt.want)
			}
		})
	}
}

func TestLooksTranslated_UnknownLanguage(t *testing.T) {
	if got := looksTranslated("Some English text here", "xx", ""); got != true {
		t.Errorf("looksTranslated(%q, \"xx\", \"\") = %v, want true", "Some English text here", got)
	}
}

func TestLooksTranslated_ShortText(t *testing.T) {
	tests := []struct {
		name string
		text string
		lang string
		want bool
	}{
		{"Text under 20 chars returns false", "Ciao", "it", false},
		{"19 chars returns false", "Il cane è bravo", "it", false},
		{"21+ chars with markers", "Ora il cane è nel parco", "it", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksTranslated(tt.text, tt.lang, ""); got != tt.want {
				t.Errorf("looksTranslated(%q, %q, \"\") = %v, want %v", tt.text, tt.lang, got, tt.want)
			}
		})
	}
}

func TestLooksTranslated_EnglishInterference(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		lang   string
		source string
		want   bool
	}{
		{"English with scattered Italian words", "This is the best thing that could happen in the world ed il resto", "it", "en", false},
		{"Italian with reasonable English mix", "Ora il cane è nel parco con gli amici and the life is bella", "it", "en", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksTranslated(tt.text, tt.lang, tt.source); got != tt.want {
				t.Errorf("looksTranslated(%q, %q, %q) = %v, want %v", tt.text[:min(len(tt.text), 60)], tt.lang, tt.source, got, tt.want)
			}
		})
	}
}
