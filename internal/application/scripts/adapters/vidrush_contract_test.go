package adapters

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushImageValidationRejectsSSRFAndHTML(t *testing.T) {
	for _, raw := range []string{
		"http://localhost/image.jpg",
		"http://127.0.0.1/image.jpg",
		"http://169.254.169.254/latest/meta-data",
		"file:///tmp/image.jpg",
	} {
		if err := ValidateVidRushRemoteImageURL(raw); !errors.Is(err, ErrVidRushSSRFBlocked) {
			t.Errorf("ValidateVidRushRemoteImageURL(%q) = %v", raw, err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not an image</html>"))
	}))
	defer server.Close()
	_, _, err := DownloadVidRushImage(context.Background(), server.Client(), server.URL, DefaultVidRushImagePolicy())
	if !errors.Is(err, ErrVidRushSSRFBlocked) {
		t.Fatalf("loopback test server error = %v, want SSRF block", err)
	}
}

func TestVidRushImageRequestUsesBrowserHeadersAndOptionalSourcePage(t *testing.T) {
	req, err := newVidRushImageRequest(context.Background(), "https://cdn.example/image.jpg", "https://example.com/gallery")
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("User-Agent"); got != vidRushImageUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, vidRushImageUserAgent)
	}
	if got := req.Header.Get("Accept"); !strings.Contains(got, "image/") {
		t.Fatalf("Accept = %q, want image media types", got)
	}
	if got := req.Header.Get("Referer"); got != "https://example.com/gallery" {
		t.Fatalf("Referer = %q", got)
	}
}

func TestVerifyVidRushImageBytesAddsHashDimensionsAndLifecycle(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf strings.Builder
	if err := jpeg.Encode(stringWriter{&buf}, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	verified, err := VerifyVidRushImageBytes(scriptpkg.SegmentAssetCandidate{
		AssetID: "image-1", Provider: scriptpkg.VidRushProviderInternetImages,
		RightsStatus: "verified",
	}, []byte(buf.String()), "image/jpeg", DefaultVidRushImagePolicy())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verified.LegacyFileMD5 == "" || verified.Width != 640 || verified.Height != 360 {
		t.Fatalf("verified artifact = %+v", verified)
	}
	if verified.Candidate.AcquisitionStatus != scriptpkg.VidRushStatusAcquired || verified.Candidate.VerificationStatus != scriptpkg.VidRushStatusVerified {
		t.Fatalf("lifecycle = %+v", verified.Candidate)
	}
}

func TestVerifyVidRushImageFileCanonicalizesTransparentPNG(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "source-*.gif")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	img.Set(0, 0, color.RGBA{A: 0})
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyVidRushImageFile(scriptpkg.SegmentAssetCandidate{
		AssetID: "transparent-image", Provider: scriptpkg.VidRushProviderInternetImages,
		RightsStatus: "verified",
	}, file.Name(), DefaultVidRushImagePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if verified.MIMEType != "image/png" {
		t.Fatalf("canonical MIME = %q, want image/png", verified.MIMEType)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4 || string(data[:4]) != "\x89PNG" {
		t.Fatalf("canonical file does not contain PNG bytes")
	}
}

func TestVidRushLifecycleDoesNotBindUnpersistedOrUnverifiedRights(t *testing.T) {
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID: "asset-1", Provider: scriptpkg.VidRushProviderInternetImages,
		DriveLink: "https://drive.google.com/file/d/asset-1/view", LegacyFileMD5: "hash",
		AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
		IndexStatus:        scriptpkg.VidRushStatusIndexed,
		RightsStatus:       "unknown",
	}
	if candidate.ReadyForBinding() {
		t.Fatal("unknown rights candidate must not be binding-ready")
	}
	candidate.RightsStatus = "verified"
	if !candidate.ReadyForBinding() {
		t.Fatal("fully persisted verified candidate should be binding-ready")
	}
	candidate.IndexStatus = "DISCOVERED"
	if !candidate.ReadyForBinding() {
		t.Fatal("persisted verified candidate awaiting derived indexing should be binding-ready")
	}
	candidate.IndexStatus = scriptpkg.VidRushStatusFailed
	if candidate.ReadyForBinding() {
		t.Fatal("failed index candidate must not be binding-ready")
	}
}

func TestMissingVidRushImageCountUsesVerifiedWebImagesOnly(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{Assets: scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{
		{Provider: scriptpkg.VidRushProviderInternetImages, AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted, IndexStatus: scriptpkg.VidRushStatusIndexed, RightsStatus: "verified"},
		{Provider: scriptpkg.VidRushProviderInternetImages, RightsStatus: "verified"},
		{Provider: scriptpkg.VidRushProviderImageGeneration, RightsStatus: "verified"},
	}}}
	if got := MissingVidRushImageCount(segment, 2); got != 1 {
		t.Fatalf("missing images = %d, want 1", got)
	}
}

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }
