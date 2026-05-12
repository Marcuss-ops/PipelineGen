package association

import (
	"testing"
)

func TestPrimaryFocus(t *testing.T) {
	tests := []struct {
		name     string
		topic    string
		subject  string
		entities []string
		expected string
	}{
		{
			name:     "Full title with subtitle",
			topic:    "Mike Tyson: The Baddest Man on the Planet",
			subject:  "Mike Tyson: The Baddest Man on the Planet",
			entities: []string{"Mike Tyson"},
			expected: "Mike Tyson",
		},
		{
			name:     "Title with dash",
			topic:    "Boxing - The Sweet Science",
			subject:  "Boxing - The Sweet Science",
			entities: []string{"Boxing"},
			expected: "Boxing",
		},
		{
			name:     "Simple title",
			topic:    "Mike Tyson",
			subject:  "Mike Tyson",
			entities: []string{"Mike Tyson"},
			expected: "Mike Tyson",
		},
		{
			name:     "Subject with extra info, entity match",
			topic:    "Mike Tyson: The Baddest Man on the Planet",
			subject:  "Mike Tyson Training",
			entities: []string{"Mike Tyson", "Boxing"},
			expected: "Mike Tyson",
		},
		{
			name:     "Topic with entity match",
			topic:    "History of Muhammad Ali",
			subject:  "The Rumble in the Jungle",
			entities: []string{"Muhammad Ali"},
			expected: "Muhammad Ali",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PrimaryFocus(tt.topic, tt.subject, tt.entities)
			if got != tt.expected {
				t.Errorf("PrimaryFocus() = %v, want %v", got, tt.expected)
			}
		})
	}
}
