package digest

import (
	"bytes"
	"encoding/hex"
	"errors"
	"image"
	"math/bits"
	"strings"

	// Register the decoders the perceptual hash understands. Decoding is
	// performed here (not by the caller) so every producer in the repository
	// computes the hash over the same pixels.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Perceptual hash contract (SSOT for "do these two images LOOK the same?").
//
// Unlike SHA-256 (byte identity) a perceptual hash is invariant to
// re-encoding, resizing, mild compression and small exposure shifts, so it can
// catch the same photo arriving from two providers under two URLs. The
// algorithm is a difference hash (dHash): the image is reduced to a 9x8
// grayscale grid and each of the 8x8 horizontal neighbour comparisons becomes
// one bit.
//
// The value is stored as an algorithm-prefixed string ("dhash64:<16 hex>").
// The prefix is not decoration: it makes a cross-algorithm comparison
// impossible to get silently wrong, because PerceptualHashDistance refuses to
// compare two different algorithms. The DCT-based imagehash.phash used at
// video indexing is a DIFFERENT algorithm, so it can never be compared here.
const (
	// PerceptualHashAlgorithm is the canonical algorithm tag for the Go-native
	// difference hash.
	PerceptualHashAlgorithm = "dhash64"
	// PerceptualHashBits is the number of bits in one dHash (8 rows x 8
	// horizontal comparisons).
	PerceptualHashBits = 64
	// PerceptualHashHexLength is the number of hex characters in the encoded
	// dHash (64 bits / 4).
	PerceptualHashHexLength = 16
	// perceptualHashGridColumns and perceptualHashGridRows are the grayscale
	// grid dimensions. Columns is rows+1 so every row yields 8 comparisons.
	perceptualHashGridColumns = 9
	perceptualHashGridRows    = 8
	perceptualHashPrefix      = PerceptualHashAlgorithm + ":"
)

// ErrPerceptualHashUnavailable is returned when the bytes are not a decodable
// image. Callers treat it as "no perceptual identity available" and keep the
// candidate; a perceptual hash is a dedup optimization, never a gate.
var ErrPerceptualHashUnavailable = errors.New("digest: perceptual hash unavailable")

// PerceptualHash computes the canonical dHash of an already-decoded image.
func PerceptualHash(img image.Image) (string, error) {
	if img == nil {
		return "", ErrPerceptualHashUnavailable
	}
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return "", ErrPerceptualHashUnavailable
	}
	gray := downsamplePerceptualGray(img, perceptualHashGridColumns, perceptualHashGridRows)
	return encodePerceptualHash(perceptualDifferenceBits(gray)), nil
}

// PerceptualHashBytes decodes the image bytes and returns the canonical dHash.
// The bytes must be an image format registered by an importing binary (PNG,
// JPEG and GIF are registered here).
func PerceptualHashBytes(data []byte) (string, error) {
	if len(data) == 0 {
		return "", ErrPerceptualHashUnavailable
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", ErrPerceptualHashUnavailable
	}
	return PerceptualHash(img)
}

// PerceptualHashDistance returns the Hamming distance between two encoded
// perceptual hashes. ok is false when either value is empty, malformed, or
// produced by a different algorithm — a cross-algorithm comparison is refused
// rather than approximated.
func PerceptualHashDistance(a, b string) (int, bool) {
	left, ok := decodePerceptualHash(a)
	if !ok {
		return 0, false
	}
	right, ok := decodePerceptualHash(b)
	if !ok {
		return 0, false
	}
	distance := 0
	for i := range left {
		distance += bits.OnesCount8(left[i] ^ right[i])
	}
	return distance, true
}

// IsPerceptualHash reports whether value is a well-formed encoded perceptual
// hash of a known algorithm.
func IsPerceptualHash(value string) bool {
	_, ok := decodePerceptualHash(value)
	return ok
}

func downsamplePerceptualGray(img image.Image, columns, rows int) []uint8 {
	bounds := img.Bounds()
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	out := make([]uint8, columns*rows)
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return out
	}
	for row := 0; row < rows; row++ {
		y0 := bounds.Min.Y + row*sourceHeight/rows
		y1 := bounds.Min.Y + (row+1)*sourceHeight/rows
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for column := 0; column < columns; column++ {
			x0 := bounds.Min.X + column*sourceWidth/columns
			x1 := bounds.Min.X + (column+1)*sourceWidth/columns
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sum uint64
			var count uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, b, _ := img.At(x, y).RGBA()
					// ITU-R BT.601 luma, kept in 8-bit space.
					sum += uint64((299*r + 587*g + 114*b) / 1000 >> 8)
					count++
				}
			}
			if count > 0 {
				out[row*columns+column] = uint8(sum / count)
			}
		}
	}
	return out
}

func perceptualDifferenceBits(gray []uint8) []byte {
	comparisonsPerRow := perceptualHashGridColumns - 1
	out := make([]byte, (comparisonsPerRow*perceptualHashGridRows+7)/8)
	bit := 0
	for row := 0; row < perceptualHashGridRows; row++ {
		base := row * perceptualHashGridColumns
		for column := 0; column < comparisonsPerRow; column++ {
			if gray[base+column] > gray[base+column+1] {
				out[bit/8] |= 1 << (7 - uint(bit%8))
			}
			bit++
		}
	}
	return out
}

func encodePerceptualHash(raw []byte) string {
	return perceptualHashPrefix + hex.EncodeToString(raw)
}

func decodePerceptualHash(value string) ([]byte, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, perceptualHashPrefix) {
		return nil, false
	}
	encoded := strings.TrimPrefix(value, perceptualHashPrefix)
	if len(encoded) != PerceptualHashHexLength {
		return nil, false
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil || len(raw) != PerceptualHashBits/8 {
		return nil, false
	}
	return raw, true
}
