// Package asset — image_enums_test.go
//
// Test coverage for the typed enums declared in image_taxonomy.go
// (ImageOrigin, ImageProvider) and image_enums.go (ImageSearchTerritory
// + String() methods). The tests verify:
//
//   1. Constant values are STABLE — they are persisted to SQLite
//      (`media_assets.origin`, `media_assets.provider`) and mapped to
//      API responses; changes are breaking.
//
//   2. String() returns the canonical value (re-hydration test).
//
//   3. IsValid() correctly classifies canonical vs unknown values,
//      with case-sensitivity intentionally enforced (provider IDs
//      are lower-case stable identifiers).
//
//   4. JSON round-trip — Marshal + Unmarshal preserves the typed
//      enum's identity (so handlers round-trip an ImageOrigin/ImageProvider
//      without losing type information).
//
//   5. JSON STABLE VALUES — the marshaled byte sequence is the
//      canonical string (no extra quoting, no escape characters).
package asset

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── ImageOrigin ────────────────────────────────────────────────────────

func TestImageOrigin_String_StableValues(t *testing.T) {
	cases := []struct {
		origin ImageOrigin
		want   string
	}{
		{ImageOriginRetrieved, "retrieved"},
		{ImageOriginGenerated, "generated"},
		{ImageOriginUploaded, "uploaded"},
	}
	for _, c := range cases {
		if got := c.origin.String(); got != c.want {
			t.Errorf("ImageOrigin(%q).String() = %q, want %q", c.origin, got, c.want)
		}
	}
}

func TestImageOrigin_IsValid(t *testing.T) {
	cases := []struct {
		origin ImageOrigin
		want   bool
	}{
		{ImageOriginRetrieved, true},
		{ImageOriginGenerated, true},
		{ImageOriginUploaded, true},
		{ImageOrigin(""), false},
		{ImageOrigin("unknown"), false},
		{ImageOrigin("retireved"), false},  // typo
		{ImageOrigin("RETRIEVED"), false},  // case-sensitive on purpose
		{ImageOrigin("retrieved "), false}, // trailing whitespace rejected
	}
	for _, c := range cases {
		if got := c.origin.IsValid(); got != c.want {
			t.Errorf("ImageOrigin(%q).IsValid() = %v, want %v", c.origin, got, c.want)
		}
	}
}

func TestImageOrigin_JSON_RoundTrip(t *testing.T) {
	type payload struct {
		Origin ImageOrigin `json:"origin"`
	}
	for _, origin := range []ImageOrigin{
		ImageOriginRetrieved, ImageOriginGenerated, ImageOriginUploaded,
	} {
		p := payload{Origin: origin}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal(%q): %v", origin, err)
		}
		var got payload
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal(%s): %v", data, err)
		}
		if got.Origin != origin {
			t.Errorf("round trip: origin = %q, want %q", got.Origin, origin)
		}
	}
}

func TestImageOrigin_JSON_StableValues(t *testing.T) {
	cases := []struct {
		origin ImageOrigin
		want   string
	}{
		{ImageOriginRetrieved, `{"origin":"retrieved"}`},
		{ImageOriginGenerated, `{"origin":"generated"}`},
		{ImageOriginUploaded, `{"origin":"uploaded"}`},
	}
	for _, c := range cases {
		type payload struct {
			Origin ImageOrigin `json:"origin"`
		}
		data, err := json.Marshal(payload{Origin: c.origin})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(data) != c.want {
			t.Errorf("JSON for %q = %s, want %s", c.origin, data, c.want)
		}
	}
}

// ── ImageProvider ──────────────────────────────────────────────────────

func TestImageProvider_String_StableValues(t *testing.T) {
	cases := []struct {
		provider ImageProvider
		want     string
	}{
		{ProviderWikipedia, "wikipedia"},
		{ProviderDuckDuckGo, "duckduckgo"},
		{ProviderSearXNG, "searxng"},
		{ProviderDrive, "drive"},
		{ProviderGoogleSlides, "google-slides"},
		{ProviderFlux, "flux"},
		{ProviderNvidia, "nvidia"},
		{ProviderUpload, "upload"},
		{ProviderUnknown, "unknown"},
	}
	for _, c := range cases {
		if got := c.provider.String(); got != c.want {
			t.Errorf("ImageProvider(%q).String() = %q, want %q", c.provider, got, c.want)
		}
	}
}

func TestImageProvider_IsValid(t *testing.T) {
	cases := []struct {
		provider ImageProvider
		want     bool
	}{
		{ProviderWikipedia, true},
		{ProviderDuckDuckGo, true},
		{ProviderSearXNG, true},
		{ProviderDrive, true},
		{ProviderGoogleSlides, true},
		{ProviderFlux, true},
		{ProviderNvidia, true},
		{ProviderUpload, true},
		{ProviderUnknown, true},
		{ImageProvider(""), false},
		{ImageProvider("midjourney"), false},
		{ImageProvider("WIKIPEDIA"), false}, // case-sensitive on purpose
		{ImageProvider("unknown "), false},
	}
	for _, c := range cases {
		if got := c.provider.IsValid(); got != c.want {
			t.Errorf("ImageProvider(%q).IsValid() = %v, want %v", c.provider, got, c.want)
		}
	}
}

func TestImageProvider_IsGeneratedAndIsRetrieved(t *testing.T) {
	// AI-generation territory.
	for _, p := range []ImageProvider{
		ProviderGoogleSlides, ProviderFlux, ProviderNvidia,
	} {
		if !p.IsGenerated() {
			t.Errorf("ImageProvider(%q).IsGenerated() = false, want true", p)
		}
		if p.IsRetrieved() {
			t.Errorf("ImageProvider(%q).IsRetrieved() = true, want false", p)
		}
	}
	// Web-retrieval territory.
	for _, p := range []ImageProvider{
		ProviderWikipedia, ProviderDuckDuckGo, ProviderSearXNG, ProviderDrive,
	} {
		if !p.IsRetrieved() {
			t.Errorf("ImageProvider(%q).IsRetrieved() = false, want true", p)
		}
		if p.IsGenerated() {
			t.Errorf("ImageProvider(%q).IsGenerated() = true, want false", p)
		}
	}
	// Generic / both false.
	for _, p := range []ImageProvider{
		ProviderUpload, ProviderUnknown, ImageProvider(""),
	} {
		if p.IsGenerated() {
			t.Errorf("ImageProvider(%q).IsGenerated() = true, want false (generic)", p)
		}
		if p.IsRetrieved() {
			t.Errorf("ImageProvider(%q).IsRetrieved() = true, want false (generic)", p)
		}
	}
}

func TestImageProvider_JSON_RoundTrip(t *testing.T) {
	type payload struct {
		Provider ImageProvider `json:"provider"`
	}
	for _, p := range []ImageProvider{
		ProviderWikipedia, ProviderDuckDuckGo, ProviderSearXNG,
		ProviderDrive, ProviderGoogleSlides, ProviderFlux, ProviderNvidia,
		ProviderUpload, ProviderUnknown,
	} {
		pl := payload{Provider: p}
		data, err := json.Marshal(pl)
		if err != nil {
			t.Fatalf("marshal(%q): %v", p, err)
		}
		var got payload
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal(%s): %v", data, err)
		}
		if got.Provider != p {
			t.Errorf("round trip: provider = %q, want %q", got.Provider, p)
		}
	}
}

func TestImageProvider_JSON_StableValues(t *testing.T) {
	// Pin the canonical JSON serialization for ALL canonical provider
	// values so a future drift (e.g. someone changes "google-slides" to
	// "googleSlides") is caught at test-time. Round-trip tests above
	// validate the cycle, this one pins the exact wire format.
	cases := []struct {
		provider ImageProvider
		want     string
	}{
		{ProviderWikipedia, `{"provider":"wikipedia"}`},
		{ProviderDuckDuckGo, `{"provider":"duckduckgo"}`},
		{ProviderSearXNG, `{"provider":"searxng"}`},
		{ProviderDrive, `{"provider":"drive"}`},
		{ProviderGoogleSlides, `{"provider":"google-slides"}`},
		{ProviderFlux, `{"provider":"flux"}`},
		{ProviderNvidia, `{"provider":"nvidia"}`},
		{ProviderUpload, `{"provider":"upload"}`},
		{ProviderUnknown, `{"provider":"unknown"}`},
	}
	type payload struct {
		Provider ImageProvider `json:"provider"`
	}
	for _, c := range cases {
		data, err := json.Marshal(payload{Provider: c.provider})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(data) != c.want {
			t.Errorf("JSON for %q = %s, want %s", c.provider, data, c.want)
		}
	}
}

// ── ImageSearchTerritory ───────────────────────────────────────────────

func TestImageSearchTerritory_String_StableValues(t *testing.T) {
	cases := []struct {
		t    ImageSearchTerritory
		want string
	}{
		{TerritoryRetrieved, "retrieved"},
		{TerritoryGenerated, "generated"},
		{TerritoryAll, "all"},
	}
	for _, c := range cases {
		if got := c.t.String(); got != c.want {
			t.Errorf("ImageSearchTerritory(%q).String() = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestImageSearchTerritory_IsValid(t *testing.T) {
	cases := []struct {
		t    ImageSearchTerritory
		want bool
	}{
		{TerritoryRetrieved, true},
		{TerritoryGenerated, true},
		{TerritoryAll, true},
		{ImageSearchTerritory(""), false},
		{ImageSearchTerritory("both"), false},
		{ImageSearchTerritory("RETRIEVED"), false},
		{ImageSearchTerritory("retrieved "), false},
	}
	for _, c := range cases {
		if got := c.t.IsValid(); got != c.want {
			t.Errorf("ImageSearchTerritory(%q).IsValid() = %v, want %v", c.t, got, c.want)
		}
	}
}

func TestImageSearchTerritory_JSON_RoundTrip(t *testing.T) {
	type payload struct {
		Territory ImageSearchTerritory `json:"territory"`
	}
	for _, terr := range []ImageSearchTerritory{
		TerritoryRetrieved, TerritoryGenerated, TerritoryAll,
	} {
		p := payload{Territory: terr}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal(%q): %v", terr, err)
		}
		var got payload
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal(%s): %v", data, err)
		}
		if got.Territory != terr {
			t.Errorf("round trip: territory = %q, want %q", got.Territory, terr)
		}
	}
}

func TestImageSearchTerritory_JSON_StableValues(t *testing.T) {
	cases := []struct {
		terr ImageSearchTerritory
		want string
	}{
		{TerritoryRetrieved, `{"territory":"retrieved"}`},
		{TerritoryGenerated, `{"territory":"generated"}`},
		{TerritoryAll, `{"territory":"all"}`},
	}
	for _, c := range cases {
		type payload struct {
			Territory ImageSearchTerritory `json:"territory"`
		}
		data, err := json.Marshal(payload{Territory: c.terr})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(data) != c.want {
			t.Errorf("JSON for %q = %s, want %s", c.terr, data, c.want)
		}
	}
}

// ── Title stability: ensure the constants haven't drifted ──────────────

func TestTypedEnums_NoStringificationQuote(t *testing.T) {
	// JSON without explicit MarshalJSON must produce a plain string
	// without any wrapper markup. This pin-tests the canonical
	// serialization so external systems (proxy, gateway, Qdrant
	// payloads) don't have to special-case these types.
	type payload struct {
		O ImageOrigin           `json:"o"`
		P ImageProvider         `json:"p"`
		T ImageSearchTerritory  `json:"t"`
	}
	data, err := json.Marshal(payload{
		O: ImageOriginRetrieved,
		P: ProviderWikipedia,
		T: TerritoryAll,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(data)
	// Substring matching: we test the semantic content (each enum
	// field renders correctly) without coupling to the syntactic
	// structure. Struct field order is fixed by declaration, but
	// substring assertions are robust against future reordering of
	// the test or addition of fields.
	for _, want := range []string{
		`"o":"retrieved"`,
		`"p":"wikipedia"`,
		`"t":"all"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("JSON payload %q missing substring %q", raw, want)
		}
	}
}
