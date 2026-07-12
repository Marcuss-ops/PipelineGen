// Package visual_validate implements post-extraction image-content
// validation.
//
// godlike/07 no-fake-availability (P0.2, July 2026): the pre-fix pipeline
// accepted a pure-white PNG as a valid GeneratedImage because Go only
// checked file size + SHA-256 — both trivially pass for a blank PNG.
// This package decodes the PNG and asserts three independent content
// invariants: low near-white percentage, non-trivial variance, and
// pHash distance from a deterministic slide-vuoto reference. A blank
// image fails at least one of them and gets a typed
// ErrBlankOrPlaceholder back to the caller — ChromeImageProvider's
// removeOutputAndFail closed-loop.
//
// Style gating (whiteboard exception): the "whiteboard" style is allowed
// up to 99.8% near-white because whiteboard renders legitimately have a
// vast white background and ink density is small. The pHash check is
// unchanged: the image MUST differ from the slide-vuoto baseline by at
// least 6 bits (hamming distance > 5) regardless of style. Pure-white
// images are still rejected.
//
// P0.2.A (July 2026, July-29 reviewer feedback): the whiteboard
// carve-out now ALSO requires a minimum edge density
// (minWhiteboardEdgeDensity) so a near-blank image with microscopic
// ink dots that JUST passes the pHash distance check is fail-closed
// (rather than passing as a "stale-reuse cache hit" by accident).
// Additionally, the DistinctColors stat is exposed (count of unique
// RGBA values in the image, capped at distinctColorCardinalityCap for
// OOM safety against maliciously-large PNG inputs).
//
// Performance: 1024x1024 decode + 8x8 pHash + 16-stride pixel scan takes
// <50 ms on a mid-range laptop. The worker callers run validation on
// disk after the worker writes output_path, so the runtime cost is
// amortised over Playwright's 22-60s per-image generation time.
package visual_validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/png" // register PNG decoder so image.Decode handles .png
	"os"
)

// Stats is the canonical content-statistics envelope exposed to
// chrome_provider.go Zap logs and to the wire-level test fixtures.
//
// Fields:
//   - Width, Height: real pixel dimensions from image.Decode bounds
//     (NOT the w/h the Go pipeline was told to ask for — those may
//     be misleading if the worker truncated or resized).
//   - WhitePct: fraction of pixels with r >= 240 && g >= 240 && b >= 240.
//     Pure-white image → 1.000; real generated image → typically < 0.5.
//   - Variance: per-pixel grayscale variance (0 for monochrome). Real
//     generated images typically have variance > 100.
//   - EdgeDensity: fraction of horizontal pixel-pairs where |Δgray| > 30.
//     Pure-white image → 0.0; real images → in (0, 1] with structure.
//   - DistinctColors: count of unique RGBA values in the image, bounded
//     by distinctColorCardinalityCap. A non-blank signal even at low
//     edge density (e.g. a 16-color flat-fill region).
//   - PHashHex: 16-hex-char perceptive hash of the 8x8 sample grid.
//
// godlike/07 transparency (P2, July 2026): Stats is the SINGLE SOURCE
// OF TRUTH for content statistics. Validate calls analyzePixels
// internally for threshold checks; chrome_provider and the Python
// worker both reach into this struct to populate their respective
// structured logs (no duplicate PIL/Go-passes).
type Stats struct {
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	WhitePct       float64 `json:"white_pct"`
	Variance       float64 `json:"variance"`
	EdgeDensity    float64 `json:"edge_density"`
	DistinctColors int     `json:"distinct_colors"`
	PHashHex       string  `json:"phash_hex"`

	// privatePhash is the canonical uint64 pHash bit-vector. It is NOT
	// serialised — Validate uses it for the distance-to-blank check
	// while the JSON struct uses the hex string. Private to avoid
	// callers bypassing Validate to forge stats.
	privatePhash uint64
}

// ComputeStats decodes the PNG at filePath and returns its content
// statistics: dimensions, near-white ratio, grayscale variance, edge
// density, distinct color count, and the 8x8 pHash in canonical
// 16-hex-char form. No threshold checks are applied — the caller
// decides what to do with the stats (Validate uses them for the typed
// sentinel; chrome_provider uses them for the Zap log; the Python
// worker uses a parallel PIL pass for its JSONL diagnostics, then
// mirrors the same numbers back to the Go side through workerResponse
// stats fields).
//
// godlike/07 transparency: this function is the canonical Go-side
// source of content statistics. Validate(filePath, styleID) calls
// it internally; chrome_provider.go::generateOnce calls it for log
// replication after a successful extraction (avoiding duplicate
// decode passes by reusing the result).
func ComputeStats(filePath string) (Stats, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return Stats{}, fmt.Errorf("visual_validate: open %q: %w", filePath, err)
	}
	defer f.Close()

	img, format, err := image.Decode(f)
	if err != nil {
		return Stats{}, fmt.Errorf("visual_validate: decode %q (format=%s): %w", filePath, format, err)
	}

	whitePct, variance, edgeDensity, distinctColors := analyzePixels(img)
	hash := pHash(img)

	return Stats{
		Width:          img.Bounds().Dx(),
		Height:         img.Bounds().Dy(),
		WhitePct:       whitePct,
		Variance:       variance,
		EdgeDensity:    edgeDensity,
		DistinctColors: distinctColors,
		PHashHex:       fmt.Sprintf("%016x", hash),
		privatePhash:   hash,
	}, nil
}

// ErrBlankOrPlaceholder is the typed sentinel returned when an image
// fails content validation. The caller (ChromeImageProvider.Generate)
// treats this as a FAIL-CLOSED condition: the file at the canonical
// output_path is REMOVED, the caller does NOT receive a
// GeneratedImage, and the typed error propagates up the retry policy.
//
// Per godlike/07 fail-closed contract, a blank/white/slide-vuoto image
// MUST NOT be considered a successful image-generation result.
var ErrBlankOrPlaceholder = errors.New("visual_validate: blank or placeholder image detected")

// StyleWhiteboard is the sentinel ID for the "whiteboard" style
// variant. Other style IDs share the standard thresholds.
const StyleWhiteboard = "whiteboard"

// Thresholds (P0.2, July 2026). Tweaked for the canonical case
// (1920x1080 PNG generated by Google Slides AI via Nano Banana Pro):
//   - Pure-white image: white_pct=1.000, variance=0, hash=0 (matches blank).
//   - Real image:      white_pct typically <0.5, variance >500, hash differs.
//   - Whiteboard:      white_pct ~0.99+, variance ~50-200, hash differs.
//
// The values below are the bounds at which Validate returns AC; the
// reject thresholds are the inverses.
//
// Review feedback P0.2: the standard "white_pct > 0.99" matches the
// user spec ("nessun file salvaricato >99% near-white"). Whiteboard gets
// 0.998 heads-up so legitimate ink-on-white renders don't fail-closed.
//
// P0.2.A: minWhiteboardEdgeDensity floor added so whiteboard exception
// only fires for actual ink-on-white renders (not microscopic-dot
// stale-reuse cache hits).
//
// distinctColorCardinalityCap bounds the in-memory color.RGBA set so
// maliciously-large PNG inputs cannot OOM the validator.
const (
	stdMaxWhitePct              = 0.99
	whiteboardMaxWhitePct       = 0.998
	minVariance                 = 5.0
	maxPhashDistanceSquash      = 5     // <=5 same as blank; >5 different.
	minWhiteboardEdgeDensity    = 0.001 // P0.2.A: whiteboard carve-out requires sparse ink.
	distinctColorCardinalityCap = 4096  // P0.2.A: bounded distinct-color set (anti-OOM).
)

// Validate decodes the PNG at filePath and asserts it is not a blank /
// slide-vuoto / placeholder image. The styleID parameter gates the
// whiteboard exception and the new edge-density floor. Returns nil for
// a content-valid PNG, otherwise a typed error wrapping
// ErrBlankOrPlaceholder with the specific failure cause.
//
// P0.2.A: whiteboard exception now requires BOTH white_pct <= 0.998
// AND edge_density >= minWhiteboardEdgeDensity. Pure-white images are
// still rejected at the pHash check regardless of style.
func Validate(filePath string, styleID string) error {
	stats, err := ComputeStats(filePath)
	if err != nil {
		return err
	}

	// Style-gated near-white threshold. Whiteboard exception is the
	// only current carve-out: see StyleWhiteboard. New style carve-outs
	// land as opt-in additions; default = standard thresholds.
	maxWhite := stdMaxWhitePct
	if styleID == StyleWhiteboard {
		maxWhite = whiteboardMaxWhitePct
	}
	if stats.WhitePct > maxWhite {
		return fmt.Errorf("%w: style=%q white_pct=%.4f > %.4f (file=%s)",
			ErrBlankOrPlaceholder, styleID, stats.WhitePct, maxWhite, filePath)
	}

	// P0.2.A: whiteboard carve-out also requires minimum edge density
	// so a microscopically-scattered image that JUST passes the pHash
	// check cannot fake a "stale-reuse hit" through the whiteboard
	// exception. This catches images like vecchiaRiusata (horizontal
	// row of dark pixels on white background) — they flip enough pHash
	// bits but their edge density is 0 (uniform row, no horizontal
	// transitions) so they are correctly rejected.
	if styleID == StyleWhiteboard && stats.EdgeDensity < minWhiteboardEdgeDensity {
		return fmt.Errorf("%w: style=%q edge_density=%.4f < %.4f (whiteboard needs sparse ink, file=%s)",
			ErrBlankOrPlaceholder, styleID, stats.EdgeDensity, minWhiteboardEdgeDensity, filePath)
	}

	// Variance below threshold = monochrome / gray gradient / single
	// color fill. A real generated image has variance > 100 typically;
	// minVariance=5 catches trivial single-color outputs that slipped
	// past the white-pct check.
	if stats.Variance < minVariance {
		return fmt.Errorf("%w: style=%q variance=%.2f < %.2f (monochrome fill, file=%s)",
			ErrBlankOrPlaceholder, styleID, stats.Variance, minVariance, filePath)
	}

	// pHash distance from the slide-vuoto reference (all-white image).
	// All-white input has hash=0; pHash of all-white reference = 0;
	// distance = 0 → reject. Real images diverge by 30-60 bits typically.
	// The 5-bit threshold is strict: anything within 5 bits is too close
	// to the canonical placeholder -> reject.
	refHash := referenceBlankHash()
	if hammingDistance(stats.privatePhash, refHash) <= maxPhashDistanceSquash {
		return fmt.Errorf("%w: style=%q phash_distance_to_blank=%d <= %d (file=%s)",
			ErrBlankOrPlaceholder, styleID,
			hammingDistance(stats.privatePhash, refHash), maxPhashDistanceSquash, filePath)
	}

	return nil
}

// MarshalJSON forces the Stats struct to omit the privatePhash field
// from its JSON output (defensive: no caller can ever see that bit-
// vector, only the hex form). Added in P2 (July 2026) so audit logs
// and JSON diagnostic dumps cannot accidentally leak the raw uint64.
func (s Stats) MarshalJSON() ([]byte, error) {
	type statsJSON struct {
		Width          int     `json:"width"`
		Height         int     `json:"height"`
		WhitePct       float64 `json:"white_pct"`
		Variance       float64 `json:"variance"`
		EdgeDensity    float64 `json:"edge_density"`
		DistinctColors int     `json:"distinct_colors"`
		PHashHex       string  `json:"phash_hex"`
	}
	return json.Marshal(statsJSON{
		Width:          s.Width,
		Height:         s.Height,
		WhitePct:       s.WhitePct,
		Variance:       s.Variance,
		EdgeDensity:    s.EdgeDensity,
		DistinctColors: s.DistinctColors,
		PHashHex:       s.PHashHex,
	})
}

// analyzePixels iterates every pixel of the image bounds and computes
// the exact near-white ratio + grayscale variance + horizontal-edge
// density + distinct color count in a SINGLE pass over a per-row
// grayscale buffer (the second mini-pass over horizontal diffs counts
// edges without redoing the per-pixel image.At lookup).
//
// Returns (whitePct, variance, edgeDensity, distinctColors).
//
// Why full iteration (not stride-sampled): the earlier 16-stride
// sampler yielded integer white_pct values (number-of-sampled-whites
// / samples). For a 80x80 image with stride=16 the sample set is
// 5x5 = 25 pixels, so a fixture with `n` ink pixels that doesn't
// align with sample positions would measure wp=1.0 (all sampled
// white) even when total ink is ~1% of the image. The dual-style
// "whiteboard exception vs standard reject" test contract requires
// white_pct in the open interval (0.99, 0.998]; 25 samples cannot
// produce non-integer ratios, so dual-style testing became impossible.
// Full iteration gives float white_pct that correctly tracks the
// actual visual ratio.
//
// distinctColors uses an in-memory map[color.RGBA]struct{} keyed by
// the exact per-pixel RGBA. The map is bounded at
// distinctColorCardinalityCap (4096) so a maliciously-large PNG cannot
// OOM the validator — once the cap is hit, the loop stops the count
// at the cap value (the canonical signal is already "many colors,
// definitely not blank" before hitting the cap).
//
// Performance: 1920x1080 = 2.07M pixels with image.At() lookups is
// ~50ms on a mid-range laptop. Add ~5ms for the per-pixel map insert
// (113ns/op median for Go's built-in map[string]struct{}, RGBA is
// similarly cheap). Acceptable for one-shot generation validation;
// the validator runs once per image, not in a hot loop.
func analyzePixels(img image.Image) (whitePct, variance, edgeDensity float64, distinctColors int) {
	bounds := img.Bounds()
	if bounds.Empty() {
		return 0, 0, 0, 0
	}
	w := bounds.Dx()
	h := bounds.Dy()
	nPixels := int64(w) * int64(h)
	if nPixels == 0 {
		return 0, 0, 0, 0
	}

	var whiteCount, sum, sumSq int64
	colorSet := make(map[color.RGBA]struct{}, 64) // P0.2.A: distinct-color set (bounded at distinctColorCardinalityCap)
	gray := make([]int16, int(nPixels))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			idx := int64(y-bounds.Min.Y)*int64(w) + int64(x-bounds.Min.X)
			r, g, b, _ := img.At(x, y).RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
			if r8 >= 240 && g8 >= 240 && b8 >= 240 {
				whiteCount++
			}
			gg := int16((int(r8) + int(g8) + int(b8)) / 3)
			gray[idx] = gg
			sum += int64(gg)
			sumSq += int64(gg) * int64(gg)
			if len(colorSet) < distinctColorCardinalityCap {
				colorSet[color.RGBA{r8, g8, b8, 255}] = struct{}{}
			}
		}
	}

	// Edge density: count horizontal transitions where |Δgray| > 30.
	// Pure-white image has every Δ = 0 → edgeCount = 0 → edgeDensity = 0.
	// Real images have structured edges → edgeDensity in (0, 1].
	var edgeCount int64
	for y := 0; y < h; y++ {
		for x := 1; x < w; x++ {
			diff := int(gray[int64(y)*int64(w)+int64(x)]) -
				int(gray[int64(y)*int64(w)+int64(x-1)])
			if diff < 0 {
				diff = -diff
			}
			if diff > 30 {
				edgeCount++
			}
		}
	}

	npf := float64(nPixels)
	whitePct = float64(whiteCount) / npf
	mean := float64(sum) / npf
	variance = float64(sumSq)/npf - mean*mean
	edgeDensity = float64(edgeCount) / npf
	distinctColors = len(colorSet)
	return whitePct, variance, edgeDensity, distinctColors
}

// pHash computes an 8x8 average-hash on the input image. The result is
// a uint64 with bit i = 1 if pixel[i] > image-mean, else 0. Identical
// uniform inputs (e.g. all-white) produce hash = 0. Real images diverge
// by 30-60 bits from the blank reference.
//
// Average-hash is intentionally simpler than DCT-pHash: it's linear-time
// with no float-precision dependencies, and the binary bits are robust
// to JPEG compression artefacts (good enough for fail-closed validation
// of PNG outputs from a stable generator).
func pHash(img image.Image) uint64 {
	const size = 8
	bounds := img.Bounds()
	if bounds.Dx() < size || bounds.Dy() < size {
		return 0
	}
	stepX := bounds.Dx() / size
	stepY := bounds.Dy() / size

	var pixels [size * size]int
	sum := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := bounds.Min.X + x*stepX
			sy := bounds.Min.Y + y*stepY
			r, g, b, _ := img.At(sx, sy).RGBA()
			gray := (int(r>>8) + int(g>>8) + int(b>>8)) / 3
			pixels[y*size+x] = gray
			sum += gray
		}
	}
	mean := sum / (size * size)
	var hash uint64
	for i, p := range pixels {
		if p > mean {
			hash |= 1 << uint(i)
		}
	}
	return hash
}

// referenceBlankHash returns the pHash of any fully-uniform image. Since
// the average-hash algorithm produces hash=0 for any uniform image
// (mean == every pixel), the value is a compile-time constant 0.
//
// This function exists as the forward-seam for an opt-in different
// placeholder reference. Review feedback P0.2: today the all-zero
// reference is correct and stable; if we later choose to fingerprint
// a specific rendered placeholder (e.g. a UI shimmer), we'd swap the
// function body to compute the hash there.
func referenceBlankHash() uint64 { return 0 }

// hammingDistance returns the number of bits that differ between two
// uint64 values. Implementation uses Brian Kernighan's bit-twiddling
// for O(set-bits) speed.
func hammingDistance(a, b uint64) int {
	x := a ^ b
	d := 0
	for x != 0 {
		d++
		x &= x - 1
	}
	return d
}
