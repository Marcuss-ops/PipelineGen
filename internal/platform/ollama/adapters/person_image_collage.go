package adapters

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
)

// hasPanelGridCollage rejects source images with multiple near-full-length,
// straight panel boundaries. This pixel check complements the vision model:
// the runtime's current model has accepted a news montage as a single portrait.
func hasPanelGridCollage(data []byte) bool {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return false
	}
	vertical := alignedPanelSeams(img, true)
	if len(vertical) >= 2 && !singlePhotoWithBlurredExtension(img, vertical, true) {
		return true
	}
	horizontal := alignedPanelSeams(img, false)
	return len(horizontal) >= 2 && !singlePhotoWithBlurredExtension(img, horizontal, false)
}

// singlePhotoWithBlurredExtension recognizes the common 16:9 news-thumbnail
// treatment where one portrait is centered and enlarged, blurred copies of
// its edges fill the left/right (or top/bottom) sidebars. The hard borders
// look like grid seams to alignedPanelSeams, but low-detail sidebars and one
// symmetric central photo distinguish this treatment from a multi-photo
// contact sheet. Any extra seam remains a collage signal.
func singlePhotoWithBlurredExtension(img image.Image, seams []int, vertical bool) bool {
	if len(seams) != 2 {
		return false
	}
	bounds := img.Bounds()
	axisMin, axisLength := bounds.Min.Y, bounds.Dy()
	crossMin, crossLength := bounds.Min.X, bounds.Dx()
	if vertical {
		axisMin, axisLength = bounds.Min.X, bounds.Dx()
		crossMin, crossLength = bounds.Min.Y, bounds.Dy()
	}
	left, right := seams[0], seams[1]
	centerRatio := float64(right-left) / float64(axisLength)
	if centerRatio < 0.35 || centerRatio > 0.58 {
		return false
	}
	if float64(absInt(left+right-axisLength))/float64(axisLength) > 0.04 {
		return false
	}
	leftDetail := meanRegionGradient(img, vertical, axisMin, axisMin+left, crossMin, crossMin+crossLength)
	centerDetail := meanRegionGradient(img, vertical, axisMin+left, axisMin+right, crossMin, crossMin+crossLength)
	rightDetail := meanRegionGradient(img, vertical, axisMin+right, axisMin+axisLength, crossMin, crossMin+crossLength)
	if centerDetail < 3 {
		return false
	}
	return leftDetail < centerDetail*0.68 && rightDetail < centerDetail*0.68
}

// meanRegionGradient measures one-pixel luma changes on a bounded sampling
// grid. The small inset removes the seam edge itself from the region score.
func meanRegionGradient(img image.Image, vertical bool, axisStart, axisEnd, crossStart, crossEnd int) float64 {
	axisLength := axisEnd - axisStart
	crossLength := crossEnd - crossStart
	if axisLength < 8 || crossLength < 8 {
		return 0
	}
	inset := axisLength / 16
	if inset < 2 {
		inset = 2
	}
	axisStart += inset
	axisEnd -= inset
	crossInset := crossLength / 10
	crossStart += crossInset
	crossEnd -= crossInset
	if axisEnd-axisStart < 4 || crossEnd-crossStart < 4 {
		return 0
	}
	const stride = 4
	var total, count int64
	for axis := axisStart + 1; axis < axisEnd-1; axis += stride {
		for cross := crossStart + 1; cross < crossEnd-1; cross += stride {
			var beforeAxis, afterAxis, beforeCross, afterCross color.Color
			if vertical {
				beforeAxis = img.At(axis-1, cross)
				afterAxis = img.At(axis+1, cross)
				beforeCross = img.At(axis, cross-1)
				afterCross = img.At(axis, cross+1)
			} else {
				beforeAxis = img.At(cross, axis-1)
				afterAxis = img.At(cross, axis+1)
				beforeCross = img.At(cross-1, axis)
				afterCross = img.At(cross+1, axis)
			}
			total += int64(absInt(pixelLuma(beforeAxis) - pixelLuma(afterAxis)))
			total += int64(absInt(pixelLuma(beforeCross) - pixelLuma(afterCross)))
			count += 2
		}
	}
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}

// alignedPanelSeams returns long, high-contrast interior lines on one axis.
// Requiring two separate lines avoids rejecting ordinary photos with one
// horizon, wall edge, or frame. Lines on each side of a narrow gutter are
// clustered into one seam.
func alignedPanelSeams(img image.Image, vertical bool) []int {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	axisLength, crossLength := height, width
	if vertical {
		axisLength, crossLength = width, height
	}
	if axisLength < 320 || crossLength < 240 {
		return nil
	}

	crossStart, crossEnd := crossLength*15/100, crossLength*85/100
	crossSamples := crossEnd - crossStart
	if crossSamples > 512 {
		crossSamples = 512
	}
	if crossSamples < 32 {
		return nil
	}
	interiorStart, interiorEnd := axisLength*15/100, axisLength*85/100
	groupGap := axisLength / 512
	if groupGap < 8 {
		groupGap = 8
	}
	if groupGap > 16 {
		groupGap = 16
	}
	const edgeThreshold = 45
	const continuityThreshold = 0.58

	strong := make([]int, 0, 4)
	for position := interiorStart + 3; position < interiorEnd-3; position++ {
		edges := 0
		for sample := 0; sample < crossSamples; sample++ {
			cross := crossStart + sample*(crossEnd-crossStart-1)/max(1, crossSamples-1)
			var a, b int
			if vertical {
				a = pixelLuma(img.At(bounds.Min.X+position-3, bounds.Min.Y+cross))
				b = pixelLuma(img.At(bounds.Min.X+position+3, bounds.Min.Y+cross))
			} else {
				a = pixelLuma(img.At(bounds.Min.X+cross, bounds.Min.Y+position-3))
				b = pixelLuma(img.At(bounds.Min.X+cross, bounds.Min.Y+position+3))
			}
			if absInt(a-b) >= edgeThreshold {
				edges++
			}
		}
		if float64(edges)/float64(crossSamples) >= continuityThreshold {
			strong = append(strong, position)
		}
	}
	if len(strong) == 0 {
		return nil
	}

	seams := make([]int, 0, len(strong))
	start, previous := strong[0], strong[0]
	for _, position := range strong[1:] {
		if position-previous <= groupGap {
			previous = position
			continue
		}
		seams = append(seams, start+(previous-start)/2)
		start, previous = position, position
	}
	seams = append(seams, start+(previous-start)/2)
	return seams
}

func pixelLuma(color color.Color) int {
	r, g, b, _ := color.RGBA()
	return (299*int(r>>8) + 587*int(g>>8) + 114*int(b>>8)) / 1000
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
