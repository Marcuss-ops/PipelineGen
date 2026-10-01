package digest

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// verticalBandPattern paints 9 alternating light/dark vertical bands of equal
// fraction, so the downsampled grid is non-monotonic and the dHash is neither
// all-zero nor all-one.
func verticalBandPattern(width, height int) *image.Gray {
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
	return img
}

// horizontalBandPattern paints 8 alternating light/dark horizontal bands, so
// every downsampled row is constant and the dHash collapses to zero. It is the
// orthogonal counterpart used to prove distinct images separate.
func horizontalBandPattern(width, height int) *image.Gray {
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
	return img
}

func TestPerceptualHash_DeterministicAndWellFormed(t *testing.T) {
	img := verticalBandPattern(64, 64)
	first, err := PerceptualHash(img)
	if err != nil {
		t.Fatalf("PerceptualHash: %v", err)
	}
	second, err := PerceptualHash(img)
	if err != nil {
		t.Fatalf("PerceptualHash (repeat): %v", err)
	}
	if first != second {
		t.Fatalf("hash is not deterministic: %q vs %q", first, second)
	}
	if !IsPerceptualHash(first) {
		t.Fatalf("IsPerceptualHash(%q) = false, want true", first)
	}
	if len(first) != len(PerceptualHashAlgorithm)+1+PerceptualHashHexLength {
		t.Fatalf("hash %q has unexpected length", first)
	}
	if first == "dhash64:0000000000000000" || first == "dhash64:ffffffffffffffff" {
		t.Fatalf("structured pattern produced a degenerate hash %q", first)
	}
}

func TestPerceptualHash_InvariantToScaleAndBrightness(t *testing.T) {
	baseHash, err := PerceptualHash(verticalBandPattern(64, 64))
	if err != nil {
		t.Fatalf("PerceptualHash(base): %v", err)
	}
	scaledHash, err := PerceptualHash(verticalBandPattern(320, 180))
	if err != nil {
		t.Fatalf("PerceptualHash(scaled): %v", err)
	}
	if baseHash != scaledHash {
		t.Fatalf("scale changed the hash: %q vs %q", baseHash, scaledHash)
	}

	bright := image.NewGray(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		value := 40
		if (x*9/64)%2 == 0 {
			value = 200
		}
		shifted := uint8(value + 20) // 60 / 220: no clamping, ordering preserved.
		for y := 0; y < 64; y++ {
			bright.SetGray(x, y, color.Gray{Y: shifted})
		}
	}
	brightHash, err := PerceptualHash(bright)
	if err != nil {
		t.Fatalf("PerceptualHash(bright): %v", err)
	}
	if baseHash != brightHash {
		t.Fatalf("uniform brightness shift changed the hash: %q vs %q", baseHash, brightHash)
	}
}

func TestPerceptualHash_DistinguishesDifferentImages(t *testing.T) {
	baseHash, err := PerceptualHash(verticalBandPattern(128, 128))
	if err != nil {
		t.Fatalf("PerceptualHash(vertical bands): %v", err)
	}
	orthogonalHash, err := PerceptualHash(horizontalBandPattern(128, 128))
	if err != nil {
		t.Fatalf("PerceptualHash(horizontal bands): %v", err)
	}
	if baseHash == orthogonalHash {
		t.Fatalf("orthogonal patterns produced the same hash %q", baseHash)
	}
	distance, ok := PerceptualHashDistance(baseHash, orthogonalHash)
	if !ok {
		t.Fatalf("PerceptualHashDistance refused two valid hashes")
	}
	if distance == 0 {
		t.Fatalf("distance between orthogonal patterns = 0, want > 0")
	}
}

func TestPerceptualHashBytes_MatchesDecodedImage(t *testing.T) {
	img := verticalBandPattern(200, 120)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	want, err := PerceptualHash(img)
	if err != nil {
		t.Fatalf("PerceptualHash: %v", err)
	}
	got, err := PerceptualHashBytes(encoded.Bytes())
	if err != nil {
		t.Fatalf("PerceptualHashBytes: %v", err)
	}
	if got != want {
		t.Fatalf("PerceptualHashBytes = %q, want %q", got, want)
	}
}

func TestPerceptualHashBytes_RejectsNonImage(t *testing.T) {
	if _, err := PerceptualHashBytes([]byte("not an image")); err == nil {
		t.Fatalf("PerceptualHashBytes accepted non-image bytes")
	}
	if _, err := PerceptualHashBytes(nil); err == nil {
		t.Fatalf("PerceptualHashBytes accepted empty bytes")
	}
}

func TestPerceptualHashDistance_RefusesMalformedAndCrossAlgorithm(t *testing.T) {
	valid, err := PerceptualHash(verticalBandPattern(32, 32))
	if err != nil {
		t.Fatalf("PerceptualHash: %v", err)
	}
	if _, ok := PerceptualHashDistance(valid, ""); ok {
		t.Fatalf("distance with empty value reported ok")
	}
	if _, ok := PerceptualHashDistance(valid, "dhash64:zzzz"); ok {
		t.Fatalf("distance with malformed hex reported ok")
	}
	if _, ok := PerceptualHashDistance(valid, "phash:0123456789abcdef"); ok {
		t.Fatalf("distance across algorithms reported ok")
	}
	if distance, ok := PerceptualHashDistance(valid, valid); !ok || distance != 0 {
		t.Fatalf("self-distance = (%d, %v), want (0, true)", distance, ok)
	}
}
