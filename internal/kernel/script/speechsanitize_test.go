package script

import "testing"

func TestSanitizeForSpeechRemovesMarkdownWithoutLosingWords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bold", "Questo è **molto** importante.", "Questo è molto importante."},
		{"italic", "Questo è *davvero* importante.", "Questo è davvero importante."},
		{"underscore emphasis", "Questo è __davvero__ _importante_.", "Questo è davvero importante."},
		{"inline code", "Usa `ffmpeg` per encodare.", "Usa ffmpeg per encodare."},
		{"fenced code", "Prima\n```bash\nffmpeg -i in.mp4 out.mp4\n```\nDopo.", "Prima Dopo."},
		{"heading", "# Titolo\nCorpo del testo.", "Titolo Corpo del testo."},
		{"link", "Leggi [la fonte](https://example.com) ora.", "Leggi la fonte ora."},
		{"image", "![Corrado Augias](https://example.com/a.jpg) al centro.", "Corrado Augias al centro."},
		{"bullet", "* primo\n* secondo", "primo secondo"},
		{"blockquote", "> una citazione", "una citazione"},
		{"rule", "prima\n---\ndopo", "prima dopo"},
		{"html", "Testo <b>forte</b>.", "Testo forte."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeForSpeech(tc.in); got != tc.want {
				t.Fatalf("SanitizeForSpeech(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeForSpeechFormattingOnlyBecomesEmpty(t *testing.T) {
	if got := SanitizeForSpeech("**  ** ` ` ###"); got != "" {
		t.Fatalf("formatting-only input = %q, want empty", got)
	}
}

func TestSanitizeForSpeechIsIdempotentForPlainText(t *testing.T) {
	const plain = "Una frase già pulita, con punteggiatura."
	if got := SanitizeForSpeech(SanitizeForSpeech(plain)); got != plain {
		t.Fatalf("idempotent sanitize = %q, want %q", got, plain)
	}
}
