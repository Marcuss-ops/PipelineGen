package digest

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// DefaultImagePixelBudget is the canonical upper bound for an image decoded
// from an untrusted source. 40 MP keeps a single RGBA decode near 160 MB,
// which is compatible with the bounded worker concurrency while still
// admitting normal editorial photography and 4K frames.
const DefaultImagePixelBudget = 40_000_000

// ErrImagePixelBudget marks an image whose declared dimensions exceed the
// caller's budget. It is returned before any full decode, so a compressed
// decompression bomb cannot allocate the pixel buffer.
var ErrImagePixelBudget = errors.New("image exceeds the pixel budget")

// DecodeImageConfigBounded reads only the image header and rejects negative,
// zero or oversized dimensions. Callers use it as the first gate on bytes
// received from the network.
func DecodeImageConfigBounded(data []byte, maxPixels int) (image.Config, string, error) {
	if maxPixels <= 0 {
		maxPixels = DefaultImagePixelBudget
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, "", fmt.Errorf("decode image config: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return image.Config{}, "", fmt.Errorf("decode image config: invalid dimensions %dx%d", config.Width, config.Height)
	}
	if int64(config.Width)*int64(config.Height) > int64(maxPixels) {
		return image.Config{}, "", fmt.Errorf("%w: %dx%d exceeds %d pixels", ErrImagePixelBudget, config.Width, config.Height, maxPixels)
	}
	return config, format, nil
}

// DecodeImageBounded applies the header budget before decoding the pixels.
// The returned error preserves ErrImagePixelBudget so callers can map it to
// their own "too large" contract.
func DecodeImageBounded(data []byte, maxPixels int) (image.Image, string, error) {
	if _, _, err := DecodeImageConfigBounded(data, maxPixels); err != nil {
		return nil, "", err
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}
	return img, format, nil
}
