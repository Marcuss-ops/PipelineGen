// Package scriptgeneration — runner_map_level_test.go pins how the requested
// cartographic level reaches the geocoder. The resolved map_presentation is
// authoritative; the legacy map_level field is the fallback envelope fields
// use. Getting this wrong makes a country-level map geocode its cities.
package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRequestedMapLevelPrefersTheResolvedPresentation(t *testing.T) {
	cases := []struct {
		name string
		req  GenerateRequest
		want string
	}{
		{name: "no map surface", req: GenerateRequest{}},
		{name: "legacy field", req: GenerateRequest{MapLevel: "country"}, want: "country"},
		{
			name: "resolved presentation wins",
			req:  GenerateRequest{MapLevel: "municipality", MapPresentation: &scriptpkg.MapPresentation{Level: "country"}},
			want: "country",
		},
		{
			name: "empty presentation falls back",
			req:  GenerateRequest{MapLevel: "region", MapPresentation: &scriptpkg.MapPresentation{}},
			want: "region",
		},
		{
			name: "auto presentation is no level",
			req:  GenerateRequest{MapPresentation: &scriptpkg.MapPresentation{Level: "auto"}},
			want: "auto",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestedMapLevel(tc.req); got != tc.want {
				t.Fatalf("requestedMapLevel(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
