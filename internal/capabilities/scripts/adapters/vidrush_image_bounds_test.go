package adapters

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestCanonicalizeVidRushImageFileRejectsDeclaredPixelBomb(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bomb.png")
	if err := os.WriteFile(path, pngWithDeclaredDimensionsForBounds(t, 30000, 30000), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	err := CanonicalizeVidRushImageFile(path, DefaultVidRushImagePolicy())
	if !errors.Is(err, ErrVidRushImageTooLarge) {
		t.Fatalf("CanonicalizeVidRushImageFile error = %v, want ErrVidRushImageTooLarge", err)
	}
}

func TestVidRushImagePolicyAcceptsPortraitAndRejectsBanner(t *testing.T) {
	policy := DefaultVidRushImagePolicy()
	cases := []struct {
		name          string
		width, height int
		want          bool
	}{
		{name: "landscape", width: 640, height: 360, want: true},
		{name: "portrait", width: 600, height: 900, want: true},
		{name: "vertical", width: 360, height: 640, want: true},
		{name: "banner", width: 1200, height: 300, want: false},
		{name: "wide_short", width: 900, height: 300, want: false},
		{name: "thumbnail", width: 320, height: 240, want: false},
		{name: "strip", width: 1000, height: 200, want: false},
		{name: "invalid", width: 0, height: 0, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.AcceptsDimensions(tc.width, tc.height); got != tc.want {
				t.Fatalf("AcceptsDimensions(%d, %d) = %t, want %t", tc.width, tc.height, got, tc.want)
			}
		})
	}
}

func TestVerifyVidRushImageBytesAcceptsPortrait(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 600, 900))
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	verified, err := VerifyVidRushImageBytes(scriptpkg.SegmentAssetCandidate{
		AssetID: "portrait", Provider: scriptpkg.VidRushProviderInternetImages,
	}, buffer.Bytes(), "image/png", DefaultVidRushImagePolicy())
	if err != nil {
		t.Fatalf("portrait rejected: %v", err)
	}
	if verified.Width != 600 || verified.Height != 900 {
		t.Fatalf("verified dimensions = %dx%d, want 600x900", verified.Width, verified.Height)
	}
}

func TestVerifyVidRushImageBytesRejectsBanner(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1200, 300))
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	if _, err := VerifyVidRushImageBytes(scriptpkg.SegmentAssetCandidate{
		AssetID: "banner", Provider: scriptpkg.VidRushProviderInternetImages,
	}, buffer.Bytes(), "image/png", DefaultVidRushImagePolicy()); !errors.Is(err, ErrVidRushImageInvalid) {
		t.Fatalf("banner error = %v, want ErrVidRushImageInvalid", err)
	}
}

// pngWithDeclaredDimensionsForBounds builds a valid PNG header that advertises
// a huge size while the compressed payload stays one pixel.
func pngWithDeclaredDimensionsForBounds(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	data := buffer.Bytes()
	if len(data) < 33 {
		t.Fatalf("PNG fixture is too short: %d bytes", len(data))
	}
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}
