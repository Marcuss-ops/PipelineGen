package clipindexer

import "testing"

func TestIsSkippableAssetName_MetadataJsonIsIndexed(t *testing.T) {
	if isSkippableAssetName("metadata.json") {
		t.Fatalf("metadata.json must be indexed, not skipped")
	}
	if !isSkippableAssetName("captions.json") {
		t.Fatalf("generic JSON sidecars should still be skipped")
	}
}
