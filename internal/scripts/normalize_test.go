package scripts

import (
	"fmt"
	"strings"
	"testing"

	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// ─── Block 1: Duration formula ────────────────────────────────────────

func TestCalculateTargetWords(t *testing.T) {
	tests := []struct {
		name            string
		durationSeconds int
		durationMinutes int
		expectedWords   int
	}{
		{
			name:            "60 seconds = 1 minute = 140 words",
			durationSeconds: 60,
			expectedWords:   140,
		},
		{
			name:            "600 seconds = 10 minutes = 1400 words",
			durationSeconds: 600,
			expectedWords:   1400,
		},
		{
			name:            "DurationMinutes has priority over DurationSeconds",
			durationSeconds: 60,
			durationMinutes: 10,
			expectedWords:   1400,
		},
		{
			name:            "30 seconds = 0 minutes floor → 1 minute = 140 words",
			durationSeconds: 30,
			expectedWords:   140,
		},
		{
			name:            "120 seconds = 2 minutes = 280 words",
			durationSeconds: 120,
			expectedWords:   280,
		},
		{
			name:            "DurationMinutes=5 direct",
			durationMinutes: 5,
			expectedWords:   700,
		},
		{
			name:            "zero defaults to 1 minute",
			durationSeconds: 0,
			durationMinutes: 0,
			expectedWords:   140,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			words := CalculateTargetWords(tt.durationSeconds, tt.durationMinutes)
			if words != tt.expectedWords {
				t.Fatalf("CalculateTargetWords(%d, %d) = %d, want %d",
					tt.durationSeconds, tt.durationMinutes, words, tt.expectedWords)
			}
		})
	}
}

// ─── Block 2: WordCountBounds (percentage-based tolerance) ────────────

func TestWordCountBounds(t *testing.T) {
	tests := []struct {
		target  int
		wantMin int
		wantMax int
	}{
		{
			target:  500,
			wantMin: 500, // 500 * 0.85 = 425, but floor is 500
			wantMax: 575, // 500 * 1.15
		},
		{
			target:  800,
			wantMin: 680, // 800 * 0.85
			wantMax: 919, // 800 * 1.15 = 920.0, int truncation
		},
		{
			target:  1500,
			wantMin: 1350, // 1500 * 0.90
			wantMax: 1650, // 1500 * 1.10
		},
		{
			target:  2000,
			wantMin: 1800, // 2000 * 0.90
			wantMax: 2200, // 2000 * 1.10
		},
		{
			target:  3000,
			wantMin: 2760, // 3000 * 0.92
			wantMax: 3240, // 3000 * 1.08
		},
		{
			target:  0,
			wantMin: 1620, // default 1800 * 0.90 (10% bracket, since 1800 <= 2000)
			wantMax: 1980, // default 1800 * 1.10
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("target_%d", tt.target), func(t *testing.T) {
			min, max := WordCountBounds(tt.target)
			if min != tt.wantMin || max != tt.wantMax {
				t.Fatalf("WordCountBounds(%d) = (%d, %d), want (%d, %d)",
					tt.target, min, max, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestWordCountBoundsNeverBelowFloor(t *testing.T) {
	// Floor is 500 words minimum
	min, _ := WordCountBounds(100)
	if min < 500 {
		t.Fatalf("min words = %d, must be >= 500", min)
	}
}

// ─── Block 3: Expand prompt ───────────────────────────────────────────

func TestExpandPromptIsControlled(t *testing.T) {
	prompt := ExpandPrompt(
		"Caitlin Clark changed the WNBA",
		"short chapter text",
		1400,
		400,
		"",
	)

	required := []string{
		"more narrative context",
		"examples",
		"consequences",
		"do not repeat",
		"do not add facts",
		"target",
	}

	lower := strings.ToLower(prompt)
	for _, phrase := range required {
		if !strings.Contains(lower, phrase) {
			t.Fatalf("expand prompt missing required phrase: %q\nPrompt:\n%s", phrase, prompt)
		}
	}
}

func TestExpandPromptIncludesDeficit(t *testing.T) {
	prompt := ExpandPrompt("topic", "chapter", 1400, 500, "")
	if !strings.Contains(prompt, "900") {
		t.Fatalf("expand prompt should mention deficit of ~900 words, got:\n%s", prompt)
	}
}

func TestExpandPromptIncludesGuidelines(t *testing.T) {
	prompt := ExpandPrompt("topic", "chapter", 1400, 500, "Use a cinematic tone")
	if !strings.Contains(prompt, "cinematic tone") {
		t.Fatalf("expand prompt should include guidelines, got:\n%s", prompt)
	}
}

// ─── Block 4: Compress prompt ─────────────────────────────────────────

func TestCompressPromptPreservesValue(t *testing.T) {
	prompt := CompressPrompt(
		"topic",
		"Very long chapter text that exceeds the target significantly and needs compression to fit within bounds",
		1400,
		2000,
		"",
	)

	required := []string{
		"removing repetition",
		"filler",
		"preserve",
		"examples",
		"numbers",
		"voice and tone",
		"target",
	}

	lower := strings.ToLower(prompt)
	for _, phrase := range required {
		if !strings.Contains(lower, phrase) {
			t.Fatalf("compress prompt missing required phrase: %q\nPrompt:\n%s", phrase, prompt)
		}
	}
}

func TestCompressPromptIncludesExcess(t *testing.T) {
	prompt := CompressPrompt("topic", "chapter", 1000, 1500, "")
	if !strings.Contains(prompt, "500") {
		t.Fatalf("compress prompt should mention removing ~500 words, got:\n%s", prompt)
	}
}

// ─── Block 5: CountWords ──────────────────────────────────────────────

func TestCountWords(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"hello world", 2},
		{"  hello   world  ", 2},
		{"", 0},
		{"one", 1},
		{"this is a test sentence", 5},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("input_%q", tt.input), func(t *testing.T) {
			got := textutil.CountWords(tt.input)
			if got != tt.want {
				t.Fatalf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
