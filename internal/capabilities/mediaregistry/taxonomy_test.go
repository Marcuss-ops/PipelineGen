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
