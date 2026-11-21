package adapters

import "testing"

func TestFindAnnotationSpanFoldsDiacriticsWithoutRewritingText(t *testing.T) {
	text := "Il caffè era pronto."
	span, ok := findAnnotationSpan(text, "caffe")
	if !ok {
		t.Fatal("accent-insensitive span was not found")
	}
	if got := string([]rune(text)[span.StartRune:span.EndRune]); got != "caffè" {
		t.Fatalf("span text = %q, want the original accented word", got)
	}
}
