package termutil

import (
	"reflect"
	"testing"
)

func TestExtractLikelyNames(t *testing.T) {
	got := ExtractLikelyNames("John and Mary went to Paris. John saw Mary.")
	want := []string{"John", "Mary", "Paris"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractLikelyNames() = %v, want %v", got, want)
	}
}

func TestLooksLikePersonName(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"single capitalized", "John", true},
		{"full name", "John Smith", true},
		{"lowercase", "john", false},
		{"four words capitalized", "John Jacob Jingleheimer Schmidt", true},
		{"five words", "John Jacob Jingleheimer Schmidt Extra", false},
		{"mixed case sentence", "The Quick Brown Fox", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LooksLikePersonName(tt.text)
			if got != tt.want {
				t.Errorf("LooksLikePersonName(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
