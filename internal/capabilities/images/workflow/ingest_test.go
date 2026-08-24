package workflow

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

// Helper to decode dimensions using the same function used in production
func mustDecodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode test PNG: %v", err)
	}
	return buf.Bytes()
}

func mustDecodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("failed to encode test JPEG: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeImageDimensions_JPEG(t *testing.T) {
	data := mustDecodeJPEG(t, 800, 600)
	w, h := decodeImageDimensions(data)
	if w != 800 || h != 600 {
		t.Fatalf("expected 800x600, got %dx%d", w, h)
	}
}

func TestDecodeImageDimensions_PNG(t *testing.T) {
	data := mustDecodePNG(t, 300, 240)
	w, h := decodeImageDimensions(data)
	if w != 300 || h != 240 {
		t.Fatalf("expected 300x240, got %dx%d", w, h)
	}
}

func TestDecodeImageDimensions_Invalid(t *testing.T) {
	w, h := decodeImageDimensions([]byte{0x00, 0x01, 0x02, 0x03})
	if w != 0 || h != 0 {
		t.Fatalf("expected 0x0 for invalid data, got %dx%d", w, h)
	}
}

func TestDecodeImageDimensions_Empty(t *testing.T) {
	w, h := decodeImageDimensions(nil)
	if w != 0 || h != 0 {
		t.Fatalf("expected 0x0 for nil, got %dx%d", w, h)
	}
}

func TestUniqueAppend_NoDuplicates(t *testing.T) {
	result := uniqueAppend([]string{"a", "b"}, "c", "d")
	expected := []string{"a", "b", "c", "d"}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
	for i, v := range expected {
		if result[i] != v {
			t.Fatalf("index %d: expected %q, got %q", i, v, result[i])
		}
	}
}

func TestUniqueAppend_WithDuplicates(t *testing.T) {
	result := uniqueAppend([]string{"a", "b"}, "b", "c", "a")
	expected := []string{"a", "b", "c"}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
	for i, v := range expected {
		if result[i] != v {
			t.Fatalf("index %d: expected %q, got %q", i, v, result[i])
		}
	}
}

func TestUniqueAppend_EmptyBase(t *testing.T) {
	result := uniqueAppend(nil, "x", "y")
	expected := []string{"x", "y"}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestUniqueAppend_NoNewItems(t *testing.T) {
	result := uniqueAppend([]string{"a", "b"})
	expected := []string{"a", "b"}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestExtractStyleFromPath_Downloaded(t *testing.T) {
	path := "images/downloaded/wikipedia/realistic/abc123/def.jpg"
	style := extractStyleFromPath(path)
	if style != "realistic" {
		t.Fatalf("expected 'realistic', got %q", style)
	}
}

func TestExtractStyleFromPath_Generated(t *testing.T) {
	path := "images/generated/oil-painting/xyz/789.jpg"
	style := extractStyleFromPath(path)
	if style != "oil-painting" {
		t.Fatalf("expected 'oil-painting', got %q", style)
	}
}

func TestExtractStyleFromPath_ShortPath(t *testing.T) {
	path := "images/generated"
	style := extractStyleFromPath(path)
	if style != "" {
		t.Fatalf("expected empty string for short path, got %q", style)
	}
}

func TestExtractStyleFromPath_DownloadedShort(t *testing.T) {
	path := "images/downloaded/wikipedia"
	style := extractStyleFromPath(path)
	if style != "" {
		t.Fatalf("expected empty string for short downloaded path, got %q", style)
	}
}

func TestExtractStyleFromPath_Empty(t *testing.T) {
	style := extractStyleFromPath("")
	if style != "" {
		t.Fatalf("expected empty string for empty path, got %q", style)
	}
}

func TestExtractStyleFromPath_GeneratedNoSlash(t *testing.T) {
	path := "images/generated"
	style := extractStyleFromPath(path)
	if style != "" {
		t.Fatalf("expected empty for 'images/generated', got %q", style)
	}
}

func TestExtractStyleFromPath_UnknownBase(t *testing.T) {
	path := "images/custom/photo/123.jpg"
	style := extractStyleFromPath(path)
	if style != "" {
		t.Fatalf("expected empty for unknown base, got %q", style)
	}
}
