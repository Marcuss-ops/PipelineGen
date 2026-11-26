package model

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
)

func isAllowedMapMotion(id string) bool {
	return capanimreg.AnimationSelectable(id, "image.map_centered")
}

// isSingleLineVisibleText reports whether value is non-blank valid UTF-8 with
// no control characters — the shape every value that reaches a rendered text
// surface must have. It is the single owner of the "single line, no control
// characters" rule shared by the visible attribution and the pin labels.
func isSingleLineVisibleText(value string) bool {
	if strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// verifyPNGRaster fails closed unless the content-addressed basemap is really
// a PNG. The declared media type is producer-supplied metadata; the plate the
// renderer paints and georeferences is the bytes on disk, so the bytes are
// what gets checked.
func verifyPNGRaster(path string) error {
	_, _, err := verifyPNGRasterDimensions(path)
	return err
}

func verifyPNGRasterDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, len(pngSignature))
	if _, err := io.ReadFull(file, header); err != nil {
		return 0, 0, fmt.Errorf("not a PNG raster: %w", err)
	}
	if !bytes.Equal(header, pngSignature) {
		return 0, 0, fmt.Errorf("not a PNG raster: unexpected magic")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, 0, err
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	if format != "png" || config.Width <= 0 || config.Height <= 0 {
		return 0, 0, fmt.Errorf("invalid PNG raster configuration")
	}
	return config.Width, config.Height, nil
}
