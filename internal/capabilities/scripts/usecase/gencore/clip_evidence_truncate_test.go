package gencore

import (
	"strings"
	"testing"
)

// TestClipEvidenceField_UnlimitedByDefault pins the legacy contract: without
// the operator knob every field passes through byte-identical.
func TestClipEvidenceField_UnlimitedByDefault(t *testing.T) {
	resetClipEvidenceFieldCharsForTest()
	long := strings.Repeat("word ", 1000)
	if got := clipEvidenceField(long); got != long {
		t.Fatal("default must not truncate evidence fields")
	}
}

// TestClipEvidenceField_TruncatesAtWordBoundary pins the gated behavior: a
// pathological field is cut at a word boundary with an explicit marker,
// while short fields stay untouched.
func TestClipEvidenceField_TruncatesAtWordBoundary(t *testing.T) {
	t.Setenv(EnvClipEvidenceFieldChars, "100")
	resetClipEvidenceFieldCharsForTest()
	if got := clipEvidenceFieldChars(); got != 100 {
		t.Fatalf("knob = %d, want 100", got)
	}
	short := "A short transcript."
	if got := clipEvidenceField(short); got != short {
		t.Fatalf("short field changed: %q", got)
	}
	long := strings.Repeat("word ", 1000)
	got := clipEvidenceField(long)
	if len(got) >= len(long) {
		t.Fatal("long field was not truncated")
	}
	if !strings.HasSuffix(got, "[...evidence truncated]") {
		t.Fatalf("missing truncation marker: %q", got[len(got)-30:])
	}
	if strings.HasSuffix(strings.TrimSuffix(got, " [...evidence truncated]"), " ") {
		t.Fatal("cut must land on a word boundary")
	}
}

// TestClipEvidenceField_InvalidKnobIsIgnored pins fail-open parsing: garbage
// keeps the legacy unlimited behavior instead of failing generation.
func TestClipEvidenceField_InvalidKnobIsIgnored(t *testing.T) {
	t.Setenv(EnvClipEvidenceFieldChars, "huge")
	resetClipEvidenceFieldCharsForTest()
	if got := clipEvidenceFieldChars(); got != 0 {
		t.Fatalf("knob = %d, want 0 (unlimited)", got)
	}
}
