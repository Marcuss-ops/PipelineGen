package platform

import (
	"reflect"
	"testing"
)

func TestSplitScriptSentences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty", "", []string{}},
		{"single sentence", "Hello world.", []string{"Hello world."}},
		{"multiple sentences", "First sentence. Second sentence! Third?", []string{"First sentence.", "Second sentence!", "Third?"}},
		{"newline splitting", "Line one.\nLine two.", []string{"Line one.", "Line two."}},
		{"carriage return", "Line one.\r\nLine two.", []string{"Line one.", "Line two."}},
		{"bullet trimming", "• Point one. • Point two.", []string{"Point one.", "Point two."}},
		{"no terminal punctuation", "no punctuation here", []string{"no punctuation here"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitScriptSentences(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("SplitScriptSentences(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestBuildSceneQuery(t *testing.T) {
	tests := []struct {
		name     string
		sentence string
		topic    string
		style    string
		language string
		expected string
	}{
		{"all fields", "A scene.", "topic", "style", "en", "A scene. | topic | style | en"},
		{"empty topic", "A scene.", "", "style", "en", "A scene. | style | en"},
		{"empty style", "A scene.", "topic", "", "en", "A scene. | topic | en"},
		{"empty language", "A scene.", "topic", "style", "", "A scene. | topic | style"},
		{"only sentence", "A scene.", "", "", "", "A scene."},
		{"trim whitespace", " A scene. ", " topic ", " style ", " en ", "A scene. | topic | style | en"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildSceneQuery(tt.sentence, tt.topic, tt.style, tt.language)
			if got != tt.expected {
				t.Errorf("BuildSceneQuery(%q, %q, %q, %q) = %q, want %q", tt.sentence, tt.topic, tt.style, tt.language, got, tt.expected)
			}
		})
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"empty", "", 0},
		{"spaces only", "   ", 0},
		{"one word", "hello", 1},
		{"multiple words", "hello world", 2},
		{"trim whitespace", "  hello   world  ", 2},
		{"punctuation", "hello, world!", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.expected {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestLangFullName(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{"it", "Italian"},
		{"es", "Spanish"},
		{"fr", "French"},
		{"de", "German"},
		{"en", "en"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got := LangFullName(tt.code)
			if got != tt.expected {
				t.Errorf("LangFullName(%q) = %q, want %q", tt.code, got, tt.expected)
			}
		})
	}
}

func TestSlugifyWithMax(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"ascii no truncation", "Hello World", 50, "hello-world"},
		{"ascii truncated", "very long text that should be truncated", 10, "very-long"},
		{"empty", "", 50, ""},
		{"trim dashes", "  ---trim---  ", 50, "trim"},
		{"unicode truncated", "Caffè Müller extra", 10, "caffè-müll"},
		{"emoji become hyphens then trimmed", "🔥🔥🔥🔥🔥", 4, ""},
		{"cjk truncated", "中文測試文字很長", 4, "中文測試"},
		{"negative maxLen returns full slug", "hello", -1, "hello"},
		{"zero maxLen returns full slug", "hello", 0, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SlugifyWithMax(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("SlugifyWithMax(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		n        int
		expected string
	}{
		{"short string keeps original", "short", 100, "short"},
		{"exact length", "exactly", 7, "exactly"},
		{"empty string", "", 10, ""},
		{"truncation with ellipsis", "hello world", 5, "he..."},
		{"n < 3 does not add ellipsis", "hello", 2, "he"},
		{"accented italian", "Caffè Müller", 10, "Caffè M..."},
		{"emoji at boundary", "🔥🔥🔥🔥🔥", 4, "🔥..."},
		{"emoji short n=3 ellipsis only", "🔥🔥🔥🔥🔥", 3, "..."},
		{"chinese characters", "中文測試文字很長", 6, "中文測..."},
		{"mixed ascii and unicode", "hello🔥world", 8, "hello..."},
		{"no truncation needed", "hello", 10, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.n)
			if got != tt.expected {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.expected)
			}
		})
	}
}

func TestContainsCI(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{"exact match", "Hello World", "Hello World", true},
		{"case difference", "Hello World", "hello world", true},
		{"substring", "Hello World", "WORLD", true},
		{"empty substr", "Hello World", "", false},
		{"empty both", "", "", false},
		{"not found", "Hello World", "xyz", false},
		{"unicode", "Caffè Müller", "caffè", true},
		{"unicode case", "Caffè Müller", "MÜLLER", true},
		{"empty s", "", "a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsCI(tt.s, tt.substr)
			if got != tt.expected {
				t.Errorf("ContainsCI(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.expected)
			}
		})
	}
}

func TestCleanForVoiceover(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"plain text", "Hello world.", "Hello world."},
		{"heading", "# Title", "Title"},
		{"bold", "**bold text**", "bold text"},
		{"italic", "*italic text*", "italic text"},
		{"blockquote", "> quote", "quote"},
		{"bracket artifact", "text [music] more", "text more"},
		{"chapter label", "Chapter 1: Intro", "Intro"},
		{"multiple newlines", "a\n\n\n\nb", "a\n\nb"},
		{"multiple spaces", "a   b", "a b"},
		{"horizontal rule", "text\n---\nmore", "text\n\nmore"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanForVoiceover(tt.input)
			if got != tt.expected {
				t.Errorf("CleanForVoiceover(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
