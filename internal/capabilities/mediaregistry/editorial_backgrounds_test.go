package mediaregistry

import (
	"reflect"
	"strings"
	"testing"
)

func TestEditorialBackgroundCatalogIsCanonicalAndCollisionFree(t *testing.T) {
	if err := ValidateEditorialBackgroundCatalog(); err != nil {
		t.Fatal(err)
	}
	assets := EditorialBackgroundAssets()
	if len(assets) != 11 {
		t.Fatalf("editorial background assets = %d, want 11 canonical plates", len(assets))
	}
	want := []string{
		"drive-background-01", "drive-background-02", "drive-background-03",
		"drive-background-04", "drive-background-05", "drive-background-06",
		"drive-background-boxe", "drive-background-crime", "drive-background-music",
		"drive-background-wwe", "drive-background-discovery",
	}
	if got := EditorialBackgroundIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("background ids = %v, want %v", got, want)
	}
	for _, asset := range assets {
		if asset.MediaType != "video/mp4" || !strings.HasSuffix(asset.Filename, ".mp4") {
			t.Errorf("%s: media_type/file must stay video/mp4 (got %q/%q)", asset.ID, asset.MediaType, asset.Filename)
		}
		if asset.Role == "" {
			t.Errorf("%s: role is required", asset.ID)
		}
		if _, ok := LookupEditorialBackground(strings.ToUpper(asset.ID)); !ok {
			t.Errorf("lookup is not case-insensitive for %q", asset.ID)
		}
	}
	if _, ok := LookupEditorialBackground("drive-background-99"); ok {
		t.Error("unknown id must not resolve")
	}
}

func TestResolveEditorialBackgroundReferenceAcceptsChannelLabels(t *testing.T) {
	tests := map[string]string{
		"Boxe":                 "drive-background-boxe",
		"background: Crime":    "drive-background-crime",
		"background_music":     "drive-background-music",
		"background-wrestling": "drive-background-wwe",
		"DOCUMENTARY":          "drive-background-discovery",
	}
	for reference, wantID := range tests {
		asset, ok := ResolveEditorialBackgroundReference(reference)
		if !ok || asset.ID != wantID {
			t.Errorf("ResolveEditorialBackgroundReference(%q) = (%q, %v), want (%q, true)", reference, asset.ID, ok, wantID)
		}
	}
	if _, ok := ResolveEditorialBackgroundReference("not-a-background"); ok {
		t.Fatal("unknown background label must fail closed")
	}
}

func TestValidateEditorialBackgroundIdentity(t *testing.T) {
	plate, ok := LookupEditorialBackground("drive-background-01")
	if !ok {
		t.Fatal("drive-background-01 is missing from the registry")
	}

	// The certified normalized hash is accepted, case-insensitively.
	if err := ValidateEditorialBackgroundIdentity("drive-background-01", plate.SHA256); err != nil {
		t.Fatalf("certified hash rejected: %v", err)
	}
	if err := ValidateEditorialBackgroundIdentity("drive-background-01", strings.ToUpper(plate.SHA256)); err != nil {
		t.Fatalf("certified hash rejected when upper-cased: %v", err)
	}

	// A different content hash (for example the original Drive file bytes,
	// which carry an audio stream) must fail closed.
	if err := ValidateEditorialBackgroundIdentity("drive-background-01", strings.Repeat("a", 64)); err == nil {
		t.Fatal("a non-certified plate hash must fail closed")
	}
	if err := ValidateEditorialBackgroundIdentity("drive-background-01", ""); err == nil {
		t.Fatal("an empty plate hash must fail closed")
	}

	// A non-plate asset is not this check's business.
	if err := ValidateEditorialBackgroundIdentity("classic1", "anything"); err != nil {
		t.Fatalf("non-plate asset rejected: %v", err)
	}
	if err := ValidateEditorialBackgroundIdentity("clip_abc", ""); err != nil {
		t.Fatalf("non-plate asset rejected: %v", err)
	}
}

func TestEditorialBackgroundAssetsReturnsDefensiveCopy(t *testing.T) {
	assets := EditorialBackgroundAssets()
	assets[0].ID = "mutated"
	assets[0].DriveFileID = "mutated"
	fresh := EditorialBackgroundAssets()
	if fresh[0].ID == "mutated" || fresh[0].DriveFileID == "mutated" {
		t.Fatal("editorial background catalog leaked mutable state")
	}
}
