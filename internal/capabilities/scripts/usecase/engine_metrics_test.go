package usecase

import (
	"testing"
)

func TestExtractCountryForTelemetry_BCP47_Composite(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"it-IT", "it-IT", "IT"},
		{"en-US", "en-US", "US"},
		{"pt-BR", "pt-BR", "BR"},
		{"es-ES", "es-ES", "ES"},
		{"fr-FR", "fr-FR", "FR"},
		{"de-DE", "de-DE", "DE"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractCountryForTelemetry(tc.in); got != tc.want {
				t.Fatalf("ExtractCountryForTelemetry(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractCountryForTelemetry_Fallback(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"language only it", "it", "IT"},
		{"language only zh upper", "ZH", "ZH"},
		{"language only zh lower", "zh", "ZH"},
		{"empty sentinel", "", "XX"},
		{"whitespace sentinel", "   ", "XX"},
		{"bcp47 trim whitespace", "  en-US  ", "US"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractCountryForTelemetry(tc.in); got != tc.want {
				t.Fatalf("ExtractCountryForTelemetry(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
