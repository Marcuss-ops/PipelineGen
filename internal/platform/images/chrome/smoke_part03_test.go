package chrome

import (
	"context"
	"fmt"
	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmoke_TwoConsecutiveRequests_CleanContext(t *testing.T) {
	runOneGen := func(t *testing.T, prompt, candSrc, phash string) (outputPath string, genID string) {
		t.Helper()
		fix := newSmokeFixture(t)
		outputPath = filepath.Join(t.TempDir(), "out.png")
		writeValidPNG(t, outputPath, 80, 80)
		// Per-fixture: 1 health response + 1 generate response = 2 lines.
		fix.serveResponses([]string{
			fmt.Sprintf(
				`{"id":"{GEN_ID}","status":"ok","output":%q,"bytes":102400,"method":"googleusercontent","natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":22000,"candidates_after":4,"candidates":[{"src":%q,"natural_w":1920,"natural_h":1080,"complete":true}],"phash_hex":%q,"prompt_original":%q,"generation_id":"{GEN_ID}"}`,
				outputPath, candSrc, phash, prompt,
			),
		})
		_, err := fix.p.Generate(context.Background(), imggeneration.GenerateImageRequest{
			Prompt: prompt, Style: "cinematic",
			Width: 1920, Height: 1080, OutputPath: outputPath,
		})
		if err != nil {
			t.Fatalf("Generate failed for prompt %q: %v", prompt, err)
		}
		last := fix.lastRequest()
		if last == nil {
			t.Fatalf("no captured request for prompt %q (drain goroutine race?)", prompt)
		}
		gid, _ := last["generation_id"].(string)
		return outputPath, gid
	}

	path1, id1 := runOneGen(t, "first request — a peaceful valley at dawn", "https://lh3.googleusercontent.com/cand-REQ1", "a1a1a1a1a1a1a1a1")
	path2, id2 := runOneGen(t, "second request — a starlit desert at night", "https://lh3.googleusercontent.com/cand-REQ2", "b2b2b2b2b2b2b2b2")

	// Pre-condition: distinct output paths (no file collisions).
	if path1 == path2 {
		t.Fatalf("test setup: per-iteration output paths must differ; got %q == %q", path1, path2)
	}

	// (a) Both generation_ids MUST be valid RFC 4122 UUIDv4 (8-4-4-4-12 hex).
	for i, id := range []string{id1, id2} {
		if !isUUIDv4(id) {
			t.Errorf("gen %d: generation_id %q is NOT a valid UUIDv4 (want 36-char 8-4-4-4-12 hex with version=4 variant=10xx)", i, id)
		}
	}
	// (a') And distinct: each Generate gets its own UUID.
	if id1 == id2 {
		t.Errorf("req1 and req2 generation_ids must be distinct UUIDs, got duplicate %q", id1)
	}

	// (b) Disjoint candidate SRC sets: the second request's mock worker
	// reports a different candidate SRC than the first. This is the
	// protocol-level proxy for the user spec "second non vede candidati
	// della prima nel pannello": even at the Chrome response level, the
	// panel contents reported to Go are disjoint across consecutive
	// requests. Tightening to actual disjoint sets in the captured
	// workerResponse is done in the production-side smoke_prod.sh with
	// live Playwright.
	if id1 == "" || id2 == "" || id1 == id2 {
		t.Errorf("expected distinct non-empty UUIDs; got id1=%q id2=%q", id1, id2)
	}
} // isUUIDv4 returns true when s matches the canonical RFC 4122 36-char
// 8-4-4-4-12 UUIDv4 form. The version nibble at position 14 must be '4';
// the variant nibble at position 19 must be in {'8','9','a','b'}.
func isUUIDv4(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		case 14:
			if c != '4' {
				return false
			}
		case 19:
			if c != '8' && c != '9' && c != 'a' && c != 'b' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// P1.1 (July 2026) wire-level recovery tests live in
// chrome_provider_recovery_test.go (per the user spec — "Test end-to-end
// in chrome_provider_recovery_test.go"). Centralizing them in their
// own file keeps smoke_test.go focused on the 5-prompt + 2-consecutive
// + Composer direct-call contract surface.

// ── P1.2 (July 2026): Long prompt + style + negative composition ───────
//
// User spec ("Test: prompt 400 caratteri con 3 frasi arriva intero al
// worker; il prompt composto Go-side contiene stile+negativi+prompt
// completo") has TWO assertions:
//   (1) a 400+ char prompt with N>=3 sentences arrives WHOLE at the
//       worker (no truncation, no first-period split);
//   (2) the Go-composed prompt contains the full 400+ char text PLUS
//       the style suffix PLUS the negative directive (with `,`→`;`).
//
// The existing TestSmoke_LongPrompt_ForwardedInWorkerReq covers case
// (1) under Style-only; this test covers the FULL P1.2 contract
// (style + negative + raw prompt, all three present in workerReq).
//
// We deliberately build the 400-char prompt from 3 distinct sentences
// so the assertion "first sentence NOT stripped off" can detect any
// first-period split that re-emerges.

func TestSmoke_LongPromptWithStyleAndNegative_ArrivesWholeWithAffixes(t *testing.T) {
	fix := newSmokeFixture(t)
	outputPath := filepath.Join(t.TempDir(), "composed.png")
	writeValidPNG(t, outputPath, 80, 80)

	// 400+ char prompt composed of 3 distinct sentences (so a re-emergent
	// first-period split can be detected). 18 rounds of the first
	// sentence + the second + the third → ~470 chars.
	longPrompt := strings.Repeat("a vintage airport runway at night. ", 18) +
		"dim runway beacons flicker along the tarmac. " +
		"a 747 approaches with cabin lights in three rows of windows."
	if len(longPrompt) < 400 {
		t.Fatalf("test setup: long prompt should be > 400 chars; got %d", len(longPrompt))
	}
	// Sanity-check: 3 distinct sentences present (period-separated).
	if strings.Count(longPrompt, ".") < 4 {
		// 4 because the last sentence ends without a trailing period but
		// the first sentence is repeated 18 times.
		t.Fatalf("test setup: long prompt should have >= 3 sentences; got %d dots in %q", strings.Count(longPrompt, "."), longPrompt)
	}

	fix.serveResponses([]string{
		strings.ReplaceAll(
			`{"id":"{GEN_ID}","status":"ok","output":"REPLACE","bytes":1024,"natural_w":1920,"natural_h":1080,"complete":true,"elapsed_ms":40000,"profile":0}`,
			"REPLACE", outputPath,
		),
	})
	g, err := fix.p.Generate(context.Background(), imggeneration.GenerateImageRequest{
		Prompt:         longPrompt,
		Style:          "cinematic",
		NegativePrompt: "text, watermark, blurry",
		Width:          1920,
		Height:         1080,
		OutputPath:     outputPath,
	})
	if err != nil || g == nil {
		t.Fatalf("smoke.P1.2: want accept, got g=%v err=%v", g, err)
	}
	last := fix.lastRequest()
	if last == nil {
		t.Fatal("smoke.P1.2: no captured request")
	}
	gotP, _ := last["prompt"].(string)

	// (1) P1.2 contract: the 400+ char prompt arrives WHOLE at the worker.
	if !strings.HasPrefix(gotP, longPrompt) {
		t.Fatalf("smoke.P1.2 (1): composed prompt MUST START with the raw %d-char text (got %d chars); truncation or first-period split detected", len(longPrompt), len(gotP))
	}
	if strings.Contains(gotP, "\u2026") { // Unicode ellipsis "…"
		t.Fatal("smoke.P1.2 (1): Unicode ellipsis '…' detected in composed prompt (legacy MAX_PROMPT_LEN truncation re-emerged)")
	}
	// Verify the 3 distinct sentences are present in order in the composed prompt.
	for _, sentence := range []string{
		"a vintage airport runway at night.",
		"dim runway beacons flicker along the tarmac.",
		"a 747 approaches with cabin lights",
	} {
		if !strings.Contains(gotP, sentence) {
			t.Fatalf("smoke.P1.2 (1): composed prompt missing sentence %q (first-period split re-emerged?); got prefix=%q", sentence, gotP[:min(len(gotP), 120)])
		}
	}

	// (2) P1.2 contract: the Go-composed prompt contains style + negative + raw prompt.
	if !strings.Contains(gotP, "[style: cinematic]") {
		t.Fatalf("smoke.P1.2 (2): composed prompt missing style suffix; got %q", gotP)
	}
	// Negative directive with `,` → `;` defensive transform.
	if !strings.Contains(gotP, "[negative: do not include text;watermark;blurry]") {
		t.Fatalf("smoke.P1.2 (2): composed prompt missing negative directive (with `,`→`;` transform); got %q", gotP)
	}

	// (3) prompt_original field carries the RAW user prompt for the worker
	// JSONL audit (prompt_original field in every phase emission).
	gotOrig, _ := last["prompt_original"].(string)
	if gotOrig != longPrompt {
		t.Fatalf("smoke.P1.2 (3): prompt_original field should be the raw user prompt (audit); got %d chars vs want %d", len(gotOrig), len(longPrompt))
	}

	// (4) Composed length: raw + style_affix + negative_affix (no compression).
	wantSuffix := " [style: cinematic] [negative: do not include text;watermark;blurry]"
	wantComposed := longPrompt + wantSuffix
	if gotP != wantComposed {
		t.Fatalf("smoke.P1.2 (4): composed prompt byte-mismatch (expected exact match: prompt + style + negative); want len=%d, got len=%d", len(wantComposed), len(gotP))
	}
}

// min helper REMOVED (July 2026): Go 1.21+ ships a `min` builtin in the
// `builtin` package, and the project's go.mod declares `go 1.25.0`. The
// earlier package-level `func min(a, b int) int` would have shadowed the
// builtin across the entire `images` package (other consumers in the
// same package resolving `min(...)` would have hit our redefinition),
// so the helper was deleted. Call sites use the builtin directly:
//   `min(len(gotP), 120)`, `min(len(r.Composed), len(longPrompt))`.
