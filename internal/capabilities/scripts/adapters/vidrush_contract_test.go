package adapters

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushImageHeadPreflightTreatsMimeAsAdvisoryAndRejectsOversize(t *testing.T) {
	policy := DefaultVidRushImagePolicy()
	if err := validateVidRushImageHead(http.StatusOK, "text/html; charset=utf-8", 1024, policy); err != nil {
		t.Fatalf("misreported HEAD MIME should be advisory: %v", err)
	}
	if err := validateVidRushImageHead(http.StatusOK, "image/jpeg", policy.MaxBytes+1, policy); !errors.Is(err, ErrVidRushImageTooLarge) {
		t.Fatalf("oversized HEAD response error = %v, want too large", err)
	}
	if err := validateVidRushImageHead(http.StatusMethodNotAllowed, "", -1, policy); err != nil {
		t.Fatalf("unsupported HEAD should fall back to guarded GET: %v", err)
	}
	if err := validateVidRushImageHead(http.StatusForbidden, "", -1, policy); err != nil {
		t.Fatalf("403 HEAD should fall back to guarded GET: %v", err)
	}
	if err := validateVidRushImageHead(http.StatusOK, "image/jpeg", 1024, policy); err != nil {
		t.Fatalf("valid image HEAD response rejected: %v", err)
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
	if verified.ContentSHA256 == "" || verified.Width != 640 || verified.Height != 360 {
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
		DriveLink: "https://drive.google.com/file/d/asset-1/view", ContentSHA256: "hash",
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

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }
