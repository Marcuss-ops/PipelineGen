package ollama

import "testing"

// TestTranslationOutputBudget pins the per-call output budget. The previous
// floor (512 tokens) was paid on EVERY cue: a 30-char subtitle cue needs ~10
// output tokens, yet the model generated 294-497 of them.
func TestTranslationOutputBudget(t *testing.T) {
	cases := []struct {
		name      string
		sourceLen int
		want      int
	}{
		{name: "empty text keeps the floor", sourceLen: 0, want: minTranslationPredictTokens},
		{name: "short cue stays at the floor", sourceLen: 30, want: minTranslationPredictTokens},
		{name: "cue at the floor boundary", sourceLen: 64, want: minTranslationPredictTokens},
		{name: "one sentence scales with the source", sourceLen: 200, want: 164},
		{name: "scene-sized text", sourceLen: 356, want: 242},
		{name: "long text is capped", sourceLen: 60_000, want: maxTranslationPredictTokens},
		{name: "negative guard", sourceLen: -5, want: minTranslationPredictTokens},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := translationOutputBudget(tc.sourceLen); got != tc.want {
				t.Fatalf("translationOutputBudget(%d) = %d, want %d", tc.sourceLen, got, tc.want)
			}
		})
	}
}

// TestTranslationOutputBudget_IsMonotonicAndBounded pins the two properties the
// caller relies on: a longer source never gets a smaller budget, and no budget
// escapes [floor, cap].
func TestTranslationOutputBudget_IsMonotonicAndBounded(t *testing.T) {
	previous := 0
	for sourceLen := 0; sourceLen <= 20_000; sourceLen += 37 {
		got := translationOutputBudget(sourceLen)
		if got < previous {
			t.Fatalf("budget shrank from %d to %d at source length %d", previous, got, sourceLen)
		}
		if got < minTranslationPredictTokens || got > maxTranslationPredictTokens {
			t.Fatalf("budget %d out of [%d,%d] at source length %d",
				got, minTranslationPredictTokens, maxTranslationPredictTokens, sourceLen)
		}
		previous = got
	}
}
