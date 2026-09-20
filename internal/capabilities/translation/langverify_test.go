// Package translation — langverify_test.go: contract tests for the
// language/script verification of a translated string.
package translation

import "testing"

func TestVerifyTargetLanguage(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		lang     string
		wantOK   bool
		wantHint string
	}{
		{
			name:   "russian text is russian",
			text:   "Привет, как дела сегодня?",
			lang:   "ru",
			wantOK: true,
		},
		{
			name:   "latin text is not russian",
			text:   "Hello, how are you doing today?",
			lang:   "ru",
			wantOK: false,
		},
		{
			name:   "english text is english",
			text:   "The progress rarely arrives in one dramatic moment and it emerges from repeating a clear process.",
			lang:   "en",
			wantOK: true,
		},
		{
			name:   "italian text is italian",
			text:   "La coerenza trasforma un'idea in risultati e il processo continua ancora.",
			lang:   "it",
			wantOK: true,
		},
		{
			name:   "english text is not indonesian",
			text:   "The quick brown fox jumps over the lazy dog and runs away",
			lang:   "id",
			wantOK: false,
		},
		{
			name:   "japanese text is japanese",
			text:   "これはテストです",
			lang:   "ja",
			wantOK: true,
		},
		{
			name:   "latin text is not japanese",
			text:   "hello world this is a test",
			lang:   "ja",
			wantOK: false,
		},
		{
			name:   "short fragment abstains",
			text:   "OK",
			lang:   "ru",
			wantOK: true,
		},
		{
			name:   "digits only abstain",
			text:   "2026",
			lang:   "ru",
			wantOK: true,
		},
		{
			name:   "unknown target abstains",
			text:   "whatever text here",
			lang:   "xx",
			wantOK: true,
		},
		{
			name:   "empty text abstains",
			text:   "",
			lang:   "ru",
			wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := VerifyTargetLanguage(tc.text, tc.lang)
			if ok != tc.wantOK {
				t.Fatalf("VerifyTargetLanguage(%q, %q) = %v (%s), want %v", tc.text, tc.lang, ok, reason, tc.wantOK)
			}
			if ok && reason != "" {
				t.Fatalf("positive verdict must carry no reason, got %q", reason)
			}
			if !ok && reason == "" {
				t.Fatalf("negative verdict must carry a reason")
			}
		})
	}
}

func TestBaseLanguage(t *testing.T) {
	cases := map[string]string{
		"pt-BR":   "pt",
		"EN":      "en",
		"zh_Hans": "zh",
		"":        "",
		"  it  ":  "it",
	}
	for in, want := range cases {
		if got := baseLanguage(in); got != want {
			t.Fatalf("baseLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
