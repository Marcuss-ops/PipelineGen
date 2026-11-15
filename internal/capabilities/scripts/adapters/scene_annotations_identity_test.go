package adapters

import "testing"

func TestExpandPersonCanonicalNameLeavesAmbiguousSurnameUnresolved(t *testing.T) {
	text := "John Smith met John Doe at the courthouse."
	if got := expandPersonCanonicalName(text, "John"); got != "John" {
		t.Fatalf("ambiguous expansion = %q, want the bare extracted surface", got)
	}
	if got := expandPersonCanonicalName("Muhammad Ali changed boxing.", "Muhammad"); got != "Muhammad Ali" {
		t.Fatalf("unique expansion = %q, want Muhammad Ali", got)
	}
}

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
