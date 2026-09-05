package chronon

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

func watermarkDimensions(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}

func watermarkPositionForSize(imgW, imgH int, position string, canvasW, canvasH, margin int) []int {
	if margin < 0 {
		margin = 0
	}
	if imgW <= 0 || imgH <= 0 {
		return []int{0, 0}
	}
	x, y := margin, margin
	switch position {
	case cliprender.PositionTopRight:
		x = canvasW - margin - imgW
	case cliprender.PositionBottomLeft:
		y = canvasH - margin - imgH
	case cliprender.PositionBottomRight:
		x = canvasW - margin - imgW
		y = canvasH - margin - imgH
	case cliprender.PositionCenter:
		x = (canvasW - imgW) / 2
		y = (canvasH - imgH) / 2
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return []int{x + imgW/2 - canvasW/2, y + imgH/2 - canvasH/2}
}
