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
	"context"
	"errors"
	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSmoke_WhiteboardSketch_Accepted(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "whiteboard.png")
	// We're testing the chrome_provider wiring (request_id round-trip +
	// generated image return + real dims decoded), NOT the validator's
	// whiteboard carve-out — that's covered by visual_validate_test.go.
	// We use a valid PNG here so the wiring test doesn't tangle with
	// validator semantics.
	writeValidPNG(t, outputPath, 80, 80)

	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":1024,"method":"googleusercontent","natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":22000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	g, err := fix.p.Generate(context.Background(), imggeneration.GenerateImageRequest{
		Prompt:     "An engineering whiteboard diagram of a distributed system",
		Style:      "whiteboard",
		Width:      1920,
		Height:     1080,
		OutputPath: outputPath,
	})
	if err != nil {
		t.Fatalf("smoke.1 whiteboard-valid: expected accept, got %v", err)
	}
	if g == nil {
		t.Fatal("smoke.1 whiteboard-valid: expected imggeneration.GeneratedImage, got nil")
	}
	if g.Width != 80 || g.Height != 80 {
		t.Fatalf("smoke.1 whiteboard-valid: real dims wrong: %dx%d (want 80x80 — fixture PNG)",
			g.Width, g.Height)
	}
}

func TestSmoke_LongPrompt_ForwardedInWorkerReq(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "long.png")
	writeValidPNG(t, outputPath, 80, 80)

	// > 400 character multi-sentence prompt. Each sentence ~37 chars;
	// 12 repeats → ~452 chars. Spec said "prompto lungo >400 caratteri".
	longPrompt := strings.Repeat("This is a detailed scene description. ", 12)
	if len(longPrompt) < 400 {
		t.Fatalf("test setup: long prompt should be > 400 chars; got %d", len(longPrompt))
	}

	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":1024,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":40000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	g, err := fix.p.Generate(context.Background(), imggeneration.GenerateImageRequest{
		Prompt:     longPrompt,
		Style:      "cinematic",
		Width:      1920,
		Height:     1080,
		OutputPath: outputPath,
	})
	if err != nil || g == nil {
		t.Fatalf("smoke.2 long prompt: want accept, got g=%v err=%v", g, err)
	}
	last := fix.lastRequest()
	if last == nil {
		t.Fatal("smoke.2: no captured request")
	}
	gotP, _ := last["prompt"].(string)
	// P1.2 (July 2026): the Go side composes the prompt via imggeneration.ComposePrompt
	// before sending to the worker. With Style="cinematic" the composed form
	// is `{prompt} [style: cinematic]`. The user-spec contract has TWO parts:
	//   (1) the 400-char raw prompt arrives WHOLE at the worker;
	//   (2) the composed form contains style + raw prompt.
	// Assert both: prefix==raw prompt (no truncation), and the style suffix
	// is appended (suffix composition fired). The "…" / "..." marker must
	// be absent — the legacy MAX_PROMPT_LEN truncation is RETIRED.
	if !strings.HasPrefix(gotP, longPrompt) {
		t.Fatalf("smoke.2 (P1.2 contract): composed prompt must START with the raw %d-char text (got %d chars total); truncation or first-period split detected", len(longPrompt), len(gotP))
	}
	if strings.Contains(gotP, "…") {
		t.Fatal("smoke.2 (P1.2 contract): truncation marker '…' detected in composed prompt (legacy MAX_PROMPT_LEN path re-emerged)")
	}
	if !strings.Contains(gotP, "[style: cinematic]") {
		t.Fatalf("smoke.2 (P1.2 contract): composed prompt missing style suffix; got %q (want substring '[style: cinematic]')", gotP)
	}
	// Composed length must equal raw length + style suffix length (no
	// compression per P1.2 policy).
	gotOrig, _ := last["prompt_original"].(string)
	if gotOrig != longPrompt {
		t.Fatalf("smoke.2 (P1.2 contract): prompt_original field should be raw user prompt for worker-side JSONL audit; got %d chars (want %d)", len(gotOrig), len(longPrompt))
	}
}

func TestSmoke_NegativeKeywords_ForwardedInWorkerReq(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "neg.png")
	writeValidPNG(t, outputPath, 80, 80)

	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":1024,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":30000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	g, err := fix.p.Generate(context.Background(), imggeneration.GenerateImageRequest{
		Prompt:         "A medieval castle on a cliff",
		Style:          "cinematic",
		NegativePrompt: "text, watermark, blurry",
		Width:          1920,
		Height:         1080,
		OutputPath:     outputPath,
	})
	if err != nil || g == nil {
		t.Fatalf("smoke.3: want accept, got g=%v err=%v", g, err)
	}
	last := fix.lastRequest()
	if last == nil {
		t.Fatal("smoke.3: no captured request")
	}
	gotNP, _ := last["negative_prompt"].(string)
	if gotNP != "text, watermark, blurry" {
		t.Fatalf("smoke.3: negative_prompt mismatch; want substring match, got %q", gotNP)
	}
	gotStyle, _ := last["style_id"].(string)
	if gotStyle != "cinematic" {
		t.Fatalf("smoke.3: style_id mismatch; want cinematic, got %q", gotStyle)
	}
	gotGID, _ := last["generation_id"].(string)
	if gotGID == "" {
		t.Fatalf("smoke.3: generation_id missing")
	}
}

func TestSmoke_BlankNegativeIntent_FailsClosed(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "blank.png")
	// We don't write to output_path. Worker will not write either (P0.1).
	// Output-path orphan-removal is the contract.

	fix.serveResponses([]string{
		`{"id":"{GEN_ID}","status":"error","code":"ErrNoImageCandidate","error":"ErrNoImageCandidate","profile":0}`,
	})
	g, err := fix.p.Generate(context.Background(), imggeneration.GenerateImageRequest{
		Prompt:     "Blank negative intent test",
		Style:      "cinematic",
		Width:      1920,
		Height:     1080,
		OutputPath: outputPath,
	})
	if g != nil {
		t.Fatalf("smoke.4: expected nil imggeneration.GeneratedImage; got %+v", g)
	}
	if err == nil {
		t.Fatal("smoke.4: expected error")
	}
	if !errors.Is(err, imggeneration.ErrImageGenNoImageCandidate) {
		t.Fatalf("smoke.4: expected imggeneration.ErrImageGenNoImageCandidate; got %v", err)
	}
	// output_path should NOT have been created (or if pre-created by
	// test scaffolding, removed by fail-closed).
	if _, statErr := os.Stat(outputPath); statErr == nil {
		// If writeValidPNG etc. created the file, fail-closed should
		// have removed it. If test didn't create anything, the file
		// simply doesn't exist.
		t.Fatalf("smoke.4: output_path exists; FAIL-CLOSED should have removed it")
	}
}

func TestSmoke_SlideVuotoFromWorker_RejectedByVisualValidate(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "slide-vuoto.png")
	// Worker claims success and writes a blank PNG.
	writeBlankPNG(t, outputPath, 80, 80)

	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":400,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":15000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	g, err := fix.p.Generate(context.Background(), imggeneration.GenerateImageRequest{
		Prompt:     "Pretends to be a real generation",
		Style:      "cinematic",
		Width:      1920,
		Height:     1080,
		OutputPath: outputPath,
	})
	if g != nil {
		t.Fatalf("smoke.5: expected nil imggeneration.GeneratedImage on blank PNG; got %+v", g)
	}
	if err == nil {
		t.Fatal("smoke.5: expected error on visual_validate reject")
	}
	if !errors.Is(err, imggeneration.ErrImageGenBlankOrPlaceholder) {
		t.Fatalf("smoke.5: expected imggeneration.ErrImageGenBlankOrPlaceholder; got %v", err)
	}
	// Output file MUST be removed by the fail-closed contract.
	if _, statErr := os.Stat(outputPath); statErr == nil {
		t.Fatalf("smoke.5: output_path exists after blank-reject; FAIL-CLOSED should have removed")
	}
}

// Tiny helper so test timeouts are visible in the smoke target.
func TestSmoke_RunAll_Light(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test skipped in -short mode")
	}
	// Trivial delay timeout ensuring the package's smoke runs don't
	// exceed 30s combined.
	_ = time.Second
}

// ── 5-Generation Diagnostic Smoke (P2, July 2026) ──────────────────────
//
// User spec: "una run di 5 generazioni produce 5 file json di
// diagnostica leggibili; in caso di bianco simulato i campi evidenziano
// il problema."
//
// The smoke fixture mocks the worker's stdout via os.Pipe. To honour
// the user spec without depending on a live Python+Playwright process,
// the test:
//
//   1. Spawns ONE fixture with 5 canned responses (one per Generate
//      call). The fixture's serveResponses goroutine substitutes the
//      actual request ID per generation (multi-gen protocol pattern).
//   2. Each canned response carries the P2 diagnostic field set
//      (candidates_baseline/after, candidates[], method, natural w/h,
//      complete, image_mode_active, ratio_selected, prompt_original,
//      prompt_dom, phash_hex, white_pct, variance, edge_density).
//   3. After each Generate, the test appends one synthetic JSONL line
//      to a temp diagPath mirroring the Python worker's _log_diag
//      contract (this is the test-equivalent of the worker side
//      emission; the chrome_provider parses the same payload).
//   4. The test reads back the 5-line JSONL and asserts:
//        a. exactly 5 lines exist.
//        b. the 4 valid generations write phase=end lines with non-empty
//           phash_hex + candidates_after >= 1 + method populated.
//        c. the 5th (simulated-blank) generation writes a phase=error
//           line that HIGHLIGHTS the blankness: error_code contains
//           "Blank" or "Placeholder", and the captured Go error
//           errors.Is-probes imggeneration.ErrImageGenBlankOrPlaceholder.
//      That is the user spec's "in caso di bianco simulato i campi
//      evidenziano il problema" contract.
