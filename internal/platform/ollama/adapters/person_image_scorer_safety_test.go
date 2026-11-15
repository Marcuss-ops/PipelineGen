package adapters

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestOllamaPersonImageScorerMalformedVerdictIsRetryable(t *testing.T) {
	client := &personImageScorerFakeClient{responses: []string{"not-json"}}
	scorer := NewOllamaPersonImageScorer(client)
	scorer.download = func(context.Context, scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
		return testPersonScorerJPEG(t), "image/jpeg", nil
	}
	candidates := []scriptpkg.SegmentAssetCandidate{{
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Entity:    "Chiara Poggi",
		SourceURL: "https://images.example/portrait.jpg",
	}}
	got, err := scorer.ScoreImageCandidates(context.Background(), "Chiara Poggi", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SemanticStatus != "unknown" {
		t.Fatalf("malformed verdict status = %q, want unknown (retryable)", got[0].SemanticStatus)
	}
	if !strings.Contains(got[0].QualityReason, "malformed") || !strings.Contains(got[0].QualityReason, "retryable") {
		t.Fatalf("malformed verdict reason = %q", got[0].QualityReason)
	}
}

func TestOllamaPersonImageScorerAcceptsContextualIdentityMatch(t *testing.T) {
	client := &personImageScorerFakeClient{responses: []string{
		`{"identity_match":true,"single_person_headshot":false,"split_panel_or_collage":false,"confidence":0.91}`,
	}}
	scorer := NewOllamaPersonImageScorer(client)
	scorer.download = func(context.Context, scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
		return testPersonScorerJPEG(t), "image/jpeg", nil
	}
	candidates := []scriptpkg.SegmentAssetCandidate{{
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Entity:    "Chiara Poggi",
		SourceURL: "https://images.example/contextual.jpg",
	}}
	got, err := scorer.ScoreImageCandidates(context.Background(), "Chiara Poggi", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SemanticStatus != "accepted" {
		t.Fatalf("contextual identity match status = %q, want accepted", got[0].SemanticStatus)
	}
	if !strings.Contains(got[0].QualityReason, "single_subject_layout=false") {
		t.Fatalf("layout was not reported separately: %q", got[0].QualityReason)
	}
}

func TestNormalizePersonImageForVisionResizes4K(t *testing.T) {
	source := testPersonScorerJPEGSize(t, 3840, 2160)
	got, err := normalizePersonImageForVision(source)
	if err != nil {
		t.Fatalf("normalize 4K image: %v", err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("decode normalized 4K image: %v", err)
	}
	if config.Width > maxVisionLongSide || config.Height > maxVisionLongSide {
		t.Fatalf("normalized 4K image = %dx%d, want long side <= %d", config.Width, config.Height, maxVisionLongSide)
	}
}

func TestNormalizePersonImageForVisionRejectsDeclaredDecompressionBomb(t *testing.T) {
	source := jpegWithDeclaredSize(t, testPersonScorerJPEG(t), 9000, 9000)
	if _, err := normalizePersonImageForVision(source); err == nil || !strings.Contains(err.Error(), "pixel budget") {
		t.Fatalf("decompression bomb error = %v, want a pixel-budget rejection", err)
	}
}

func testPersonScorerJPEGSize(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y += 8 {
		for x := 0; x < width; x += 8 {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jpegWithDeclaredSize patches the SOF0 dimensions of a valid JPEG so the
// decoder config reports a huge frame without allocating its pixels. It models
// the decompression-bomb input: the declared size must be rejected before the
// full decode.
func jpegWithDeclaredSize(t *testing.T, data []byte, width, height int) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	for i := 0; i+8 < len(out); i++ {
		if out[i] == 0xFF && out[i+1] == 0xC0 {
			out[i+5] = byte(height >> 8)
			out[i+6] = byte(height)
			out[i+7] = byte(width >> 8)
			out[i+8] = byte(width)
			return out
		}
	}
	t.Fatal("JPEG SOF0 marker not found")
	return nil
}
