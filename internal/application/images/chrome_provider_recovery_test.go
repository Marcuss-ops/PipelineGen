// Package images — chrome_provider_recovery_test.go (P1.1, July 2026):
// wire-level recovery tests for the chrome_provider.go → slide_worker.py
// forwarding contract.
//
// Why this file exists (against extending smoke_test.go):
//   The user P1.1 spec explicitly asks for "Test end-to-end in
//   chrome_provider_recovery_test.go". The 5-prompt smoke + 2-consecutive
//   clean-context + Composer direct-call tests already live in smoke_test.go;
//   adding the 3 P1.1 protocol-extension tests there would bloat an
//   already-large file. Centralizing them in their own file also makes the
//   "what is the wire-level contract today?" question trivially answerable
//   (one file, one protocol surface, one canonical assertion set).
//
// What each test pins:
//   1. TestChromeProvider_NegativePromptForwarded (P1.1 §a) —
//        negative_prompt field is forwarded verbatim in workerReq;
//        the composed prompt contains the canonical P1.2 directive
//        `[negative: do not include ...]`; prompt_original carries the
//        raw user prompt for the worker-side JSONL audit trail.
//
//   2. TestChromeProvider_PromptSuffixForwarded (P1.1 §a + §b) —
//        prompt_suffix field is forwarded verbatim when set; absent
//        when PromptSuffix is empty (zero-value discrimination).
//
//   3. TestChromeProvider_RatioForwarded (P1.1 §c) —
//        ratio field is forwarded verbatim when set; the `id`
//        correlation token (== `request_id` per spec) is always
//        populated. The wire-level key is `id`; chrome_provider.go
//        also emits a `request_id` alias for spec compliance.
//
// These tests reuse the smokeFixture mock from smoke_test.go (same
// package; newSmokeFixture, newSmokeFixture.serveResponses, writeValidPNG
// are all accessible without import). They do NOT depend on Python
// being installed.

package images

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestChromeProvider_NegativePromptForwarded (P1.1 §a) verifies the
// wire-level forwarding of the negative_prompt field plus the canonical
// composed-prompt contract (P1.2 still owns the default format).
//
//	(a) negative_prompt MUST be forwarded verbatim in workerReq.
//	(b) composed prompt MUST contain the canonical negative directive
//	    `[negative: do not include ...]` (P1.2's prompt_composer
//	    produces format; P1.1's wire-level field just threads the raw).
//	(c) prompt_original MUST carry the raw user prompt (P1.2 audit
//	    trail — verifies the worker can later recover the raw form
//	    for JSONL diagnostics).
func TestChromeProvider_NegativePromptForwarded(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "neg_fwd.png")
	writeValidPNG(t, outputPath, 80, 80)

	const negativePrompt = "text, watermark, blurry"
	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":102400,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":22000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	g, err := fix.p.Generate(context.Background(), GenerateImageRequest{
		Prompt:         "a peaceful valley at dawn",
		Style:          "cinematic",
		Width:          1920,
		Height:         1080,
		NegativePrompt: negativePrompt,
		OutputPath:     outputPath,
	})
	if err != nil || g == nil {
		t.Fatalf("P1.1 §a: want accept; got g=%v err=%v", g, err)
	}
	last := fix.lastRequest()
	if last == nil {
		t.Fatal("P1.1 §a: no captured request")
	}

	// (a) negative_prompt field MUST carry the raw user prompt verbatim
	// so the worker can use it for diagnostics / fallback composition.
	gotNP, _ := last["negative_prompt"].(string)
	if gotNP != negativePrompt {
		t.Fatalf("P1.1 §a: negative_prompt not forwarded verbatim; want %q, got %q",
			negativePrompt, gotNP)
	}

	// (b) composed prompt MUST contain the negative directive (P1.2
	// canonical format `[negative: do not include ...]`). If the chrome
	// provider regressed to non-composition, the substring would be
	// absent and the operator would silently lose the keywords.
	gotP, _ := last["prompt"].(string)
	if !strings.Contains(gotP, "[negative: do not include text;watermark;blurry]") {
		t.Fatalf("P1.1 §a: composed prompt missing negative directive; got %q", gotP)
	}

	// (c) prompt_original field MUST carry the raw user prompt (P1.2
	// audit trail — verifies the worker can later recover the raw form).
	gotOrig, _ := last["prompt_original"].(string)
	if gotOrig != "a peaceful valley at dawn" {
		t.Fatalf("P1.1 §a: prompt_original not raw; got %q", gotOrig)
	}
}

// TestChromeProvider_PromptSuffixForwarded (P1.1 §a + §b) verifies the
// wire-level forwarding of the prompt_suffix escape-hatch field plus
// zero-value discrimination.
//
//	(a) prompt_suffix MUST be forwarded verbatim when set.
//	(b) When PromptSuffix is NOT supplied, the field MUST be absent in
//	    the workerReq payload (chrome_provider.go only emits it when
//	    non-empty; the zero-value-discrimination guard prevents noise
//	    fields in the audit log).
func TestChromeProvider_PromptSuffixForwarded(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "suffix_fwd.png")
	writeValidPNG(t, outputPath, 80, 80)

	const rawPrompt = "a starlit desert at night"
	const explicitSuffix = "negative_keywords: avoid text, watermark, blurry"

	// (a) happy path: suffix set → forwarded verbatim.
	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":102400,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":22000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	g, err := fix.p.Generate(context.Background(), GenerateImageRequest{
		Prompt:       rawPrompt,
		Style:        "cinematic",
		Width:        1920,
		Height:       1080,
		PromptSuffix: explicitSuffix,
		OutputPath:   outputPath,
	})
	if err != nil || g == nil {
		t.Fatalf("P1.1 §a+b: want accept; got g=%v err=%v", g, err)
	}
	last := fix.lastRequest()
	if last == nil {
		t.Fatal("P1.1 §a+b: no captured request")
	}
	gotSuffix, ok := last["prompt_suffix"].(string)
	if !ok || gotSuffix != explicitSuffix {
		t.Fatalf("P1.1 §a: prompt_suffix not forwarded verbatim; want %q, got %q (ok=%v)",
			explicitSuffix, gotSuffix, ok)
	}

	// (b) zero-value discrimination: a 2nd fixture with the same prompt
	// but no PromptSuffix should NOT have prompt_suffix in the payload.
	fix2 := newSmokeFixture(t)
	outputPath2 := filepath.Join(t.TempDir(), "no_suffix.png")
	writeValidPNG(t, outputPath2, 80, 80)
	fix2.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":102400,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":22000,"profile":0}`,
			"REPLACE", outputPath2,
		),
	})
	_, err2 := fix2.p.Generate(context.Background(), GenerateImageRequest{
		Prompt: rawPrompt,
		Style:  "cinematic",
		Width:  1920, Height: 1080,
		OutputPath: outputPath2,
	})
	if err2 != nil {
		t.Fatalf("P1.1 §b (zero-value): want accept; got %v", err2)
	}
	last2 := fix2.lastRequest()
	if _, present := last2["prompt_suffix"]; present {
		t.Fatal("P1.1 §b (zero-value): prompt_suffix should NOT be present when empty")
	}
}

// TestChromeProvider_RatioForwarded (P1.1 §c) verifies the wire-level
// forwarding of the ratio override field plus the request_id correlation
// token population.
//
//	(a) ratio MUST be forwarded verbatim when set.
//	(b) The `id` correlation token (= `request_id` per spec) MUST be
//	    populated so the worker can echo it back in the response for
//	    log correlation. Wire-level key is `id`; chrome_provider.go
//	    also emits a `request_id` alias for spec compliance.
func TestChromeProvider_RatioForwarded(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "ratio_fwd.png")
	writeValidPNG(t, outputPath, 80, 80)

	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":102400,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":22000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	g, err := fix.p.Generate(context.Background(), GenerateImageRequest{
		Prompt: "a misty forest with sunlight",
		Style:  "cinematic",
		Width:  1920, Height: 1080,
		Ratio:      "16:9",
		OutputPath: outputPath,
	})
	if err != nil || g == nil {
		t.Fatalf("P1.1 §c: want accept; got g=%v err=%v", g, err)
	}
	last := fix.lastRequest()
	if last == nil {
		t.Fatal("P1.1 §c: no captured request")
	}

	// (a) ratio verbatim.
	gotRatio, ok := last["ratio"].(string)
	if !ok || gotRatio != "16:9" {
		t.Fatalf("P1.1 §c: ratio not forwarded verbatim; want 16:9, got %q (ok=%v)", gotRatio, ok)
	}

	// (b) `id` AND `request_id` are both populated to the same value
	// (chrome_provider.go emits `request_id` as an alias of `id` per
	// the user spec's request_id naming convention).
	idVal, idOK := last["id"].(string)
	if !idOK || idVal == "" {
		t.Fatalf("P1.1 §c: id (request_id) missing; got %q (ok=%v)", idVal, idOK)
	}
	reqIDVal, reqIDOK := last["request_id"].(string)
	if !reqIDOK || reqIDVal != idVal {
		t.Fatalf("P1.1 §c: request_id alias missing or mismatched; id=%q, request_id=%q",
			idVal, reqIDVal)
	}
}

// TestChromeProvider_RequestIDAliasOnly (P1.1, July-29 review-feedback)
// verifies that even when the ratio + prompt_suffix fields are absent
// (default canonical composition), the `request_id` alias IS in the
// payload (it's always emitted, not gated on ratio or suffix presence).
func TestChromeProvider_RequestIDAliasOnly(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "rid_only.png")
	writeValidPNG(t, outputPath, 80, 80)

	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":102400,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":22000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	_, err := fix.p.Generate(context.Background(), GenerateImageRequest{
		Prompt: "a snow-capped peak at sunrise",
		Style:  "watercolor",
		Width:  1920, Height: 1080,
		// No Ratio, no PromptSuffix — zero-value path.
		OutputPath: outputPath,
	})
	if err != nil {
		t.Fatalf("P1.1 (zero-value pass): want accept; got %v", err)
	}
	last := fix.lastRequest()
	if last == nil {
		t.Fatal("P1.1 (zero-value pass): no captured request")
	}
	idVal, _ := last["id"].(string)
	reqIDVal, ok := last["request_id"].(string)
	if !ok || reqIDVal != idVal || reqIDVal == "" {
		t.Fatalf("request_id alias must always be present and equal to id; id=%q, request_id=%q (ok=%v)",
			idVal, reqIDVal, ok)
	}
	if idVal == "" {
		t.Fatal("id correlation token must be non-empty")
	}
}
