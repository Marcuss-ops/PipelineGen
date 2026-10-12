package adapters

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func TestHasPanelGridCollageRejectsContactSheet(t *testing.T) {
	const width, height, gutter = 600, 360, 4
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	colors := []color.RGBA{
		{R: 20, G: 80, B: 150, A: 255}, {R: 190, G: 60, B: 25, A: 255}, {R: 50, G: 155, B: 80, A: 255},
		{R: 180, G: 140, B: 30, A: 255}, {R: 95, G: 45, B: 150, A: 255}, {R: 20, G: 140, B: 155, A: 255},
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			column := x * 3 / width
			row := y * 2 / height
			if x%(width/3) < gutter || y%(height/2) < gutter {
				img.Set(x, y, color.White)
				continue
			}
			img.Set(x, y, colors[row*3+column])
		}
	}

	if !hasPanelGridCollage(encodePNG(t, img)) {
		t.Fatal("panel grid was not rejected")
	}
}

func TestHasPanelGridCollageKeepsSinglePhoto(t *testing.T) {
	const width, height = 600, 360
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dx, dy := float64(x-width/2)/115, float64(y-height/2)/145
			if dx*dx+dy*dy < 1 {
				img.Set(x, y, color.RGBA{R: 185, G: 125, B: 95, A: 255})
			} else {
				img.Set(x, y, color.RGBA{R: uint8(35 + y/12), G: uint8(55 + x/30), B: 75, A: 255})
			}
		}
	}

	if hasPanelGridCollage(encodePNG(t, img)) {
		t.Fatal("single photo was classified as a panel grid")
	}
}

func TestHasPanelGridCollageAllowsSinglePhotoWithBlurredSidebars(t *testing.T) {
	const width, height, photoLeft, photoRight = 600, 360, 180, 420
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var luma int
			switch {
			case x < photoLeft:
				// Smooth, low-detail copies used to fill the sides of a
				// centered portrait crop.
				luma = 35 + int(5*math.Sin(float64(x+y)/65))
			case x >= photoRight:
				luma = 45 + int(5*math.Sin(float64(x-y)/65))
			default:
				// A single photographic panel has visible local detail, but
				// no long straight divider within the photo itself.
				luma = 120 + int(25*math.Sin(float64(x)/8)) + int(25*math.Sin(float64(y)/9))
				if x-photoLeft < 5 || photoRight-x <= 5 {
					luma = 225
				}
			}
			img.Set(x, y, color.Gray{Y: uint8(luma)})
		}
	}

	if hasPanelGridCollage(encodePNG(t, img)) {
		t.Fatal("single central photo with blurred sidebars was classified as a collage")
	}
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buffer.Bytes()
}
