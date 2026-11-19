package adapters

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func encodeCollageTestImage(t *testing.T, collage bool) []byte {
	t.Helper()
	const width, height = 900, 900
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if collage && ((y >= 300 && y < 324) || (y >= 624 && y < 648)) {
				img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
				continue
			}
			shade := uint8(20 + (x*160)/width)
			img.SetRGBA(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buffer.Bytes()
}

func verifyImageTestCandidate() scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{AssetID: "test-image", Provider: scriptpkg.VidRushProviderInternetImages}
}

// TestVerifyVidRushImageBytesRejectsPanelGridCollage pins that one asset which
// visibly contains three stacked pictures can never bind as a single photo:
// the viewer reads it as images piled on top of each other, and no motion or
// layout can split a source bitmap.
func TestVerifyVidRushImageBytesRejectsPanelGridCollage(t *testing.T) {
	_, err := VerifyVidRushImageBytes(verifyImageTestCandidate(), encodeCollageTestImage(t, true), "image/png", DefaultVidRushImagePolicy())
	if !errors.Is(err, ErrVidRushImageCollage) {
		t.Fatalf("expected ErrVidRushImageCollage, got %v", err)
	}
	if !errors.Is(err, ErrVidRushImageInvalid) {
		t.Fatal("collage rejection must remain a soft image-invalid rejection")
	}
}

func TestVerifyVidRushImageBytesAcceptsSinglePhoto(t *testing.T) {
	verified, err := VerifyVidRushImageBytes(verifyImageTestCandidate(), encodeCollageTestImage(t, false), "image/png", DefaultVidRushImagePolicy())
	if err != nil {
		t.Fatalf("single photo must verify: %v", err)
	}
	if verified.Candidate.PerceptualHash == "" {
		t.Fatal("verification must still stamp the perceptual hash")
	}
	if verified.Width != 900 || verified.Height != 900 {
		t.Fatalf("unexpected verified dimensions %dx%d", verified.Width, verified.Height)
	}
}
