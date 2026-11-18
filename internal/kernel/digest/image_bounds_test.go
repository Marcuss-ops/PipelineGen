package digest

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDecodeImageConfigBoundedRejectsDeclaredPixelBomb(t *testing.T) {
	data := pngWithDeclaredDimensions(t, 30000, 30000)
	if _, _, err := DecodeImageConfigBounded(data, DefaultImagePixelBudget); !errors.Is(err, ErrImagePixelBudget) {
		t.Fatalf("DecodeImageConfigBounded error = %v, want ErrImagePixelBudget", err)
	}
}

func TestDecodeImageBoundedAcceptsNormalImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	decoded, format, err := DecodeImageBounded(buffer.Bytes(), DefaultImagePixelBudget)
	if err != nil {
		t.Fatalf("DecodeImageBounded: %v", err)
	}
	if decoded == nil || format != "png" {
		t.Fatalf("decoded = %v, format = %q", decoded, format)
	}
	if bounds := decoded.Bounds(); bounds.Dx() != 4 || bounds.Dy() != 4 {
		t.Fatalf("decoded bounds = %v, want 4x4", bounds)
	}
}

// pngWithDeclaredDimensions builds a valid PNG whose IHDR advertises the
// requested size while the compressed payload stays one pixel. It is the
// smallest legal declaration of a decompression bomb: the header is enough
// for DecodeConfig, so a decoder that ignores the budget would allocate the
// declared buffer before noticing the truncated image data.
func pngWithDeclaredDimensions(t *testing.T, width, height uint32) []byte {
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
