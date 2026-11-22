package digest

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// panelCollageFixture builds a triptych: three dark panels separated by
// full-width white gutters, the layout the pipeline must never bind as one
// photograph.
func panelCollageFixture(t *testing.T) image.Image {
	t.Helper()
	const width, height, gutter = 900, 900, 24
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, white)
		}
	}
	panels := []color.RGBA{
		{R: 40, G: 60, B: 90, A: 255},
		{R: 120, G: 90, B: 60, A: 255},
		{R: 30, G: 30, B: 30, A: 255},
	}
	top := []int{0, 300 + gutter, 600 + 2*gutter}
	for index, panel := range panels {
		bottom := top[index] + 300
		if index == 0 {
			bottom = 300
		}
		for y := top[index]; y < bottom; y++ {
			for x := 0; x < width; x++ {
				img.SetRGBA(x, y, panel)
			}
		}
	}
	return img
}

func singlePhotoFixture(t *testing.T) image.Image {
	t.Helper()
	const width, height = 900, 900
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// A smooth horizon-like gradient: no straight luma seam.
			shade := uint8(20 + (x*160)/width)
			img.SetRGBA(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	return img
}

func photoWithPartialArchitecturalEdgesFixture() image.Image {
	const width, height = 900, 900
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			shade := uint8(35 + (x*110)/width)
			img.SetRGBA(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	// Two hard, partial-height edges resemble building columns in a single
	// photograph. Their ~71% sampled continuity passed the old 58% grid gate.
	for _, seam := range []int{280, 620} {
		for y := height * 25 / 100; y < height*75/100; y++ {
			img.SetRGBA(seam, y, color.RGBA{R: 245, G: 245, B: 245, A: 255})
		}
	}
	return img
}

func TestHasPanelGridCollageImageDetectsTriptych(t *testing.T) {
	if !HasPanelGridCollageImage(panelCollageFixture(t)) {
		t.Fatal("three panels separated by white gutters must be a collage")
	}
}

// diptychFixture builds a two-panel side-by-side montage with a thin white
// divider: the layout that only exposes a single centered vertical seam.
func diptychFixture(t *testing.T) image.Image {
	t.Helper()
	const width, height = 960, 540
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	left := color.RGBA{R: 40, G: 60, B: 90, A: 255}
	right := color.RGBA{R: 120, G: 90, B: 60, A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			switch {
			case x < width/2-2:
				img.SetRGBA(x, y, left)
			case x >= width/2+2:
				img.SetRGBA(x, y, right)
			default:
				img.SetRGBA(x, y, white)
			}
		}
	}
	return img
}

func TestHasPanelGridCollageImageDetectsDiptych(t *testing.T) {
	if !HasPanelGridCollageImage(diptychFixture(t)) {
		t.Fatal("two photos split by one centered seam must be a collage")
	}
}

func TestHasPanelGridCollageImageAcceptsSinglePhoto(t *testing.T) {
	if HasPanelGridCollageImage(singlePhotoFixture(t)) {
		t.Fatal("a seamless photo must not be rejected as a collage")
	}
}

func TestHasPanelGridCollageImageAcceptsPhotoWithPartialArchitecturalEdges(t *testing.T) {
	if HasPanelGridCollageImage(photoWithPartialArchitecturalEdgesFixture()) {
		t.Fatal("partial-height architectural edges in one photograph are not a panel collage")
	}
}

func TestHasPanelGridCollageBytesDetectsTriptych(t *testing.T) {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, panelCollageFixture(t)); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	if !HasPanelGridCollageBytes(buffer.Bytes()) {
		t.Fatal("encoded triptych must be a collage")
	}
}

func TestHasPanelGridCollageBytesIgnoresUndecodableData(t *testing.T) {
	if HasPanelGridCollageBytes([]byte("not an image")) {
		t.Fatal("undecodable bytes must not be classified as a collage")
	}
}

func TestPanelGridCollageEvidenceExplainsRejection(t *testing.T) {
	img := panelCollageFixture(t)
	if !HasPanelGridCollageImage(img) {
		t.Fatal("fixture must remain a collage verdict")
	}
	seamsV, seamsH, continuityV, continuityH := PanelGridCollageEvidence(img)
	if seamsH < 2 {
		t.Fatalf("horizontal seams = %d, want >= 2 for the triptych fixture", seamsH)
	}
	if continuityH < 0.82 {
		t.Fatalf("horizontal continuity = %.2f, want >= 0.82", continuityH)
	}
	// The evidence must never invent a stronger vertical verdict than the
	// horizontal one that drove the rejection.
	if continuityV > 1 || seamsV < 0 {
		t.Fatalf("invalid vertical evidence: seams=%d continuity=%.2f", seamsV, continuityV)
	}
}
