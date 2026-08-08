// Package scripts — clip_plaintext_output_p0f_test.go: P0.F
// plain-text output contract suite for the LLM-emitted narrative
// on POST /api/script/generate.
//
// July 2026 PR — P0.F gate. Pins the canonical contract for the
// V2 source=clips prose output: the LLM-emitted narrative text
// MUST be PURE prose. None of the 8 forbidden signatures listed
// below may appear in the canonical Output.Text field.
//
// FORBIDDEN SIGNATURES (case-insensitive per `grep -Ei` semantics
// in the user spec):
//
//  1. ```            (3-backtick code fences — model is leaking
//     raw code into the prose stream)
//  2. JSON           (literal word "JSON" — model is signaling
//     structured output rather than prose)
//  3. schema_version (canonical model-output schema token — same
//     leak as JSON)
//  4. specscene      (canonical model-output structured-scene
//     token — same leak as JSON)
//  5. scene-1        (canonical scene ID prefix — model is
//     emitting per-scene identifiers into the
//     narrative stream)
//  6. clip_id        (canonical clip binding key — model is
//     emitting per-clip identifiers into the
//     narrative stream)
//  7. drive.google.com (canonical Drive URL host — model is
//     leaking internal storage URLs into
//     the prose)
//  8. lines starting with `{` (JSON object opening — model is
//     emitting structured data
//     alongside or instead of prose)
//
// The contract is enforced at TWO LEVELS:
//
//	(a) HELPER level — pure-function regex check that runs against
//	    any prose string (cheap, no Ollama). Per-signature
//	    regression coverage (9 sub-tests, includes a
//	    case-variant lock) + happy-path clean-prose coverage
//	    (4 sub-tests) + cumulative detection (1 test: a fixture
//	    containing all 8 signatures must trip ALL 8 matches, not
//	    just the first).
//
//	(b) ORCHESTRATOR level — drives GenerateOneUseCase via
//	    buildUsecaseWithClipResolver + fakeOllamaGen + a
//	    text-only source, asserts the canonical result.Output.Text
//	    passes the contract. Proves the regex check applies to
//	    the REAL model-emitted output end-to-end, not just
//	    hand-crafted strings.
//
// godlike/07 NO-FAKE-AVAILABILITY: every signature is checked via
// `(?i)` regex (Go: regexp.MustCompile + MatchString), so the
// contract catches LITERAL matches, CASE-VARIANT matches, and
// EMBEDDED matches. A future regression where the model emits
// `Schema_Version` (case variant) is caught just as loudly as
// `schema_version` (canonical).
package usecase

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plaintextContractSignatures is the canonical list of regex
// patterns that MUST NOT appear in the model-emitted narrative.
// Each pattern is named for grep-friendly failure messages; the
// per-test helper runPlaintextContract iterates this list and
// fails the test on the first match. The `(?i)` flag is applied
// at compile time inside the helper so the patterns below
// remain canonical / literal / readable.
//
// Pattern sources match the user-spec P0.F verbatim (8 entries
// exactly, no more, no fewer — the next contributor who adds or
// removes a pattern must update the per-signature regression
// test in lockstep).
var plaintextContractSignatures = []struct {
	Name    string
	Pattern string
}{
	// 1. 3-backtick code fences.
	{"code_fence", "```"},
	// 2. Literal word "JSON" (case-insensitive via (?i) at compile).
	{"json_literal", "JSON"},
	// 3. Canonical model-output schema token.
	{"schema_version", "schema_version"},
	// 4. Canonical model-output structured-scene token.
	{"specscene", "specscene"},
	// 5. Canonical scene ID prefix.
	{"scene_1", "scene-1"},
	// 6. Canonical clip binding key.
	{"clip_id", "clip_id"},
	// 7. Canonical Drive URL host (escaped literal dot).
	{"drive_google_host", `drive\.google\.com`},
	// 8. Multi-line mode: lines starting with `{` (optional
	//    leading whitespace). Anchors the JSON-object opening
	//    even when the line is indented.
	{"line_opening_brace", `(?m)^\s*\{`},
}

// runPlaintextContract runs every regex in
// plaintextContractSignatures against output and fails t on the
// first match. Returns the number of matches (0 means the
// contract holds end-to-end).
//
// Each failure message includes:
//   - the signature name (grep-friendly)
//   - the regex pattern (debug aid)
//   - the source label (so a reader knows which seam fired the
//     violation: helper-level vs orchestrator-level)
//   - the match offset (so a reader can `cut` the output stream)
//   - a ±40-char excerpt around the match (so the offending
//     substring is visible in CI output without scrolling)
func runPlaintextContract(t *testing.T, output, sourceLabel string) int {
	t.Helper()
	matches := 0
	for _, sig := range plaintextContractSignatures {
		re := regexp.MustCompile("(?i)" + sig.Pattern)
		loc := re.FindStringIndex(output)
		if loc == nil {
			continue
		}
		matches++

		start := loc[0] - 40
		if start < 0 {
			start = 0
		}
		end := loc[1] + 40
		if end > len(output) {
			end = len(output)
		}
		excerpt := output[start:end]
		t.Errorf(
			"plaintext contract violation: signature=%q pattern=%q source=%s offset=%d excerpt=%q",
			sig.Name, sig.Pattern, sourceLabel, loc[0], excerpt,
		)
	}
	return matches
}

// TestPlaintextOutput_P0F_HappyPath_AllClean verifies the
// signature detector is NOT a false-positive generator: 4
// canonical clean-prose outputs must pass the contract with
// exactly 0 matches.
//
// Each sub-test uses a different prose SHAPE to surface hidden
// regex bugs (e.g., a buggy character-class that happens to
// match a single letter in a single-word prose).
func TestPlaintextOutput_P0F_HappyPath_AllClean(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		prose  string
		source string
	}{
		{
			name:   "single_sentence",
			prose:  "The bell rings and the fighters touch gloves before round one begins.",
			source: "single-sentence prose",
		},
		{
			name: "multi_paragraph",
			prose: strings.Join([]string{
				"Round 1 begins with both fighters trading jabs.",
				"",
				"The champion enters the ring to a chorus of cheers.",
				"",
				"The challenger studies his opponent carefully.",
			}, "\n"),
			source: "multi-paragraph prose",
		},
		{
			name:   "single_word",
			prose:  "Boom.",
			source: "single-word prose",
		},
		{
			name:   "control_prose",
			prose:  "The bell rings. The crowd roars. The fight begins.",
			source: "control prose (plain ASCII, intentionally no CJK / emoji so the test does not cross-pollinate with future multibyte-character work)",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			matches := runPlaintextContract(t, tc.prose, tc.source)
			assert.Equalf(t, 0, matches,
				"clean prose MUST pass the contract with 0 matches; got %d (case=%q, prose=%q)",
				matches, tc.name, tc.prose)
		})
	}
}

// TestPlaintextOutput_P0F_RegressionCoverage_PerSignature pins
// each of the 8 forbidden signatures individually. Each sub-test
// feeds a string that contains exactly one signature, and asserts
// the helper detects it (the expected signature name appears in
// the matched set).
//
// This is the LOAD-BEARING regression test: a future contributor
// who accidentally drops a signature from the list (e.g., a
// reformat of plaintextContractSignatures) would surface here
// as a sub-test FAIL with a precise "no matches detected"
// message naming the missing signature.
func TestPlaintextOutput_P0F_RegressionCoverage_PerSignature(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		prose     string
		signature string // the name of the signature expected to fire
	}{
		{
			name:      "code_fence_backticks",
			prose:     "Here is the script:\n```\nBLOCK OF CODE\n```\nDone.",
			signature: "code_fence",
		},
		{
			name:      "json_literal_capitalized",
			prose:     "Here is the script as JSON.",
			signature: "json_literal",
		},
		{
			name:      "json_literal_lowercase",
			prose:     "Here is the script as json.",
			signature: "json_literal",
		},
		{
			name:      "schema_version",
			prose:     "Output schema_version = 1 in this version.",
			signature: "schema_version",
		},
		{
			name:      "specscene",
			prose:     "The specscene field contains the scene array.",
			signature: "specscene",
		},
		{
			name:      "scene_1",
			prose:     "First scene (scene-1) introduces the main character.",
			signature: "scene_1",
		},
		{
			name:      "clip_id",
			prose:     "Bound to clip_id clip-abc in the registry.",
			signature: "clip_id",
		},
		{
			name:      "drive_google_host",
			prose:     "Watch the full video at drive.google.com/file/d/abc/view.",
			signature: "drive_google_host",
		},
		{
			name:      "line_opening_brace",
			prose:     "Some prose here.\n{\"key\": \"value\"}\nMore prose.",
			signature: "line_opening_brace",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Collect EVERY signature that fires (not just the
			// first). The fixture prose MAY incidentally trip
			// more than one — that's fine, but the EXPECTED
			// signature MUST be among the matches.
			matchedNames := make(map[string]bool)
			for _, sig := range plaintextContractSignatures {
				re := regexp.MustCompile("(?i)" + sig.Pattern)
				if re.MatchString(tc.prose) {
					matchedNames[sig.Name] = true
				}
			}

			assert.Truef(t, matchedNames[tc.signature],
				"P0.F regression contract: signature %q MUST be detected in prose %q; matched signatures=%v",
				tc.signature, tc.prose, matchedNames)
		})
	}
}

// TestPlaintextOutput_P0F_SignatureIndex_AllReported pins the
// cumulative-detection contract: a single fixture containing
// ALL 8 forbidden signatures must trip ALL 8 matches. This
// regression-locks against future helper bugs that early-exit
// after the first match (so a reviewer sees the full violation
// surface in a single test failure rather than fixing one
// signature at a time across N CI runs).
func TestPlaintextOutput_P0F_SignatureIndex_AllReported(t *testing.T) {
	t.Parallel()

	// Prose that intentionally contains all 8 signatures. Each
	// signature is on its own line for surgical grep-ability.
	prose := strings.Join([]string{
		"```",                          // code_fence
		"as json output",               // json_literal (lowercase variant of JSON)
		"schema_version=1",             // schema_version
		"specscene here",               // specscene
		"scene-1 starts now",           // scene_1
		"clip_id=abc",                  // clip_id
		"https://drive.google.com/xyz", // drive_google_host
		`{"key": "value"}`,             // line_opening_brace
	}, "\n")

	matched := 0
	for _, sig := range plaintextContractSignatures {
		re := regexp.MustCompile("(?i)" + sig.Pattern)
		if re.MatchString(prose) {
			matched++
		}
	}
	require.Equalf(t, 8, matched,
		"all-signatures fixture MUST trigger all 8 signatures; got %d", matched)
}

// TestPlaintextOutput_P0F_Orchestrator_FakeOllamaCleanProse
// drives the FULL orchestrator path (GenerateOneUseCase) with a
// fakeOllamaGen emitting a clean prose JSON envelope via a
// text-only source, and asserts the canonical result.Output.Text
// passes the contract.
//
// This is the integration-level lock: the helper-level tests
// above verify the regex; this test verifies the regex applies
// to the real model-emitted output end-to-end (through the
// engine + postprocessor pipeline), not just hand-crafted
// strings.
//
// Source choice: a TEXT-ONLY item (no clips). Routing through
// the clip source would require registering a clip in the
// resolver and engaging the clip-binding postprocessor — the
// plain-prose contract applies to the final Output.Text seam
// either way, and the text-only path is the simplest way to
// exercise that seam in isolation.
//
// Wire shape: scriptports.GenerationResult is a flat struct
// with fields {Script, WordCount, EstDuration, Model, Prompt}.
// The engine decodes `Script` as a JSON envelope
// (ModelScriptOutputV1: {schema_version, text, specscene}) and
// surfaces the inner `text` field as engineResult.Output.Text,
// which the orchestrator propagates to result.Output.Text. So
// the canonical test fixture is a JSON envelope with CLEAN
// prose in the `text` field + an empty `scenes` array (no
// per-scene structured data) — exactly the prose-only
// post-LLM-PLAIN-TEXT-CONTRACT shape.

// A canonical clean prose — well within the 8-signature
// ban. The test asserts the orchestrator's Output.Text
// field passes the contract WITHOUT any of the forbidden
// signatures leaking through the engine / postprocessor
// pipeline.

// JSON envelope: canonical ModelScriptOutputV1 shape with
// CLEAN prose in the "text" field and an empty scenes
// array. Built inline (rather than via canonicalSceneJSON)
// so the prose is preserved EXACTLY (canonicalSceneJSON
// re-tokenises sourceText and does not round-trip the
// original sentence).

// Text-only wiring: passes nil for the clip resolver
// (buildUsecaseWithClipResolver wires the SourceText
// resolver by default). The engine decodes Script and
// surfaces the inner "text" field as
// engineResult.Output.Text → result.Output.Text.

// PRE-EXISTING-13 Option A (applied on disk, July 2026) replaces the
// PRE-EXISTING-7 / FASE 13 PART 4 empty-SourceText design below:
// the orchestrator-level test is integrated end-to-end via a literal
// SourceText on the fixture side AND CachePolicy.Mode="disabled"
// so the source_enrichment.go cache layer is bypassed per godlike/07
// fail-closed doctrine (test isolation MUST be a hard-wired decision
// by the test author, not a runtime inference). The fixture's literal
// SourceText is a canonical-fake anchor — it is NOT exercised for
// editorial comparison; the orchestrator's Output.Text assertion
// below is the load-bearing contract check. The literal anchor
// exists purely to keep FASE 13 PART 4's double-trips tolerance gate
// from engaging WHEN SourceText is empty (TOLERANCE-OBSERVATIONAL).
//
// Sanity: the orchestrator propagated the prose (a stable
// substring is enough — strict byte-equality is too tight
// if a future engine tweak adds a trailing newline or
// normalises unicode quotes; the contract check above is
// the load-bearing assertion, this Contains check is just
// a smoke-test that the prose survived the pipeline).

// TestPlaintextOutput_P0F_Orchestrator_FakeOllamaCleanProse is the
// integration-level lock:
//   - helper-level tests verify the regex;
//   - this test verifies the regex applies to the real model-emitted
//     output end-to-end (through the engine + postprocessor pipeline),
//     not just hand-crafted strings.
//
// PRE-EXISTING-13 Option A (architecture/issues.yaml follow_up):
//   - literal SourceText on the fixture anchors the editorial gate,
//   - CachePolicy.Mode=disabled hardwires source-enrichment bypass,
//   - t.Log surfaces the override in test logs (the recommended
//     log-trace emission from the follow_up Option-A guidance).
func TestPlaintextOutput_P0F_Orchestrator_FakeOllamaCleanProse(t *testing.T) {
	t.Parallel()

	t.Log("Option A test-only fixture override: assigning explicit " +
		"SourceText + CachePolicy.Mode=disabled — anchors FASE 13 PART 4 " +
		"word-count gate and bypasses source_enrichment.go per " +
		"PRE-EXISTING-13-USECASE-PLAINTEXT-ENRICHMENT follow_up Option A")

	prose := "The bell rings and the fighters touch gloves before round one begins."
	cleanJSON := fmt.Sprintf(`{"schema_version":1,"text":%q,"scenes":[]}`, prose)

	uc := buildUsecaseWithClipResolver(
		&fakeOllamaGen{result: &scriptports.GenerationResult{
			Script:      cleanJSON,
			WordCount:   10,
			EstDuration: 3,
			Model:       "llama3:8b",
			Prompt:      "p0f-orchestrator-test-prompt",
		}},
		nil, // text-only path: no clip resolver engaged.
	)

	item := scriptpkg.GenerationItemV2{
		ID:       "p0f-text-only-orchestrator",
		Title:    "Plain prose contract — text-only anchor",
		Language: "en",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "clean prose contract",
			SourceText: "test anchor",
			CachePolicy: scriptpkg.SourceCachePolicy{
				Mode: scriptpkg.SourceCacheModeDisabled,
			},
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords: 10,
			MinWords:    8,
			// SkipQualityGate isolates this test from the editorial gate
			// so the load-bearing assertion stays the plaintext contract.
			SkipQualityGate: true,
		},
	}

	// No-op ProgressFn avoids nil-call hazard in tracker.TrackEvent /
	// tracker.PhaseComplete when called from generate_one_usecase.go
	// during the Execute path. The closure signature matches
	// ProgressFn (see progress.go); handlers that fire during this
	// test are silently absorbed so contract assertions below stay
	// green without operator UI coupling.
	tracker := NewProgressTracker(func(int, string) {}, item.ID)
	result, err := uc.Execute(context.Background(), item, scriptpkg.PresetCustom, tracker)
	require.NoErrorf(t, err,
		"orchestrator-level execution MUST NOT error under Option A fixture-overlap fix (PRE-EXISTING-13 follow_up)")
	require.NotNilf(t, result,
		"orchestrator MUST return a non-nil generation result")
	// godlike/07 fail-closed: catch silent-empty-body (engine decoding
	// failure) so the contract check below does not pass vacuously
	// when Output.Text=="".
	require.NotEmptyf(t, result.Output.Text,
		"orchestrator MUST propagate non-empty Output.Text end-to-end; got %q",
		result.Output.Text)

	matches := runPlaintextContract(t, result.Output.Text, "orchestrator-level prose")
	assert.Equalf(t, 0, matches,
		"orchestrator-emitted Output.Text MUST pass the plaintext contract; "+
			"got %d forbidden-signature matches in Output.Text=%q",
		matches, result.Output.Text)
}
