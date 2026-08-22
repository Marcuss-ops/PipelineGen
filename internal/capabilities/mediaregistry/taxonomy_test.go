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

// TestResolveTaxonomy_DerivesKindForEveryMediaType pins that the resolver is
// the single decision point for provider + media_type → asset_kind across the
// whole media space (video, image, audio, text, document).
func TestResolveTaxonomy_DerivesKindForEveryMediaType(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		media    MediaType
		wantKind AssetKind
	}{
		{"youtube video", "youtube", MediaVideo, AssetClip},
		{"artlist video", "artlist", MediaVideo, AssetStockVideo},
		{"stock video", "stock", MediaVideo, AssetStockVideo},
		{"image", "image", MediaImage, AssetWebImage},
		{"voiceover audio", "voiceover", MediaAudio, AssetVoiceover},
		{"bgm audio", "bgm", MediaAudio, AssetBGM},
		{"sfx audio", "sfx", MediaAudio, AssetSFX},
		{"sound_effect audio", "sound_effect", MediaAudio, AssetSFX},
		{"generic audio", "drive", MediaAudio, AssetClipAudio},
		{"text", "created", MediaText, AssetMetadata},
		{"document", "document", MediaDocument, AssetDocument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveTaxonomy(TaxonomyInput{AssetID: "a", Provider: tc.provider, MediaType: tc.media})
			if err != nil {
				t.Fatalf("ResolveTaxonomy: %v", err)
			}
			if got.AssetKind != tc.wantKind {
				t.Fatalf("AssetKind = %q, want %q (full taxonomy: %+v)", got.AssetKind, tc.wantKind, got)
			}
		})
	}
}
