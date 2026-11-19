package adapters

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func TestHasPanelGridCollageRejectsDeclaredPixelBombWithoutDecoding(t *testing.T) {
	// Without the bounded decode this fixture allocates 30000*30000*4 bytes
	// inside image.Decode before the vision normalizer's 40 MP guard runs.
	if digest.HasPanelGridCollageBytes(pngWithDeclaredDimensionsForCollage(t, 30000, 30000)) {
		t.Fatal("declared pixel bomb must not be classified as a collage")
	}
}

func pngWithDeclaredDimensionsForCollage(t *testing.T, width, height uint32) []byte {
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
