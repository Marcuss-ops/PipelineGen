package scriptgeneration

import (
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestSemanticAssetLogicalPathStableForURLAndMediaTypeInputs(t *testing.T) {
	cases := []struct {
		name string
		ref  capoverlay.OverlayAssetRef
		want string
	}{
		{"already logical", capoverlay.OverlayAssetRef{AssetID: "image-1", URL: "assets/custom/logo.png", MediaType: "image/png"}, "assets/custom/logo.png"},
		{"URL extension", capoverlay.OverlayAssetRef{AssetID: "remote id", URL: "https://cdn.invalid/picture.webp?x=1", MediaType: "image/png"}, "assets/semantic/remote_id.webp"},
		{"media type fallback", capoverlay.OverlayAssetRef{AssetID: "font-1", MediaType: "font/ttf; charset=binary"}, "assets/semantic/font-1.ttf"},
		{"unknown type", capoverlay.OverlayAssetRef{AssetID: "opaque"}, "assets/semantic/opaque"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := semanticAssetLogicalPath(tc.ref); got != tc.want {
				t.Fatalf("logical path = %q, want %q", got, tc.want)
			}
		})
	}
}
