package adapters

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// bandedGrayPNG paints alternating vertical bands; rowBandedGrayPNG paints
// alternating horizontal bands. The two are orthogonal at the dHash grid,
// so they must not collide.
func bandedGrayPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		value := uint8(40)
		if (x*9/width)%2 == 0 {
			value = 200
		}
		for y := 0; y < height; y++ {
			img.SetGray(x, y, color.Gray{Y: value})
		}
	}
	return encodeGrayPNG(t, img)
}

func rowBandedGrayPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		value := uint8(40)
		if (y*8/height)%2 == 0 {
			value = 200
		}
		for x := 0; x < width; x++ {
			img.SetGray(x, y, color.Gray{Y: value})
		}
	}
	return encodeGrayPNG(t, img)
}

func encodeGrayPNG(t *testing.T, img *image.Gray) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestVerifyVidRushImageBytesStampsPerceptualHash(t *testing.T) {
	data := bandedGrayPNG(t, 640, 360)
	verified, err := VerifyVidRushImageBytes(scriptpkg.SegmentAssetCandidate{
		AssetID: "perceptual-1", Provider: scriptpkg.VidRushProviderInternetImages, RightsStatus: "verified",
	}, data, "image/png", DefaultVidRushImagePolicy())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !digest.IsPerceptualHash(verified.Candidate.PerceptualHash) {
		t.Fatalf("PerceptualHash = %q, want a well-formed dhash64", verified.Candidate.PerceptualHash)
	}

	again, err := VerifyVidRushImageBytes(scriptpkg.SegmentAssetCandidate{
		AssetID: "perceptual-2", Provider: scriptpkg.VidRushProviderInternetImages, RightsStatus: "verified",
	}, data, "image/png", DefaultVidRushImagePolicy())
	if err != nil {
		t.Fatalf("verify (repeat): %v", err)
	}
	if again.Candidate.PerceptualHash != verified.Candidate.PerceptualHash {
		t.Fatalf("identical bytes produced different perceptual hashes: %q vs %q",
			verified.Candidate.PerceptualHash, again.Candidate.PerceptualHash)
	}

	other, err := VerifyVidRushImageBytes(scriptpkg.SegmentAssetCandidate{
		AssetID: "perceptual-3", Provider: scriptpkg.VidRushProviderInternetImages, RightsStatus: "verified",
	}, rowBandedGrayPNG(t, 640, 360), "image/png", DefaultVidRushImagePolicy())
	if err != nil {
		t.Fatalf("verify (other): %v", err)
	}
	if other.Candidate.PerceptualHash == verified.Candidate.PerceptualHash {
		t.Fatalf("orthogonal pattern produced the same perceptual hash %q", other.Candidate.PerceptualHash)
	}
}
