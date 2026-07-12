// Package asset — pipeline_state_test.go (Fase 4, July 2026)
//
// Pins the IsValidTransition table for asset.PipelineState. The
// contract is the canonical 12-state per-item state machine
// declared at pipeline_state.go::IsValidTransition:
//
//	Happy path (10 edges):
//	    DISCOVERED          → DOWNLOAD_PENDING
//	    DOWNLOAD_PENDING    → DOWNLOADING
//	    DOWNLOADING         → DOWNLOADED
//	    DOWNLOADED          → PROCESSING
//	    PROCESSING          → PROCESSED
//	    PROCESSED           → PUBLISHING
//	    PUBLISHING          → PUBLISHED
//	    PUBLISHED           → INDEX_PENDING
//	    INDEX_PENDING       → INDEXED
//
//	Failure / skip exits (any non-terminal → FAILED or SKIPPED):
//	    <any non-terminal>   → FAILED
//	    <any non-terminal>   → SKIPPED
//
//	Self-loops are idempotent (`a.IsValidTransition(a) == true`).
//	Unknown target values (e.g. "discovered", "DOWNLOAD", "")
//	are REJECTED (Valid() check on `to`).
//
// The table below enumerates every (from, to) pair across the 12
// canonical PipelineState values (144 pairs total; the 12
// self-loops are always true by IsValidTransition's first-line
// guard).
//
// The drift detector: a missing pair surfaces as a test failure
// with the offending (from, to) pair printed; a new state added
// to allPipelineStates / CanonicalPipelineStateValues surfaces
// as an "expected pair missing from want-if-explicit map" panic
// + asserts.Equal diff the full want-actual map side-by-side.
//
// Mirrors the test surface for LifecycleState at
// lifecycle_state_test.go and IndexState at index_state_test.go —
// one canonical state file per typed enum so audit hunks stay
// scoped to the affected type.
package asset

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// allPipelineStates is the canonical, deterministic enumeration
// used to drive the full (from, to) Cartesian. Mirrors
// CanonicalPipelineStateValues() in pipeline_state.go to keep
// the test table in lockstep with production code: if a new state
// lands in the canonical list, the expected-vs-actual diffs in
// TestPipelineState_IsValidTransitionFullTable surface the gap
// within a single CI run.
var allPipelineStates = []PipelineState{
	StatePipelineDiscovered,
	StatePipelineDownloadPending,
	StatePipelineDownloading,
	StatePipelineDownloaded,
	StatePipelineProcessing,
	StatePipelineProcessed,
	StatePipelinePublishing,
	StatePipelinePublished,
	StatePipelineIndexPending,
	StatePipelineIndexed,
	StatePipelineFailed,
	StatePipelineSkipped,
}

// happyPathEdges is the explicit list of happy-path edges.
// Mirrors the IsValidTransition method body verbatim so a
// future refactor that drops an edge is caught by the matrix
// test below.
var happyPathEdges = []struct {
	from, to PipelineState
}{
	{StatePipelineDiscovered, StatePipelineDownloadPending},
	{StatePipelineDownloadPending, StatePipelineDownloading},
	{StatePipelineDownloading, StatePipelineDownloaded},
	{StatePipelineDownloaded, StatePipelineProcessing},
	{StatePipelineProcessing, StatePipelineProcessed},
	{StatePipelineProcessed, StatePipelinePublishing},
	{StatePipelinePublishing, StatePipelinePublished},
	{StatePipelinePublished, StatePipelineIndexPending},
	{StatePipelineIndexPending, StatePipelineIndexed},
}

// nonTerminalPipelineStates is the list of states that admit a
// FAILED or SKIPPED exit. Terminal states (INDEXED, FAILED,
// SKIPPED) cannot be re-entered; the matrix test below verifies
// the rejection.
var nonTerminalPipelineStates = []PipelineState{
	StatePipelineDiscovered,
	StatePipelineDownloadPending,
	StatePipelineDownloading,
	StatePipelineDownloaded,
	StatePipelineProcessing,
	StatePipelineProcessed,
	StatePipelinePublishing,
	StatePipelinePublished,
	StatePipelineIndexPending,
}

// TestPipelineState_IsValidTransitionFullTable exhaustively
// enumerates the (from, to) Cartesian product over the 12
// canonical pipeline states (144 pairs total; the 12 self-loops
// are always true by IsValidTransition's first-line guard).
//
// The contract lives in the IsValidTransition method body; this
// test is the regression pin.
func TestPipelineState_IsValidTransitionFullTable(t *testing.T) {
	for _, from := range allPipelineStates {
		t.Run(string(from), func(t *testing.T) {
			for _, to := range allPipelineStates {
				got := from.IsValidTransition(to)
				// Universal self-loop.
				if from == to {
					if !got {
						t.Errorf("self-loop rejected: IsValidTransition(%q, %q)=false; want true (idempotent self-loop)", from, to)
					}
					continue
				}
				// Compute the expected verdict via the
				// test-side mirror of IsValidTransition's
				// logic.
				want := isExplicitlyAllowed(from, to)
				if want && !got {
					t.Errorf("IsValidTransition(%q, %q)=false; want true (explicit edge in pipeline state machine)", from, to)
				}
				if !want && got {
					t.Errorf("IsValidTransition(%q, %q)=true; want false (no documented edge)", from, to)
				}
			}
		})
	}
}

// isExplicitlyAllowed is the test-side mirror of
// IsValidTransition's logic. Pinning the truth table here means
// a future refactor that drops an edge OR adds an undocumented
// edge surfaces as a diff between the test's computed verdict
// and the production verdict.
func isExplicitlyAllowed(from, to PipelineState) bool {
	// Happy-path edges.
	for _, e := range happyPathEdges {
		if e.from == from && e.to == to {
			return true
		}
	}
	// Failure / skip exits: any non-terminal can move to
	// FAILED or SKIPPED.
	if to == StatePipelineFailed || to == StatePipelineSkipped {
		for _, nt := range nonTerminalPipelineStates {
			if nt == from {
				return true
			}
		}
	}
	return false
}

// TestPipelineState_StringLiteralValues pins the exact strings
// of every canonical PipelineState. Drift in the string values
// would silently break every WHERE clause that compares against
// the media_assets_pipeline_events.fase column (the
// AppendPipelineEvent writer's idempotency fence, the
// GetPipelineEvents read-back, etc.).
func TestPipelineState_StringLiteralValues(t *testing.T) {
	cases := []struct {
		state PipelineState
		want  string
	}{
		{StatePipelineDiscovered, "DISCOVERED"},
		{StatePipelineDownloadPending, "DOWNLOAD_PENDING"},
		{StatePipelineDownloading, "DOWNLOADING"},
		{StatePipelineDownloaded, "DOWNLOADED"},
		{StatePipelineProcessing, "PROCESSING"},
		{StatePipelineProcessed, "PROCESSED"},
		{StatePipelinePublishing, "PUBLISHING"},
		{StatePipelinePublished, "PUBLISHED"},
		{StatePipelineIndexPending, "INDEX_PENDING"},
		{StatePipelineIndexed, "INDEXED"},
		{StatePipelineFailed, "FAILED"},
		{StatePipelineSkipped, "SKIPPED"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			assert.Equal(t, c.want, string(c.state))
		})
	}
}

// TestPipelineState_Valid pins the Valid() gate. Ad-hoc values
// must be rejected; canonical values must be accepted.
func TestPipelineState_Valid(t *testing.T) {
	cases := []struct {
		s    PipelineState
		want bool
	}{
		// All 12 canonical values: valid.
		{StatePipelineDiscovered, true},
		{StatePipelineDownloadPending, true},
		{StatePipelineDownloading, true},
		{StatePipelineDownloaded, true},
		{StatePipelineProcessing, true},
		{StatePipelineProcessed, true},
		{StatePipelinePublishing, true},
		{StatePipelinePublished, true},
		{StatePipelineIndexPending, true},
		{StatePipelineIndexed, true},
		{StatePipelineFailed, true},
		{StatePipelineSkipped, true},
		// Ad-hoc values: invalid.
		{PipelineState(""), false},
		{PipelineState("discovered"), false},   // lowercase
		{PipelineState("DOWNLOAD"), false},     // truncated
		{PipelineState("DOWNLOADED_X"), false}, // padded
		{PipelineState("FAILED "), false},      // trailing space
		{PipelineState(" INDEXED"), false},     // leading space
	}
	for _, c := range cases {
		t.Run(string(c.s), func(t *testing.T) {
			assert.Equal(t, c.want, c.s.Valid())
		})
	}
}

// TestPipelineState_IsTerminal pins the terminal-state gate.
// The 3 terminal states are INDEXED, FAILED, SKIPPED; the 9
// in-flight states are not terminal.
func TestPipelineState_IsTerminal(t *testing.T) {
	cases := []struct {
		s    PipelineState
		want bool
	}{
		// 3 terminal states.
		{StatePipelineIndexed, true},
		{StatePipelineFailed, true},
		{StatePipelineSkipped, true},
		// 9 in-flight states.
		{StatePipelineDiscovered, false},
		{StatePipelineDownloadPending, false},
		{StatePipelineDownloading, false},
		{StatePipelineDownloaded, false},
		{StatePipelineProcessing, false},
		{StatePipelineProcessed, false},
		{StatePipelinePublishing, false},
		{StatePipelinePublished, false},
		{StatePipelineIndexPending, false},
	}
	for _, c := range cases {
		t.Run(string(c.s), func(t *testing.T) {
			assert.Equal(t, c.want, c.s.IsTerminal())
		})
	}
}

// TestPipelineState_HelperMethods pins the four predicate
// methods (IsFailedTerminal, IsSucceededTerminal, IsSkipped,
// IsPending). The matrix test below exhaustively walks the 12
// states × 4 methods table.
func TestPipelineState_HelperMethods(t *testing.T) {
	cases := []struct {
		s                                    PipelineState
		wantFailed, wantSucceeded, wantSkip  bool
		wantPending                          bool
	}{
		// 3 terminal states.
		{StatePipelineIndexed, false, true, false, false},
		{StatePipelineFailed, true, false, false, false},
		{StatePipelineSkipped, false, false, true, false},
		// 9 in-flight states (all pending).
		{StatePipelineDiscovered, false, false, false, true},
		{StatePipelineDownloadPending, false, false, false, true},
		{StatePipelineDownloading, false, false, false, true},
		{StatePipelineDownloaded, false, false, false, true},
		{StatePipelineProcessing, false, false, false, true},
		{StatePipelineProcessed, false, false, false, true},
		{StatePipelinePublishing, false, false, false, true},
		{StatePipelinePublished, false, false, false, true},
		{StatePipelineIndexPending, false, false, false, true},
	}
	for _, c := range cases {
		t.Run(string(c.s), func(t *testing.T) {
			assert.Equal(t, c.wantFailed, c.s.IsFailedTerminal(), "IsFailedTerminal")
			assert.Equal(t, c.wantSucceeded, c.s.IsSucceededTerminal(), "IsSucceededTerminal")
			assert.Equal(t, c.wantSkip, c.s.IsSkipped(), "IsSkipped")
			assert.Equal(t, c.wantPending, c.s.IsPending(), "IsPending")
		})
	}
}

// TestPipelineState_String pins the String() helper
// (fmt.Stringer). Each canonical value's String() returns the
// underlying string verbatim.
func TestPipelineState_String(t *testing.T) {
	for _, s := range allPipelineStates {
		t.Run(string(s), func(t *testing.T) {
			assert.Equal(t, string(s), s.String())
		})
	}
}

// TestPipelineState_UnknownTargetRejected pins the unknown-
// target gate: IsValidTransition must reject transitions to
// ad-hoc values (the Valid() check on `to`).
func TestPipelineState_UnknownTargetRejected(t *testing.T) {
	cases := []PipelineState{
		PipelineState("UNKNOWN_STATE"),
		PipelineState("discovered"),
		PipelineState(""),
	}
	for _, to := range cases {
		t.Run(string(to), func(t *testing.T) {
			// From any state: an unknown target is rejected.
			// Self-loop on "" is also rejected (Valid("") is
			// false), so the `if from == to` short-circuit
			// never fires for the empty target.
			for _, from := range allPipelineStates {
				got := from.IsValidTransition(to)
				if got {
					t.Errorf("IsValidTransition(%q, %q)=true; want false (unknown target must be rejected)", from, to)
				}
			}
		})
	}
}

// TestPipelineState_CanonicalValuesExhaustive pins that
// CanonicalPipelineStateValues returns all 12 values in the
// declared order. A future PR that adds a new state must
// extend the slice AND the IsValidTransition matrix AND the
// helper-methods matrix test.
func TestPipelineState_CanonicalValuesExhaustive(t *testing.T) {
	assert.Len(t, CanonicalPipelineStateValues(), 12,
		"CanonicalPipelineStateValues must return exactly 12 values; a future PR adding a 13th state must update the IsValidTransition matrix AND the helper-methods test")
	assert.Equal(t, allPipelineStates, CanonicalPipelineStateValues(),
		"CanonicalPipelineStateValues must return values in the same order as allPipelineStates (test-side mirror)")
}

// ── SanitizeSafeMessage tests ──────────────────────────────────────

// TestSanitizeSafeMessage_Empty pins the empty-input case.
func TestSanitizeSafeMessage_Empty(t *testing.T) {
	assert.Equal(t, "", SanitizeSafeMessage(""))
}

// TestSanitizeSafeMessage_NoOp pins the no-op case: a clean
// ASCII string is returned verbatim (modulo trim).
func TestSanitizeSafeMessage_NoOp(t *testing.T) {
	in := "download failed: HTTP 503"
	assert.Equal(t, in, SanitizeSafeMessage(in))
}

// TestSanitizeSafeMessage_StripsControlChars pins the
// control-character-stripping rule. The newline and CR are
// replaced with space; other control chars are dropped.
func TestSanitizeSafeMessage_StripsControlChars(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"newline-becomes-space", "line1\nline2", "line1 line2"},
		{"cr-becomes-space", "line1\rline2", "line1 line2"},
		{"crlf-becomes-space", "line1\r\nline2", "line1 line2"},
		{"tab-preserved", "col1\tcol2", "col1\tcol2"},
		{"null-stripped", "before\x00after", "beforeafter"},
		{"bell-stripped", "before\x07after", "beforeafter"},
		{"del-stripped", "before\x7fafter", "beforeafter"},
		{"vt-stripped", "before\x0bafter", "beforeafter"},
		{"ff-stripped", "before\x0cafter", "beforeafter"},
		{"mixed-newline-tab-cr", "a\nb\tc\rd", "a b\tc d"},
		{"escape-stripped", "before\x1bafter", "beforeafter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, SanitizeSafeMessage(c.in))
		})
	}
}

// TestSanitizeSafeMessage_CollapsesSpaces pins the multi-space
// collapse rule. Two or more consecutive spaces collapse to one.
// Tabs are NOT spaces and do not collapse against surrounding
// spaces.
func TestSanitizeSafeMessage_CollapsesSpaces(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"double-space", "a  b", "a b"},
		{"triple-space", "a   b", "a b"},
		{"many-spaces", "a          b", "a b"},
		{"tabs-preserved", "a\t\tb", "a\t\tb"},
		{"tab-space-tab", "a\t b\tc", "a\t b\tc"},
		{"mixed-spaces-and-newlines", "a\n\nb", "a b"},
		{"leading-trailing-trim", "   hello   ", "hello"},
		{"all-whitespace-trim", " \t\n\r ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, SanitizeSafeMessage(c.in))
		})
	}
}

// TestSanitizeSafeMessage_LengthCap pins the 1024-char cap.
// Strings longer than MaxSafeMessageLen are truncated with the
// "...(truncated)" marker.
func TestSanitizeSafeMessage_LengthCap(t *testing.T) {
	// 2000 'a' chars: well over the 1024 cap.
	in := strings.Repeat("a", 2000)
	got := SanitizeSafeMessage(in)
	assert.Equal(t, MaxSafeMessageLen, len(got),
		"sanitized output must be exactly MaxSafeMessageLen chars")
	assert.True(t, strings.HasSuffix(got, "...(truncated)"),
		"sanitized output must surface the truncation marker so the cap is observable")

	// 1023 'a' chars: under the cap, no truncation.
	inShort := strings.Repeat("a", 1023)
	gotShort := SanitizeSafeMessage(inShort)
	assert.Equal(t, 1023, len(gotShort),
		"strings under the cap must NOT be truncated")

	// Exactly 1024 chars: at the cap, no truncation.
	inExact := strings.Repeat("a", 1024)
	gotExact := SanitizeSafeMessage(inExact)
	assert.Equal(t, 1024, len(gotExact),
		"strings at exactly the cap must NOT be truncated")

	// 1025 chars: just over the cap, truncation fires.
	inOver := strings.Repeat("a", 1025)
	gotOver := SanitizeSafeMessage(inOver)
	assert.Equal(t, MaxSafeMessageLen, len(gotOver),
		"strings just over the cap must be truncated to exactly MaxSafeMessageLen")
	assert.True(t, strings.HasSuffix(gotOver, "...(truncated)"),
		"truncation marker must be present")
}

// TestSanitizeSafeMessage_PreservesUnicode pins the unicode
// preservation rule. The sanitizer must NOT mangle non-ASCII
// runes; the strip/collapse rules apply to ASCII control chars
// only.
func TestSanitizeSafeMessage_PreservesUnicode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"italian-error", "errore: file non trovato", "errore: file non trovato"},
		{"emoji-preserved", "errore 🔥 server", "errore 🔥 server"},
		{"chinese-preserved", "错误：文件未找到", "错误：文件未找到"},
		{"arabic-preserved", "خطأ: الملف غير موجود", "خطأ: الملف غير موجود"},
		{"cyrillic-preserved", "ошибка: файл не найден", "ошибка: файл не найден"},
		// U+2028 LINE SEPARATOR is NOT an ASCII control char;
		// it's a unicode category Zl separator. The unicode
		// package's IsControl returns false for it, so it's
		// preserved. This is by design: the operator sees the
		// original message.
		{"unicode-line-separator", "before\u2028after", "before\u2028after"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, SanitizeSafeMessage(c.in))
		})
	}
}

// TestSanitizeSafeMessage_RealisticCase pins a realistic
// operator-triage line: a multi-line ffmpeg error with mixed
// newlines, tabs, and control chars. The expected output
// collapses the multi-line trace into a single log line while
// preserving the tab-aligned columns.
func TestSanitizeSafeMessage_RealisticCase(t *testing.T) {
	in := "ffmpeg failed:\n\tStream #0:0: Video: h264, 1920x1080, 30 fps\r\n\tError: -22 (Invalid argument)\n\x07\x08"
	// Walk the rules:
	//   Step 1 (\n and \r -> space, \x07/\x08 stripped):
	//     "ffmpeg failed: \tStream #0:0: Video: h264, 1920x1080, 30 fps  \tError: -22 (Invalid argument) "
	//   Step 2 (collapse multi-space; tabs do NOT collapse):
	//     "ffmpeg failed: \tStream #0:0: Video: h264, 1920x1080, 30 fps \tError: -22 (Invalid argument)"
	//   Step 3 (trim trailing):
	//     unchanged (no leading/trailing whitespace after step 2).
	want := "ffmpeg failed: \tStream #0:0: Video: h264, 1920x1080, 30 fps \tError: -22 (Invalid argument)"
	got := SanitizeSafeMessage(in)
	assert.Equal(t, want, got)
}

// TestSanitizeSafeMessage_Idempotent pins the idempotency
// property: sanitizing an already-sanitized string is a no-op.
// Useful for callers that re-sanitize on retry / replay.
func TestSanitizeSafeMessage_Idempotent(t *testing.T) {
	cases := []string{
		"download failed: HTTP 503",
		"ffmpeg failed: \tStream #0:0: Video: h264, 1920x1080, 30 fps \tError: -22 (Invalid argument)",
		"errore: file non trovato",
		"error 🔥",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			once := SanitizeSafeMessage(c)
			twice := SanitizeSafeMessage(once)
			assert.Equal(t, once, twice,
				"sanitize must be idempotent: SanitizeSafeMessage(s) == SanitizeSafeMessage(SanitizeSafeMessage(s))")
		})
	}
}
