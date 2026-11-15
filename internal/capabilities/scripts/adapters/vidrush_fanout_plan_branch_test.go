package adapters

import "testing"

func TestImageFanoutLanePredicates(t *testing.T) {
	cases := []struct {
		name                        string
		perScene, automatic, manual bool
		entity, contextual          bool
	}{
		{name: "automatic contextual", automatic: true, contextual: true},
		{name: "automatic entity", perScene: true, automatic: true, entity: true},
		{name: "manual exclusive", automatic: true, manual: true},
		{name: "disabled automatic", perScene: true},
		{name: "entity surface disabled", entity: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := entityImageLaneRequested(tc.perScene, tc.automatic, tc.manual); got != tc.entity {
				t.Fatalf("entity lane = %v, want %v", got, tc.entity)
			}
			if got := contextualImageLaneRequested(tc.perScene, tc.automatic, tc.manual); got != tc.contextual {
				t.Fatalf("contextual lane = %v, want %v", got, tc.contextual)
			}
		})
	}
}
