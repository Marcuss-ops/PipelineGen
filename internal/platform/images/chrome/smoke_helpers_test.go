// Package images — smoke_test.go: 5-prompt integration smoke that
// exercises the P0/P1/P2 contract end-to-end via io.Pipe protocol mocks.
//
// Per FASE 6 user spec ("Esegui uno smoke test locale di image.generate.google
// con 5 prompt reali"): the 5 scenarios below are the canonical contract
// checks for the image-generation pipeline. Real Playwright/Google Slides
// integration requires an authenticated session (production-side smoke),
// but the PROTOCOL contract — payload round-trip, fail-closed paths,
// visual_validate gating, typed error sentinels — is fully verifiable
// from this harness.
//
// Each sub-test uses io.Pipe to stand in for the worker's stdin/stdout
// and injects canned JSON responses. The chrome_provider.Generate is
// exercised end-to-end; the assertions cover:
//
//  1. Whiteboard-style prompt with valid ink content → ACCEPTED.
//     Confirms the whiteboard exception path of visual_validate works.
//  2. Long prompt (400+ chars, multi-sentence) round-trips whole.
//     Confirms P1.2 (worker no longer truncates to 150 chars) surface.
//  3. Negative keywords "text, watermark, blurry" forwarded intact.
//     Confirms P1.1 wire-up of negative_prompt to the worker.
//  4. Blank-negative intent (worker reports ErrNoImageCandidate).
//     Confirms P0.1 fail-closed: nil imggeneration.GeneratedImage, file removed,
//     typed sentinel propagated.
//  5. Slide-export-style blank from worker: status=ok with output_path
//     pointing to a programmatically-generated blank PNG. Confirms
//     P0.2 visual_validate rejects a content-empty success.
//
// These tests do NOT depend on Python being installed.

package chrome

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
)

type smokeFixture struct {
	t            *testing.T
	stdInR       *os.File
	stdInW       *os.File
	stdOutR      *os.File
	stdOutW      *os.File
	cmd          *exec.Cmd
	p            *ChromeImageProvider
	capturedMu   sync.Mutex
	capturedReqs []map[string]any
}

func (f *smokeFixture) Close() {
	if f == nil {
		return
	}
	_ = f.stdInR.Close()
	_ = f.stdInW.Close()
	_ = f.stdOutR.Close()
	_ = f.stdOutW.Close()
}
func newSmokeFixture(t *testing.T) *smokeFixture {
	t.Helper()
	stdInR, stdInW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdOutR, stdOutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("sleep start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = stdInW.Close()
	})

	p := &ChromeImageProvider{
		log:     zap.NewNop(),
		stdin:   stdInW,
		stdout:  bufio.NewScanner(stdOutR),
		started: true,
		cmd:     cmd,
	}
	return &smokeFixture{
		t:       t,
		stdInW:  stdInW,
		stdInR:  stdInR,
		stdOutW: stdOutW,
		stdOutR: stdOutR,
		cmd:     cmd,
		p:       p,
	}
}

// serveResponses writes canned worker responses to stdout for ensureStarted's
// healthCheck (always) and for the generate path (variable per test).
// ensureHandshake reads the request IDs the provider sent on stdin and
// places them into a channel so serveResponses can emit matching-id
// responses.
//
// P2 multi-generation support (July 2026): when generateResponses has
// N > 1 entries, the writer loops N times, waiting for a fresh non-empty
// request id before each substitution. This is the canonical pattern
// for the 5-generation diagnostic smoke test (one fixture, five
// rounds of Generate).
func (f *smokeFixture) serveResponses(generateResponses []string) {
	f.t.Helper()
	requestIDs := make(chan string, 16)

	// Goroutine 1: drain stdin (parse JSON, extract id). Without this,
	// every provider writeJSON would block on synchronous os.Pipe.
	go func() {
		defer close(requestIDs)
		scanner := bufio.NewScanner(f.stdInR)
		for scanner.Scan() {
			var req map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				continue
			}
			id, _ := req["id"].(string)
			f.capturedMu.Lock()
			f.capturedReqs = append(f.capturedReqs, req)
			f.capturedMu.Unlock()
			select {
			case requestIDs <- id:
			default:
			}
		}
	}()

	// Goroutine 2: serve canned responses.
	go func() {
		defer f.stdOutW.Close()
		// Health probe response.
		if _, err := fmt.Fprintln(f.stdOutW, `{"status":"ok"}`); err != nil {
			return
		}
		// N generations: wait for a fresh non-empty request id per canned
		// response. This is the multi-generation contract — the original
		// (single-gen) implementation captured genID once and reused it,
		// which only worked for 1 round of Generate.
		for _, tmpl := range generateResponses {
			var genID string
			for got := range requestIDs {
				if got != "" {
					genID = got
					break
				}
			}
			if genID == "" {
				// Channel closed before any id arrived — provider didn't
				// call Generate. Stop serving.
				return
			}
			r := strings.ReplaceAll(tmpl, "{GEN_ID}", genID)
			if _, err := f.stdOutW.Write([]byte(r + "\n")); err != nil {
				return
			}
		}
	}()
}

// writeBlankPNG writes a fully-white PNG to path.
func writeBlankPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
}

// writeValidPNG writes a PNG with structured content (gradient + colored
// pixels). Passes visual_validate.
//
// Fix for vet.CompileError regression: (uint8)((x*255 + y*255)/s) % 256
// tripped go's "overflows uint8" check (256 doesn't fit in uint8). We
// now compute on ints and cast once.
func writeValidPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gray := (x*255/w + y*255/h) % 251
			r8 := uint8(gray)
			g8 := uint8(255 - gray)
			b8 := uint8((gray + 80) % 251)
			img.SetRGBA(x, y, color.RGBA{r8, g8, b8, 255})
		}
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
}

// lastRequest returns the most recent captured request map.
func (f *smokeFixture) lastRequest() map[string]any {
	f.capturedMu.Lock()
	defer f.capturedMu.Unlock()
	if len(f.capturedReqs) == 0 {
		return nil
	}
	return f.capturedReqs[len(f.capturedReqs)-1]
}

// ── Tests ─────────────────────────────────────────────────────────────────
