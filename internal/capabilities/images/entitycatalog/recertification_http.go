package images

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
)

const maxRecertificationBodyBytes = 32 << 20

// decodeImageConfig performs a complete decode, not only header parsing. The
// byte limit bounds memory and protects the periodic maintenance worker from
// oversized or malicious remote responses.
func decodeImageConfig(body io.Reader) (image.Config, string, error) {
	decoded, format, err := image.Decode(io.LimitReader(body, maxRecertificationBodyBytes))
	if err != nil {
		return image.Config{}, format, err
	}
	bounds := decoded.Bounds()
	return image.Config{Width: bounds.Dx(), Height: bounds.Dy()}, format, nil
}
