package textutil

import "testing"

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "skips empty and whitespace", values: []string{"", " \t", "value", "later"}, want: "value"},
		{name: "returns original non-empty value", values: []string{"  value  "}, want: "  value  "},
		{name: "all empty", values: []string{"", "  "}, want: ""},
		{name: "no values", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FirstNonEmpty(tt.values...); got != tt.want {
				t.Fatalf("FirstNonEmpty(%q) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

// These cases were previously colocated in the (now retired)
// internal/capabilities/images/workflow/fullimages/service_test.go — they exercise
// textutil.SafeName, so they live here with the implementation they cover.

func TestSafeName_RemovesSpecialChars(t *testing.T) {
	got := SafeName("old stone house with a man!!!")
	if got != "old stone house with a man" {
		t.Fatalf("expected 'old stone house with a man', got %q", got)
	}
}

func TestSafeName_CollapsesSpaces(t *testing.T) {
	got := SafeName("knight   on   horse")
	if got != "knight on horse" {
		t.Fatalf("expected 'knight on horse', got %q", got)
	}
}

func TestSafeName_HyphensToSpaces(t *testing.T) {
	got := SafeName("medievale-castle")
	if got != "medievale castle" {
		t.Fatalf("expected 'medievale castle', got %q", got)
	}
}

func TestSafeName_UnderscoresToSpaces(t *testing.T) {
	got := SafeName("stone_cottage")
	if got != "stone cottage" {
		t.Fatalf("expected 'stone cottage', got %q", got)
	}
}

func TestSafeName_OnlyAlphanumericAndSpaces(t *testing.T) {
	got := SafeName("Cus D'Amato!")
	if got != "Cus D Amato" {
		t.Fatalf("expected 'Cus D Amato', got %q", got)
	}
}

func TestSafeName_Empty(t *testing.T) {
	got := SafeName("")
	if got != "untitled" {
		t.Fatalf("expected 'untitled' for empty, got %q", got)
	}
}

func TestSafeName_PreservesSpaces(t *testing.T) {
	got := SafeName("a knight on a white horse")
	if got != "a knight on a white horse" {
		t.Fatalf("expected spaces preserved, got %q", got)
	}
}

func TestSafeName_SpecialCharsToSpaces(t *testing.T) {
	got := SafeName("test@#$%^&*()folder")
	if got != "test folder" {
		t.Fatalf("expected 'test folder', got %q", got)
	}
}
