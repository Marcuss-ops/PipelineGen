// Package scriptgeneration — runner_overlay_map_intent_test.go pins the one
// map-intent predicate shared by geocoding enrichment and the person-name map
// rejection. The two call sites used to carry hand-copied boolean chains, so a
// new lane could enable one and silently skip the other.
package scriptgeneration

import "testing"

func TestRequestWantsMapsSinglePredicate(t *testing.T) {
	cases := []struct {
		name string
		req  GenerateRequest
		want bool
	}{
		{name: "no map surface", req: GenerateRequest{}},
		{name: "maps only", req: GenerateRequest{MapsOnly: true}, want: true},
		{name: "positive run budget", req: GenerateRequest{MaxMapOverlays: 2}, want: true},
		{name: "maps animation lane", req: GenerateRequest{AnimationCounts: map[string]int{"maps": 1}}, want: true},
		{name: "one_map animation lane", req: GenerateRequest{AnimationCounts: map[string]int{"one_map": 1}}, want: true},
		{name: "two_maps animation lane", req: GenerateRequest{AnimationCounts: map[string]int{"two_maps": 1}}, want: true},
		{name: "unrelated lane only", req: GenerateRequest{AnimationCounts: map[string]int{"images": 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestWantsMaps(tc.req); got != tc.want {
				t.Fatalf("requestWantsMaps(%s) = %t, want %t", tc.name, got, tc.want)
			}
		})
	}
}
