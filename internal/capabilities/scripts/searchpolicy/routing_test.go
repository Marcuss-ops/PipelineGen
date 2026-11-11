package searchpolicy

import "testing"

func TestIdentityKindsSelectProviderLane(t *testing.T) {
	cases := []struct {
		kind string
		want Class
		lane string
	}{
		{"PERSON", Person, "duckduckgo"},
		{"ORG", Organization, "wikimedia_commons"},
		{"LOCATION", Location, "wikimedia_commons"},
		{"PRODUCT", Object, "wikimedia_commons"},
		{"EVENT", Event, "generic"},
		{"unknown", Generic, "generic"},
	}
	for _, tc := range cases {
		got := ForIdentityKind(tc.kind)
		if got != tc.want || got.ProviderLane() != tc.lane {
			t.Errorf("ForIdentityKind(%q) = %q/%q, want %q/%q", tc.kind, got, got.ProviderLane(), tc.want, tc.lane)
		}
	}
}

func TestQueryFallbackIsConservative(t *testing.T) {
	if got := FromQuery("Corte d'Appello di Brescia"); got != Organization {
		t.Fatalf("institutional query = %q, want %q", got, Organization)
	}
	if got := FromQuery("a forest at night"); got != Generic {
		t.Fatalf("free-text query = %q, want %q", got, Generic)
	}
}
