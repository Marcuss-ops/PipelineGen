package detail

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImageOrigin_StableValues(t *testing.T) {
	cases := []struct {
		origin ImageOrigin
		want   string
	}{
		{ImageOriginRetrieved, "retrieved"},
		{ImageOriginGenerated, "generated"},
		{ImageOriginUploaded, "uploaded"},
	}
	for _, tc := range cases {
		if got := tc.origin.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
		if !tc.origin.IsValid() {
			t.Errorf("%q should be valid", tc.origin)
		}
	}
	for _, invalid := range []ImageOrigin{"", "unknown", "RETRIEVED", "retrieved "} {
		if invalid.IsValid() {
			t.Errorf("%q should be invalid", invalid)
		}
	}
}

func TestImageOrigin_JSONRoundTrip(t *testing.T) {
	type payload struct {
		Origin ImageOrigin `json:"origin"`
	}
	for _, origin := range []ImageOrigin{ImageOriginRetrieved, ImageOriginGenerated, ImageOriginUploaded} {
		encoded, err := json.Marshal(payload{Origin: origin})
		if err != nil {
			t.Fatal(err)
		}
		var decoded payload
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Origin != origin {
			t.Fatalf("round trip = %q, want %q", decoded.Origin, origin)
		}
	}
}

func TestImageProvider_StableValues(t *testing.T) {
	cases := []struct {
		provider ImageProvider
		want     string
	}{
		{ProviderWikipedia, "wikipedia"},
		{ProviderDuckDuckGo, "duckduckgo"},
		{ProviderSearXNG, "searxng"},
		{ProviderDrive, "drive"},
		{ProviderGoogleSlides, "google-slides"},
		{ProviderNVIDIA, "nvidia"},
		{ProviderFlux, "flux"},
		{ProviderUpload, "upload"},
		{ProviderUnknown, "unknown"},
	}
	for _, tc := range cases {
		if got := tc.provider.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
		if !tc.provider.IsValid() {
			t.Errorf("%q should be valid", tc.provider)
		}
	}
	for _, invalid := range []ImageProvider{"", "midjourney", "WIKIPEDIA", "unknown "} {
		if invalid.IsValid() {
			t.Errorf("%q should be invalid", invalid)
		}
	}
}

func TestImageProvider_Territories(t *testing.T) {
	if !ProviderGoogleSlides.IsGenerated() || ProviderGoogleSlides.IsRetrieved() {
		t.Fatal("google-slides must be generated-only")
	}
	for _, provider := range []ImageProvider{ProviderNVIDIA, ProviderFlux} {
		if !provider.IsGenerated() || provider.IsRetrieved() {
			t.Errorf("%q must be generated-only", provider)
		}
	}
	for _, provider := range []ImageProvider{ProviderWikipedia, ProviderDuckDuckGo, ProviderSearXNG, ProviderDrive} {
		if !provider.IsRetrieved() || provider.IsGenerated() {
			t.Errorf("%q must be retrieved-only", provider)
		}
	}
	for _, provider := range []ImageProvider{ProviderUpload, ProviderUnknown, ""} {
		if provider.IsRetrieved() || provider.IsGenerated() {
			t.Errorf("%q must have no territory", provider)
		}
	}
}

func TestImageProvider_JSONRoundTrip(t *testing.T) {
	type payload struct {
		Provider ImageProvider `json:"provider"`
	}
	providers := []ImageProvider{
		ProviderWikipedia, ProviderDuckDuckGo, ProviderSearXNG, ProviderDrive,
		ProviderGoogleSlides, ProviderNVIDIA, ProviderFlux, ProviderUpload, ProviderUnknown,
	}
	for _, provider := range providers {
		encoded, err := json.Marshal(payload{Provider: provider})
		if err != nil {
			t.Fatal(err)
		}
		var decoded payload
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Provider != provider {
			t.Fatalf("round trip = %q, want %q", decoded.Provider, provider)
		}
	}
}

func TestImageSearchTerritory_StableValues(t *testing.T) {
	cases := []struct {
		territory ImageSearchTerritory
		want      string
	}{
		{TerritoryRetrieved, "retrieved"},
		{TerritoryGenerated, "generated"},
		{TerritoryAll, "all"},
	}
	for _, tc := range cases {
		if tc.territory.String() != tc.want || !tc.territory.IsValid() {
			t.Errorf("invalid territory contract for %q", tc.territory)
		}
	}
}

func TestTypedEnums_JSONStrings(t *testing.T) {
	type payload struct {
		O ImageOrigin          `json:"o"`
		P ImageProvider        `json:"p"`
		T ImageSearchTerritory `json:"t"`
	}
	encoded, err := json.Marshal(payload{O: ImageOriginRetrieved, P: ProviderWikipedia, T: TerritoryAll})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"o":"retrieved"`, `"p":"wikipedia"`, `"t":"all"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("JSON %q missing %q", encoded, want)
		}
	}
}
