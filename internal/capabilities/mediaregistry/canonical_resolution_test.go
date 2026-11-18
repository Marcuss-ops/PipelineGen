package mediaregistry

import (
	"errors"
	"testing"
)

func TestResolveSourceObservationsClassifiesDurableRows(t *testing.T) {
	if _, err := ResolveSourceObservations("youtube", "vid-1", nil); !errors.Is(err, ErrCanonicalIdentityNotFound) {
		t.Fatalf("empty observations: err = %v, want ErrCanonicalIdentityNotFound", err)
	}

	// Two rows for the SAME asset are re-discoveries, not ambiguity; the last
	// observation in adapter order wins (is_primary DESC, discovered_at ASC).
	got, err := ResolveSourceObservations("youtube", "vid-1", []SourceObservation{
		{AssetID: "asset-a", ContentSHA256: "sha-old"},
		{AssetID: "asset-a", ContentSHA256: "sha-new"},
	})
	if err != nil {
		t.Fatalf("same-asset observations: %v", err)
	}
	if got.AssetID != "asset-a" || got.ContentSHA256 != "sha-new" || got.SourceType != "youtube" || got.SourceRef != "vid-1" {
		t.Fatalf("unexpected identity: %+v", got)
	}

	if _, err := ResolveSourceObservations("youtube", "vid-1", []SourceObservation{
		{AssetID: "asset-a"},
		{AssetID: "asset-b"},
	}); !errors.Is(err, ErrCanonicalIdentityAmbiguous) {
		t.Fatalf("two assets: err = %v, want ErrCanonicalIdentityAmbiguous", err)
	}
}

func TestResolveContentObservationsKeepsMultiProvenanceAnonymous(t *testing.T) {
	if _, err := ResolveContentObservations("sha-1", nil); !errors.Is(err, ErrCanonicalIdentityNotFound) {
		t.Fatalf("empty assets: err = %v, want ErrCanonicalIdentityNotFound", err)
	}

	got, err := ResolveContentObservations("sha-1", []string{"asset-a"})
	if err != nil {
		t.Fatalf("single asset: %v", err)
	}
	if got.AssetID != "asset-a" || got.ContentSHA256 != "sha-1" {
		t.Fatalf("unexpected identity: %+v", got)
	}

	multi, err := ResolveContentObservations("sha-1", []string{"asset-a", "asset-b"})
	if err != nil {
		t.Fatalf("multi provenance: %v", err)
	}
	if multi.AssetID != "" {
		t.Fatalf("multi-provenance content must not pick an owner, got %q", multi.AssetID)
	}
	if multi.ContentSHA256 != "sha-1" {
		t.Fatalf("content hash must be preserved, got %+v", multi)
	}
}

func TestCanonicalQueryValidationFailsClosed(t *testing.T) {
	if _, _, err := NormalizeSourceQuery("  ", "vid-1"); !errors.Is(err, ErrAssetSourceInvalid) {
		t.Fatalf("empty source type: err = %v, want ErrAssetSourceInvalid", err)
	}
	if _, _, err := NormalizeSourceQuery("youtube", " "); !errors.Is(err, ErrAssetSourceInvalid) {
		t.Fatalf("empty source ref: err = %v, want ErrAssetSourceInvalid", err)
	}
	sourceType, sourceRef, err := NormalizeSourceQuery(" youtube ", " vid-1 ")
	if err != nil || sourceType != "youtube" || sourceRef != "vid-1" {
		t.Fatalf("trim: (%q, %q, %v)", sourceType, sourceRef, err)
	}
	if _, err := NormalizeContentQuery(" "); !errors.Is(err, ErrContentObjectInvalid) {
		t.Fatalf("empty content: err = %v, want ErrContentObjectInvalid", err)
	}
	if got, err := NormalizeContentQuery(" sha-1 "); err != nil || got != "sha-1" {
		t.Fatalf("trim content: (%q, %v)", got, err)
	}
}
