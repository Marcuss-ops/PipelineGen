package vidrush

import "testing"

func TestImageFanoutLanePredicates(t *testing.T) {
	cases := []struct {
		name                        string
		perScene, automatic, manual bool
		search                      bool
		entity, contextual          bool
	}{
		{name: "automatic contextual", automatic: true, contextual: true},
		{name: "automatic entity", perScene: true, automatic: true, search: true, entity: true},
		{name: "manual exclusive entity", perScene: true, automatic: true, manual: true, search: true, entity: true},
		{name: "explicit_only entity", perScene: true, manual: true, search: true, entity: true},
		{name: "disabled automatic", perScene: true},
		{name: "entity surface disabled", search: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := entityImageLaneRequested(tc.perScene, tc.search); got != tc.entity {
				t.Fatalf("entity lane = %v, want %v", got, tc.entity)
			}
			if got := contextualImageLaneRequested(tc.perScene, tc.automatic, tc.manual); got != tc.contextual {
				t.Fatalf("contextual lane = %v, want %v", got, tc.contextual)
			}
		})
	}
}
