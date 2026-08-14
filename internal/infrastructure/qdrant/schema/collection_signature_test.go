package schema

import (
	"strings"
	"testing"
)

const testContractHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCollectionSignature_PhysicalName_Deterministic(t *testing.T) {
	sig := CollectionSignature{
		SchemaVersion:           "v4",
		EmbeddingContractHash:   testContractHash,
		SemanticDocumentVersion: "v3",
		TextDimension:           768,
	}
	name, err := sig.PhysicalName()
	if err != nil {
		t.Fatalf("PhysicalName: %v", err)
	}
	want := "media_assets_v4_" + testContractHash + "_v3_768"
	if name != want {
		t.Fatalf("PhysicalName = %q, want %q", name, want)
	}
	again, _ := sig.PhysicalName()
	if again != name {
		t.Fatalf("PhysicalName is not deterministic: %q vs %q", name, again)
	}
}

func TestCollectionSignature_Validate_FailClosed(t *testing.T) {
	valid := CollectionSignature{
		SchemaVersion:           "v4",
		EmbeddingContractHash:   testContractHash,
		SemanticDocumentVersion: "v3",
		TextDimension:           768,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}

	missingSchema := valid
	missingSchema.SchemaVersion = ""
	if err := missingSchema.Validate(); err == nil {
		t.Fatal("empty schema version must fail validation")
	}

	badHash := valid
	badHash.EmbeddingContractHash = "md5-not-sha256"
	if err := badHash.Validate(); err == nil {
		t.Fatal("non-SHA-256 contract hash must fail validation")
	}

	missingSemdoc := valid
	missingSemdoc.SemanticDocumentVersion = ""
	if err := missingSemdoc.Validate(); err == nil {
		t.Fatal("empty semantic document version must fail validation")
	}

	zeroDim := valid
	zeroDim.TextDimension = 0
	if err := zeroDim.Validate(); err == nil {
		t.Fatal("zero dimension must fail validation")
	}
}

func TestCollectionSignature_Matches_RoundTrip(t *testing.T) {
	sig := CollectionSignature{
		SchemaVersion:           "v4",
		EmbeddingContractHash:   testContractHash,
		SemanticDocumentVersion: "v3",
		TextDimension:           768,
	}
	name, _ := sig.PhysicalName()
	if !sig.Matches(name) {
		t.Fatalf("signature must match its own physical name %q", name)
	}

	other := sig
	other.EmbeddingContractHash = strings.Repeat("f", 64)
	otherName, _ := other.PhysicalName()
	if sig.Matches(otherName) {
		t.Fatalf("signature must NOT match a different contract hash: %q", otherName)
	}

	if sig.Matches("media_assets_v3_nomic_768_siglip_768") {
		t.Fatal("legacy unsigned v3 name must not match the signed v4 signature")
	}
}

func TestParseCollectionSignature_RoundTrip(t *testing.T) {
	sig := CollectionSignature{
		SchemaVersion:           "v4",
		EmbeddingContractHash:   testContractHash,
		SemanticDocumentVersion: "v3",
		TextDimension:           768,
	}
	name, _ := sig.PhysicalName()
	parsed, ok := ParseCollectionSignature(name)
	if !ok {
		t.Fatalf("ParseCollectionSignature(%q) failed", name)
	}
	if parsed != sig {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", parsed, sig)
	}
}

func TestParseCollectionSignature_RejectsMalformed(t *testing.T) {
	for _, name := range []string{
		"",
		"media_assets_current",
		"media_assets_v4_" + testContractHash,
		"media_assets_v4_" + testContractHash + "_v3_notadim",
		"other_collection_v4_" + testContractHash + "_v3_768",
	} {
		if _, ok := ParseCollectionSignature(name); ok {
			t.Fatalf("ParseCollectionSignature(%q) unexpectedly succeeded", name)
		}
	}
}
