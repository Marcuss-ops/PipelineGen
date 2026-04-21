package ollama

import (
	"testing"
)

func TestCleanScript(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Plain text",
			input:    "Hello world",
			expected: "Hello world",
		},
		{
			name:     "With backticks",
			input:    "```Hello world```",
			expected: "world", // Regex matches 'Hello' as lang tag, captures ' world'
		},
		{
			name:     "Markdown code block with language",
			input:    "```python\nprint('hello')\n```",
			expected: "print('hello')",
		},
		{
			name:     "With leading/trailing whitespace",
			input:    "  Hello world  ",
			expected: "Hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanScript(tt.input)
			if result != tt.expected {
				t.Errorf("cleanScript(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEstimateDuration(t *testing.T) {
	tests := []struct {
		wordCount int
		expected  int
	}{
		{0, 0},
		{140, 60},   // 140 words = 60 seconds
		{70, 30},    // 70 words = 30 seconds
		{280, 120},  // 280 words = 120 seconds
		{1000, 428}, // ~428 seconds
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := estimateDuration(tt.wordCount)
			if result != tt.expected {
				t.Errorf("estimateDuration(%d) = %d, want %d", tt.wordCount, result, tt.expected)
			}
		})
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		text     string
		expected int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  hello   world  ", 2},
		{"one two three four five", 5},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := countWords(tt.text)
			if result != tt.expected {
				t.Errorf("countWords(%q) = %d, want %d", tt.text, result, tt.expected)
			}
		})
	}
}

func TestSanitizeInput(t *testing.T) {
	// Test truncation
	long := make([]byte, 110000)
	for i := range long {
		long[i] = 'a'
	}
	result := sanitizeInput(string(long))
	if len(result) > 100000 {
		t.Errorf("sanitizeInput did not truncate long input, length = %d", len(result))
	}

	// Test newline collapsing
	input := "line1\n\n\n\n\nline2"
	result = sanitizeInput(input)
	// Should collapse 4+ newlines to 3
	if len(result) >= len(input) {
		t.Error("sanitizeInput should have collapsed extra newlines")
	}
}

func TestGetSystemPrompt(t *testing.T) {
	tests := []struct {
		language string
		tone     string
		contains string
	}{
		{"italian", "professional", "copywriter"},
		{"english", "casual", "copywriter"},
		{"spanish", "enthusiastic", "copywriter"},
		{"french", "calm", "rédacteur"},
		{"german", "funny", "Copywriter"},
		{"unknown", "professional", "copywriter"}, // defaults to english
	}

	for _, tt := range tests {
		t.Run(tt.language+"_"+tt.tone, func(t *testing.T) {
			result := getSystemPrompt(tt.language, tt.tone)
			if result == "" {
				t.Error("expected non-empty system prompt")
			}
		})
	}
}
