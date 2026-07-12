package visual_validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// jsonMarshal is a test-only helper that wraps json.Marshal returning
// ([]byte, error) → ([]byte, error) with no failure mode beyond the
// encoder itself. P2 (July 2026) ComputeStats test contract uses it
// to assert the canonical serde shape.
func jsonMarshal(t *testing.T, v any) ([]byte, error) {
	t.Helper()
	return json.Marshal(v)
}

// Fixture generation: 4 programmatically-built PNGs in t.TempDir()
// (+ vecchiaRiusata as the 5th per P0.2.A spec). We avoid shipping
// binary fixtures in the repo to keep the diff small and to
// deterministically exercise the validator's thresholds.

// writePNG encodes the given image as a PNG at the given path inside
// the test's TempDir and returns the absolute path.
func writePNG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("png.Encode %s: %v", path, err)
	}
	return path
}

// pureWhite: every pixel is (255,255,255,255). The pre-fix blank
// produced by File->Download->PNG over an empty slides.new page
// renders closest to this. Must be REJECTED by Validate for any style.
func pureWhite(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	return img
}

// slideVuoto: solid white with a faint placeholder gradient. Same
// class as pure-white from the validator's perspective. REJECTED.
func slideVuoto(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(240 + (x*15/w)%16)
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

// realImage: a colorful gradient with structured edges. Mimics what
// a generated image looks like in production. ACCEPTED under standard.
//
// Fix for vet.CompileError regression from earlier draft:
// (uint8)((x*255 + y*255)/s) % 256  triggered go's "overflows uint8"
// check because Go promotes the rhs literal 256 to uint8 in this
// context. We now compute on ints and cast once.
func realImage(w, h int, seed int64) *image.RGBA {
	r := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gray := (x*255/w + y*255/h) % 251
			if r.Intn(4) == 0 {
				gray = 250 - gray
			}
			r8 := uint8(gray)
			g8 := uint8(255 - gray)
			b8 := uint8((gray + 128) % 251)
			img.SetRGBA(x, y, color.RGBA{r8, g8, b8, 255})
		}
	}
	return img
}

// whiteboardSketch: predominantly white background with sparse dark
// "ink" pixels. ACCEPTED under whiteboard exception (high white_pct
// but pHash diverges from blank reference). Returned as *image.RGBA so
// .SetRGBA is available — the pre-fix draft returned image.Image which
// has no Set method.
//
// Dual-style contract fixture:
//
//	standard (white_pct > 0.99)                          → REJECTED
//	whiteboard (white_pct <= 0.998 AND pHash dist > 5 +
//	edge_density >= minWhiteboardEdgeDensity)            → ACCEPTED
//
// For 80x80 = 6400 pixels, 54 ink pixels (six 3x3 blocks) → wp = 0.9916
// (= 1 - 54/6400), in the open interval (0.99, 0.998]. The blocks
// are centered on pHash sample points (size=8 grid: x,y in
// {0,10,20,...,70}); each block flips a bit in the 8x8 hash, giving
// distance >= 6 (> 5). The 3x3 blocks also contribute horizontal-edge
// transitions (~6 transitions per block: 3 rows × 2 transitions per
// row), totalling 36 edges / 6400 pixels = 0.00562 edge_density,
// comfortably above the 0.001 floor.
func whiteboardSketch(w, h int) *image.RGBA {
	img := pureWhite(w, h)
	samplePoints := []struct{ cx, cy int }{
		{0, 0}, {10, 10}, {20, 20}, {30, 30}, {40, 40}, {50, 50},
	}
	for _, p := range samplePoints {
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				x, y := p.cx+dx, p.cy+dy
				if x >= 0 && x < w && y >= 0 && y < h {
					img.SetRGBA(x, y, color.RGBA{25, 25, 25, 255})
				}
			}
		}
	}
	return img
}

// vecchiaRiusata: P0.2.A 5th spec fixture. Simulates a stale-cached
// image hit (old image reused from a previous caching layer).
// Mostly-white background with a HORIZONTAL ROW of dark pixels at
// y=0. The row flips the entire y=0 sample row of pHash bits (8 of 8
// = distance 8 > 5 → passes pHash check). The row is uniform within
// itself (all dark) → ZERO horizontal-edge transitions → edge_density
// = 0 → FAILS the new whiteboard edge-density floor.
//
// Net effect under StyleWhiteboard: REJECTED on edge_density floor.
// This proves the P0.2.A floor catches the stale-reuse cache-hit
// variant that the legacy carve-out would have silently accepted.
//
// Properties:
//   - white_pct ~0.9875 (≤ 0.998 → passes whiteboard carve-out)
//   - variance ≫ 5
//   - pHash distance = 8 (> 5 → passes pHash check)
//   - edge_density = 0 (< 0.001 → fails new whiteboard floor)
func vecchiaRiusata(w, h int) *image.RGBA {
	img := pureWhite(w, h)
	for x := 0; x < w; x++ {
		img.SetRGBA(x, 0, color.RGBA{10, 10, 10, 255})
	}
	return img
}

// ── Tests ──────────────────────────────────────────────────────────────────

func TestValidate_PureWhite_RejectStandard(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir, "pure-white.png", pureWhite(80, 80))

	err := Validate(path, "")
	if err == nil {
		t.Fatal("expected error on pure-white PNG with standard style, got nil")
	}
	if !errors.Is(err, ErrBlankOrPlaceholder) {
		t.Fatalf("expected ErrBlankOrPlaceholder, got %v", err)
	}
}

func TestValidate_PureWhite_StillRejectsAsWhiteboard(t *testing.T) {
	// Pure white is so blank that even whiteboard exception catches it
	// (phash distance <= 5).
	dir := t.TempDir()
	path := writePNG(t, dir, "pure-white.png", pureWhite(80, 80))

	err := Validate(path, StyleWhiteboard)
	if err == nil {
		t.Fatal("expected error on pure-white even with whiteboard style")
	}
	if !errors.Is(err, ErrBlankOrPlaceholder) {
		t.Fatalf("expected ErrBlankOrPlaceholder, got %v", err)
	}
}

func TestValidate_SlideVuoto_Rejected(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir, "slide-vuoto.png", slideVuoto(80, 80))

	err := Validate(path, "")
	if err == nil {
		t.Fatal("expected error on near-white slide-vuoto PNG")
	}
	if !errors.Is(err, ErrBlankOrPlaceholder) {
		t.Fatalf("expected ErrBlankOrPlaceholder, got %v", err)
	}
}

func TestValidate_RealImage_Accepted(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir, "valid.png", realImage(80, 80, 1))

	err := Validate(path, "")
	if err != nil {
		t.Fatalf("real image should validate, got %v", err)
	}
}

func TestValidate_WhiteboardSketch_Accepted(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir, "whiteboard.png", whiteboardSketch(80, 80))

	// The canvas is white-dominated but the ink lines make pHash diverge.
	err := Validate(path, StyleWhiteboard)
	if err != nil {
		t.Fatalf("whiteboard sketch should validate under whiteboard exception, got %v", err)
	}
}

func TestValidate_WhiteboardSketch_Rejected_AsStandard(t *testing.T) {
	// Same sketch under standard style: likely rejected (white_pct > 0.99
	// since most pixels are white) — proves the style gating matters.
	dir := t.TempDir()
	path := writePNG(t, dir, "whiteboard.png", whiteboardSketch(80, 80))

	err := Validate(path, "")
	if err == nil {
		t.Fatal("whiteboard sketch under standard style should reject (too much white)")
	}
	if !errors.Is(err, ErrBlankOrPlaceholder) {
		t.Fatalf("expected ErrBlankOrPlaceholder, got %v", err)
	}
}

// P0.2.A: vecchia-riusata fixture (5th spec PNG). Under StyleWhiteboard
// the legacy carve-out would have silently accepted this image because
// it passes the white_pct and pHash + variance checks. The new
// minWhiteboardEdgeDensity floor catches it because its edge_density
// is 0 (uniform horizontal row of dark pixels has zero horizontal
// transitions). This is the regression test that PROVES the floor
// is wired and reachable.
func TestValidate_VecchiaRiusata_RejectedAsWhiteboard_EdgeDensityFloor(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir, "vecchia-riusata.png", vecchiaRiusata(80, 80))

	err := Validate(path, StyleWhiteboard)
	if err == nil {
		t.Fatal("vecchia riusata under StyleWhiteboard must reject (edge_density floor)")
	}
	if !errors.Is(err, ErrBlankOrPlaceholder) {
		t.Fatalf("expected ErrBlankOrPlaceholder, got %v", err)
	}
}

func TestValidate_FileMissing(t *testing.T) {
	err := Validate("/nonexistent/path/file.png", "")
	if err == nil || errors.Is(err, ErrBlankOrPlaceholder) {
		t.Fatalf("expected open error (not ErrBlankOrPlaceholder), got %v", err)
	}
}

func TestPHash_DeterministicForSameInput(t *testing.T) {
	img := realImage(64, 64, 42)
	a := pHash(img)
	b := pHash(img)
	if a != b {
		t.Fatalf("pHash not deterministic: %d vs %d", a, b)
	}
}

func TestHammingDistance_SymmetricAndZeroOnIdentical(t *testing.T) {
	if d := hammingDistance(0xff, 0xff); d != 0 {
		t.Fatalf("identical inputs should give distance 0; got %d", d)
	}
	if d := hammingDistance(0xff, 0x00); d != 8 {
		t.Fatalf("opposite bytes should give distance 8; got %d", d)
	}
	if d := hammingDistance(0xaa, 0x55); d != 8 {
		t.Fatalf("alternating bits should give distance 8; got %d", d)
	}
}

// ── ComputeStats (P2, July 2026) ────────────────────────────────────────

func TestComputeStats_PureWhite_AllZeroSignal(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir, "pure-white.png", pureWhite(80, 80))

	stats, err := ComputeStats(path)
	if err != nil {
		t.Fatalf("ComputeStats: %v", err)
	}
	if stats.Width != 80 || stats.Height != 80 {
		t.Fatalf("dims: want 80x80; got %dx%d", stats.Width, stats.Height)
	}
	if stats.WhitePct != 1.0 {
		t.Fatalf("white_pct: want 1.0; got %.4f", stats.WhitePct)
	}
	if stats.Variance != 0 {
		t.Fatalf("variance: want 0; got %.2f", stats.Variance)
	}
	if stats.EdgeDensity != 0 {
		t.Fatalf("edge_density: want 0 (uniform input); got %.4f", stats.EdgeDensity)
	}
	if stats.DistinctColors != 1 {
		t.Fatalf("distinct_colors: want 1 (single RGBA value); got %d", stats.DistinctColors)
	}
	if stats.PHashHex != "0000000000000000" {
		t.Fatalf("phash_hex: want all-zero (uniform input); got %q", stats.PHashHex)
	}
}

func TestComputeStats_RealImage_NonZeroSignal(t *testing.T) {
	// realImage renders vibrant colour-spread pixels (r=0-250, g=5-255,
	// b=0-250). The "near-white" sentinel is r>=240 AND g>=240 AND b>=240;
	// because g is always << 240 when r is high, realImage legitimately
	// has white_pct == 0 (no pixel has ALL THREE channels near-white).
	// The CANONICAL "non-blank" signals for realImage are variance +
	// edge_density + pHash-divergence — NOT white_pct. This test verifies
	// the structured-image signal; the inverse (blank highlights) is
	// pinned by TestComputeStats_PureWhite_AllZeroSignal.
	dir := t.TempDir()
	path := writePNG(t, dir, "real.png", realImage(80, 80, 7))

	stats, err := ComputeStats(path)
	if err != nil {
		t.Fatalf("ComputeStats: %v", err)
	}
	if stats.Width != 80 || stats.Height != 80 {
		t.Fatalf("dims: want 80x80; got %dx%d", stats.Width, stats.Height)
	}
	// white_pct may be 0 (no near-white pixel) or any value in [0, 1];
	// strictly neither below 0 nor above 1, but no lower bound.
	if stats.WhitePct < 0 || stats.WhitePct > 1 {
		t.Fatalf("white_pct: want [0, 1] sanity bound; got %.4f", stats.WhitePct)
	}
	if stats.Variance <= 100 {
		t.Fatalf("variance: want >>100 for structured real image; got %.2f", stats.Variance)
	}
	if stats.EdgeDensity <= 0 {
		t.Fatalf("edge_density: want >>0 for structured real image; got %.4f", stats.EdgeDensity)
	}
	// P0.2.A: distinct_colors must be populated and bounded.
	if stats.DistinctColors <= 0 {
		t.Fatalf("distinct_colors: want >0 (real image has many RGBA values); got %d", stats.DistinctColors)
	}
	if stats.DistinctColors > distinctColorCardinalityCap {
		t.Fatalf("distinct_colors: want <=%d (anti-OOM cap); got %d",
			distinctColorCardinalityCap, stats.DistinctColors)
	}
	if stats.PHashHex == "0000000000000000" {
		t.Fatalf("phash_hex: want non-zero (real image never has uniform pHash); got %q", stats.PHashHex)
	}
}

func TestComputeStats_MarshalJSON_OmitsPrivateField(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir, "real.png", realImage(80, 80, 11))

	stats, err := ComputeStats(path)
	if err != nil {
		t.Fatalf("ComputeStats: %v", err)
	}
	data, err := jsonMarshal(t, stats)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The 7 canonical Stats fields should be present (P0.2.A added
	// distinct_colors).
	for _, field := range []string{`"width"`, `"height"`, `"white_pct"`, `"variance"`, `"edge_density"`, `"distinct_colors"`, `"phash_hex"`} {
		if !bytes.Contains(data, []byte(field)) {
			t.Fatalf("Marshaled Stats missing field %s: %s", field, string(data))
		}
	}
	// The private pHash bit-vector MUST NOT leak into the JSON
	// (defensive: a leaked field would let callers forge stats).
	if bytes.Contains(data, []byte(`"private_phash"`)) || bytes.Contains(data, []byte(`"PrivatePhash"`)) {
		t.Fatalf("Marshaled Stats leaked private pHash bit-vector: %s", string(data))
	}
}

func TestComputeStats_DeterministicAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir, "real.png", realImage(80, 80, 13))

	a, err := ComputeStats(path)
	if err != nil {
		t.Fatalf("ComputeStats #1: %v", err)
	}
	b, err := ComputeStats(path)
	if err != nil {
		t.Fatalf("ComputeStats #2: %v", err)
	}
	if a != b {
		t.Fatalf("ComputeStats not deterministic across calls:\n  #1=%+v\n  #2=%+v", a, b)
	}
}
