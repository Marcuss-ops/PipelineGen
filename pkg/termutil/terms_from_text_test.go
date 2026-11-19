package termutil

import (
	"reflect"
	"testing"
)

func TestTermsFromText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		opts     TermOptions
		expected []string
	}{
		{"empty text", "", TermOptions{}, nil},
		{"basic tokens", "hello world", TermOptions{MinLen: 1, Lowercase: true, RemoveStops: false, Unique: false}, []string{"hello", "world"}},
		{"min length filter", "a big cat", TermOptions{MinLen: 3, Lowercase: true, RemoveStops: false, Unique: false}, []string{"big", "cat"}},
		{"unique", "hello hello world", TermOptions{MinLen: 1, Lowercase: true, RemoveStops: false, Unique: true}, []string{"hello", "world"}},
		{"limit", "one two three four", TermOptions{MinLen: 1, Lowercase: true, RemoveStops: false, Unique: false, Limit: 2}, []string{"one", "two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TermsFromText(tt.text, tt.opts)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("TermsFromText(%q, %+v) = %v, want %v", tt.text, tt.opts, got, tt.expected)
			}
		})
	}
}
