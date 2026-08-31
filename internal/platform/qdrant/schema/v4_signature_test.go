package schema

import (
	"strings"
	"testing"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

func TestCanonicalV4Signature_PhysicalName(t *testing.T) {
	sig := CanonicalV4Signature()
	if err := sig.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	name, err := sig.PhysicalName()
	if err != nil {
		t.Fatalf("PhysicalName: %v", err)
	}
	want := "media_assets_v4_" + coreembedding.CanonicalText.Hash() + "_" + coreembedding.SemanticDocumentVersionV3 + "_768"
	if name != want {
		t.Fatalf("PhysicalName = %q, want %q", name, want)
	}
	if len(coreembedding.CanonicalText.Hash()) != 64 {
		t.Fatalf("embedding contract hash must be 64 hex chars, got %d", len(coreembedding.CanonicalText.Hash()))
	}
}

func TestV4Signature_RoundTripParseAndMatch(t *testing.T) {
	sig := CanonicalV4Signature()
	name, err := sig.PhysicalName()
	if err != nil {
		t.Fatalf("PhysicalName: %v", err)
	}
	parsed, ok := ParseV4Signature(name)
	if !ok {
		t.Fatalf("ParseV4Signature(%q) failed", name)
	}
	if parsed != sig {
		t.Fatalf("round-trip mismatch: %+v vs %+v", parsed, sig)
	}
	if !sig.Matches(name) {
		t.Fatalf("Matches(%q) = false, want true", name)
	}
	if sig.Matches("media_assets_v3_" + strings.Repeat("a", 64) + "_v3_768") {
		t.Fatalf("Matches must reject a different schema version")
	}
}

func TestV4Signature_ValidateFailClosed(t *testing.T) {
	base := CanonicalV4Signature()

	cases := []struct {
		name   string
		mutate func(*V4Signature)
		want   string
	}{
		{"empty schema version", func(s *V4Signature) { s.SchemaVersion = "" }, "schema version"},
		{"bad hash", func(s *V4Signature) { s.EmbeddingContractHash = "not-a-hash" }, "64-hex SHA-256"},
		{"empty semantic doc version", func(s *V4Signature) { s.SemanticDocumentVersion = "" }, "semantic document version"},
		{"non-positive dimension", func(s *V4Signature) { s.TextDimension = 0 }, "text dimension"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.mutate(&s)
			err := s.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.want)
			}
			if _, err := s.PhysicalName(); err == nil {
				t.Fatal("PhysicalName on invalid signature must fail")
			}
		})
	}
}

func TestRuntimeCollectionPolicy_FailsClosedForRecoveryAndFixtures(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "production collection", want: true},
		{name: "legacy canonical physical", want: false},
		{name: "timestamped canonical", want: false},
		{name: "recovery collection", want: false},
		{name: "synthetic collection", want: false},
		{name: "test collection", want: false},
		{name: "runtime alias", want: false},
		{name: "empty", want: false},
	}
	names := map[string]string{
		"production collection":     ProductionCollection,
		"legacy canonical physical": "media_assets_v3_e5_768_siglip_768",
		"timestamped canonical":     "media_assets_v4_manual_20260831_120000_000000000",
		"recovery collection":       "media_assets_v4_recovery_20260817_1712",
		"synthetic collection":      "synthetic_assets_v3",
		"test collection":           "media_assets_v4_test",
		"runtime alias":             "media_assets_current",
		"empty":                     "",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRuntimeCollection(names[tc.name]); got != tc.want {
				t.Fatalf("IsRuntimeCollection(%q) = %v, want %v", names[tc.name], got, tc.want)
			}
		})
	}
}

func TestParseV4Signature_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"", "media_assets_v4", "other_v4_" + strings.Repeat("a", 64) + "_v3_768",
		"media_assets_v4_" + strings.Repeat("a", 64) + "_v3_notdim",
	} {
		if _, ok := ParseV4Signature(bad); ok {
			t.Fatalf("ParseV4Signature(%q) = ok, want false", bad)
		}
	}
}
