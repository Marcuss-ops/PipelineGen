package vidrush

import (
	"context"
	"strings"
	"testing"
)

func TestVidRushImageRequestAdvertisesOnlyDecodableFormats(t *testing.T) {
	req, err := newVidRushImageRequest(context.Background(), "https://example.com/image.jpg", "https://example.com/article")
	if err != nil {
		t.Fatalf("newVidRushImageRequest: %v", err)
	}
	accept := strings.ToLower(req.Header.Get("Accept"))
	if strings.Contains(accept, "avif") {
		t.Fatalf("Accept advertises AVIF, which the verifier cannot decode: %q", accept)
	}
	if strings.Contains(accept, "svg") {
		t.Fatalf("Accept advertises SVG, which is not a supported raster image: %q", accept)
	}
	for _, mime := range []string{"image/webp", "image/png", "image/jpeg", "image/gif"} {
		if !strings.Contains(accept, mime) {
			t.Fatalf("Accept %q does not include decodable format %s", accept, mime)
		}
	}
}
