package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSearchClassForEntityQuery(t *testing.T) {
	entities := []scriptpkg.ExtractedEntity{
		{Value: "Steve Jobs", Type: "PERSON"},
		{Value: "NASA", Type: "ORG"},
		{Value: "Ginevra", Type: "GPE"},
		{Value: "iPhone", Type: "PRODUCT"},
		{Value: "Nike", Type: "LOGO"},
	}
	for _, tc := range []struct{ query, want string }{
		{"Steve Jobs", "person"}, {"NASA", "organization"}, {"NASA mission", "organization"},
		{"Ginevra", "location"}, {"iPhone", "object"}, {"untyped topic", ""},
		{"Nike", "brand"},
	} {
		if got := searchClassForEntityQuery(entities, tc.query); got != tc.want {
			t.Errorf("searchClassForEntityQuery(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}
