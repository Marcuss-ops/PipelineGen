package mediaregistry

import "testing"

func TestAssetTaxonomyRejectsCrossDomainKind(t *testing.T) {
	if err := (AssetTaxonomy{AssetID: "a", Namespace: "audio", MediaType: MediaAudio, AssetKind: AssetClip, SourceType: "drive"}).Validate(); err == nil {
		t.Fatal("expected audio/clip taxonomy to fail")
	}
}

func TestAssetTaxonomyAcceptsFinalAudio(t *testing.T) {
	if err := (AssetTaxonomy{AssetID: "a", Namespace: "outputs", MediaType: MediaAudio, AssetKind: AssetFinalAudio, SourceType: "pipelinegen"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTaxonomy_YouTubeClipDefaults(t *testing.T) {
	got, err := ResolveTaxonomy(TaxonomyInput{AssetID: "yt-1", Provider: "youtube"})
	if err != nil {
		t.Fatal(err)
	}
	want := AssetTaxonomy{
		AssetID:      "yt-1",
		Namespace:    "youtube",
		MediaType:    MediaVideo,
		AssetKind:    AssetClip,
		SourceType:   "youtube",
		SemanticRole: "discovery",
	}
	if got != want {
		t.Fatalf("ResolveTaxonomy = %+v, want %+v", got, want)
	}
}

func TestResolveTaxonomy_StockVideoDerivesStockVideoKind(t *testing.T) {
	got, err := ResolveTaxonomy(TaxonomyInput{AssetID: "s-1", Provider: "artlist"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AssetKind != AssetStockVideo || got.MediaType != MediaVideo || got.SourceType != "artlist" {
		t.Fatalf("unexpected stock taxonomy: %+v", got)
	}
}

func TestResolveTaxonomy_ImageDerivesImagesNamespaceAndVisualRole(t *testing.T) {
	got, err := ResolveTaxonomy(TaxonomyInput{AssetID: "i-1", Provider: "image", MediaType: MediaImage})
	if err != nil {
		t.Fatal(err)
	}
	if got.Namespace != "images" || got.SemanticRole != "visual" || got.AssetKind != AssetWebImage {
		t.Fatalf("unexpected image taxonomy: %+v", got)
	}
}

func TestResolveTaxonomy_ExplicitOverridesWin(t *testing.T) {
	got, err := ResolveTaxonomy(TaxonomyInput{
		AssetID:      "i-2",
		Provider:     "image",
		MediaType:    MediaImage,
		AssetKind:    AssetAIImage,
		Namespace:    "generated",
		SemanticRole: "visual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.AssetKind != AssetAIImage || got.Namespace != "generated" {
		t.Fatalf("overrides not honored: %+v", got)
	}
}

func TestResolveTaxonomy_RejectsEmptyIdentity(t *testing.T) {
	if _, err := ResolveTaxonomy(TaxonomyInput{Provider: "youtube"}); err == nil {
		t.Fatal("expected error for empty asset id")
	}
}

func TestResolveTaxonomy_RejectsCrossDomainKind(t *testing.T) {
	if _, err := ResolveTaxonomy(TaxonomyInput{AssetID: "a", Provider: "drive", MediaType: MediaAudio, AssetKind: AssetClip}); err == nil {
		t.Fatal("expected error for audio/clip cross-domain kind")
	}
}
