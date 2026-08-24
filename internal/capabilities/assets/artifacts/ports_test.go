package assets

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type testSourceRepo struct{}

func (testSourceRepo) Get(context.Context, string) (*asset.Asset, error) { return nil, nil }
func (testSourceRepo) GetByDriveFileID(context.Context, string) (*asset.Asset, error) {
	return nil, nil
}
func (testSourceRepo) Delete(context.Context, string) error { return nil }

func TestNewSourceCatalogRequiresFivePorts(t *testing.T) {
	if _, err := NewSourceCatalog(testSourceRepo{}); err == nil {
		t.Fatal("NewSourceCatalog accepted an incomplete registry")
	}
}

func TestNewSourceCatalogRegistersCanonicalSources(t *testing.T) {
	repo := testSourceRepo{}
	catalog, err := NewSourceCatalog(repo, repo, repo, repo, repo)
	if err != nil {
		t.Fatalf("NewSourceCatalog: %v", err)
	}
	if got := catalog.Normalize("sound_effects"); got != "sound_effect" {
		t.Fatalf("Normalize(sound-effects) = %q", got)
	}
	if resolved, ok := catalog.Resolve("sound_effects"); !ok || resolved == nil {
		t.Fatal("sound-effects did not resolve")
	}
	if got := catalog.Names(); len(got) != 6 {
		t.Fatalf("Names() len = %d, want 6", len(got))
	}
}

func TestDefaultMetadataMapsKnownMediaTypes(t *testing.T) {
	meta := defaultMetadata()
	if got := meta.AssetTypeForMediaType("photo"); got != "image" {
		t.Fatalf("photo type = %q", got)
	}
	if got := meta.MergeMetadataSearchText(" alpha ", "", "beta "); got != "alpha beta" {
		t.Fatalf("search text = %q", got)
	}
	encoded := meta.MetadataMapToJSON(meta.BuildAssetMetadata(MetadataInput{AssetID: "a", AssetType: "image"}, nil))
	if encoded == "{}" || meta.MetadataMapFromJSON(encoded)["asset_id"] != "a" {
		t.Fatalf("metadata round trip failed: %s", encoded)
	}
}
