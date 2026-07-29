// Package usecase_test — pathological_inputs_p2c_test.go (July 2026).
//
// P2.C — Input patologici (rune-safe) test suite.
//
// USER SPEC (verbatim, July 2026): "Implementa la suite P2.C —
// Input patologici (rune-safe) su main. Testa: transcript da
// centinaia di migliaia di caratteri, emoji, accenti italiani,
// testo in cinese e arabo, caratteri null, HTML, JSON dentro
// transcript, righe molto lunghe, clip senza nome, slug con
// apostrofi, ID oltre lunghezza normale. La truncation deve
// essere RUNE-SAFE a 500 rune per clip: verifica che Unicode
// non venga corrotto. Lavora su main, commit frequenti, push."
//
// ATTESO (acceptance, per the user spec):
//
//  1. truncation is rune-safe at 500 runes per clip (no
//     mid-codepoint splits, no U+FFFD replacement chars, no
//     UTF-8 corruption).
//  2. pathological inputs (huge length, emoji, accented, CJK,
//     Arabic, control chars, HTML, JSON, very long lines) flow
//     through without crashing or corrupting the prompt
//     assembly.
//  3. clip display names fall back to filename → ID when Name
//     is empty or whitespace-only (the canonical 3-tier chain).
//  4. pathological clip IDs are handled by dedup (no length
//     cap, but trim + dedup + skip-empty are load-bearing).
//
// SCOPE NOTE: this file pins the SYSTEM's defense contract at
// the canonical truncation layer (truncateExcerpt), the
// per-clip display-name layer (clipDisplayName), and the
// per-clip ID dedup layer (dedupTrimmedClipIDs). The
// slug-derivation layer (pkg/slug.SlugifyTitle) is tested in
// pkg/slug/pathological_slug_p2c_test.go (separate package,
// separate test file, separate suite surface).
//
// The A4 test (TestClipSourceBuilder_TranscriptRuneSafeExcerpt_A4)
// covers 7 mixed-script cases at modest scale (600 chars). This
// suite covers SINGLE-SCRIPT pathological length (100K chars)
// to prove the truncation scales, plus structural noise (HTML,
// JSON, very long lines), control corruption (null bytes,
// control chars), missing-data fallbacks (Name → Filename → ID),
// and pathological-key edge cases (empty / whitespace /
// duplicate / very long IDs).
//
// SUT BUGS (pin current behavior; all are 2026-07 candidates
// for the "honest lock" backlog):
//
//  1. truncateExcerpt does NOT strip control characters:
//     \x00, \r, \n, \t are counted as runes and included in
//     the excerpt. The current contract is rune-safety ONLY
//     (no sanitization). The quality gate at the engine
//     layer is the load-bearing defense for control-char
//     rejection.
//  2. truncateExcerpt does NOT sanitize HTML/JSON:
//     "<script>alert(1)</script>" and `{"key":"value"}` are
//     preserved verbatim in the excerpt. The risk is
//     downstream rendering (if a web view shows the excerpt
//     raw, XSS / broken HTML). The current contract is
//     rune-safety ONLY (no content-type filtering).
//  3. clipDisplayName does NOT enforce max display name
//     length: a 1MB Name is rendered in full. The risk is
//     unbounded prompt growth if a malicious actor supplies
//     a 1MB Name field.
//  4. dedupTrimmedClipIDs does NOT validate ID length: a
//     100KB clip ID is passed through to the resolver. The
//     risk is unbounded DB lookup cost if the resolver
//     doesn't have its own length cap.
//  5. appendClipSourceText does NOT wrap long lines: a 10MB
//     unbroken line is rendered on a single line. The risk
//     is editor / log line length limits.
//  6. truncateExcerpt does NOT count grapheme clusters: a
//     emoji with skin-tone modifier (e.g., "👋🏽") is counted
//     as 2 runes (👋 + 🏽) but visually 1 grapheme. The
//     current contract is rune-level ONLY (no grapheme
//     cluster awareness).
package usecase_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"go.uber.org/zap"

	scripts "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Helpers ────────────────────────────────────────────────────────────

// newP2CAsset creates an asset with the canonical pathological-input
// shape: id + name + filename + drive link + transcript via the
// legacy metadata_json path. The transcript is stored as
// "transcript" metadata (the legacy key, matching the
// resolveTranscript fallback). The drive link is set so the
// canonical RequireDriveLink check passes.
func newP2CAsset(id, name, filename, transcript, driveLink string) *asset.Asset {
	a := &asset.Asset{ID: id, Name: name, Filename: filename}
	if driveLink != "" {
		a.SetDriveFileID(id + "-drive")
		a.SetDriveLink(driveLink)
	}
	if transcript != "" {
		a.SetMetadataString("transcript", transcript)
	}
	return a
}

// runP2CBuild wires the canonical stub and calls BuildClipContext.
// Returns the evidence + error for assertion.
func runP2CBuild(t *testing.T, clips map[string]*asset.Asset, ids []string) (*scriptpkg.ClipEvidence, error) {
	t.Helper()
	stub := &stubClipsResolver{byID: clips}

	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): route the
	// legacy metadata_json["transcript"] test fixture writes back
	// through the canonical TextTrackReader port. Resolved in
	// strict-cutover resolveTranscript (no metadata_json fallback).
	// Each clip's metadata_json["transcript"] field becomes the
	// Transcript block content for that clip, surfacing through
	// the canonical Fase 4 read pipeline.
	transcripts := map[string]string{}
	for id, clip := range clips {
		if text := clip.GetMetadataString("transcript"); text != "" {
			transcripts[id] = text
		}
	}

	b := scripts.NewClipSourceBuilder(stub, nil, zap.NewNop())
	if len(transcripts) > 0 {
		b.ConfigureTextTrackReader(&p2cMetaBackedTranscriptReader{transcripts: transcripts})
	}
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): pass
	// Language: "en" explicitly so optsResolveLanguage returns
	// a non-empty language code; otherwise resolveTranscript
	// short-circuits via the empty-language branch and returns
	// ErrTextTrackNotReady with empty transcript, breaking the
	// post-cutover read path.
	ev, _, _, err := b.BuildClipContext(context.Background(), ids, &scripts.ClipGenerationOptions{Language: "en"})
	return ev, err
}

// extractP2CExcerpt returns the transcript excerpt from the assembled
// text. The canonical call site runs strings.TrimSpace on the full
// text, so the transcript line is the LAST non-whitespace segment —
// there is no trailing "\n" terminator to find. Slices from the
// "  Transcript: " prefix to end-of-string.
func extractP2CExcerpt(t *testing.T, assembledText string) string {
	t.Helper()
	const prefix = "  Transcript: "
	i := strings.Index(assembledText, prefix)
	require.GreaterOrEqual(t, i, 0, "Transcript prefix not found in: %q", assembledText)
	return assembledText[i+len(prefix):]
}

// ── Group 1: Multilingual & Length ─────────────────────────────────────
//
// Pins the rune-safe truncation contract under pathological length
// + multi-byte unicode. Distinct from the A4 test (which covers
// 7 mixed cases at modest scale); this group covers SINGLE-SCRIPT
// pathological length (100K chars of CJK / emoji / accents /
// Arabic) to prove the truncation is O(n) and rune-safe at scale.
func TestPathologicalInputs_P2C_Group1_MultilingualAndLength(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label                string
		transcript           string
		wantExcerptRunes     int
		wantEndsWithEllipsis bool
	}{
		// Pathological single-script length (100K chars).
		{"100K Italian accents", strings.Repeat("àèéìòù", 20000), 501, true},
		{"100K Chinese", strings.Repeat("你好", 50000), 501, true},
		{"100K Arabic", strings.Repeat("السلام", 20000), 501, true},
		{"100K boxing emoji", strings.Repeat("🥊👊🤜", 20000), 501, true},
		{"100K ASCII", strings.Repeat("a", 100000), 501, true},

		// Boundary probes (covered by A4 too, but pin here for
		// the single-script pathological invariant).
		{"exactly 500 ASCII, no truncation", strings.Repeat("a", 500), 500, false},
		{"501 ASCII, truncated", strings.Repeat("a", 501), 501, true},
		{"500 CJK + 100 CJK, truncated at CJK", strings.Repeat("世", 500) + strings.Repeat("界", 100), 501, true},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			const clipID = "p2c-1"
			clip := newP2CAsset(clipID, "G1 clip", "", tc.transcript, "https://drive/"+clipID)
			ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{clipID})
			require.NoError(t, err)
			require.NotNil(t, ev)

			// Property 1: valid UTF-8 (no mid-codepoint split).
			assert.True(t, utf8.ValidString(ev.AssembledText), "assembled text must be valid UTF-8")
			// Property 2: no U+FFFD replacement char (the
			// canonical fingerprint of a forced byte-cut
			// mid-codepoint).
			assert.NotContains(t, ev.AssembledText, "\uFFFD", "no U+FFFD replacement chars")
			// Property 3: rune budget.
			excerpt := extractP2CExcerpt(t, ev.AssembledText)
			assert.Equal(t, tc.wantExcerptRunes, utf8.RuneCountInString(excerpt), "excerpt rune count")
			// Property 4: ellipsis iff truncated.
			assert.Equal(t, tc.wantEndsWithEllipsis, strings.HasSuffix(excerpt, "\u2026"), "ellipsis suffix")
		})
	}
}

// ── Group 2: Structural Pathologies ────────────────────────────────────
//
// Pins the rune-safe truncation under structural noise (HTML /
// JSON / Markdown / very long unbroken line). The contract is the
// same: truncation is rune-safe, no mid-codepoint splits, no
// U+FFFD. Structural noise is preserved verbatim (SUT BUG 2: no
// HTML/JSON sanitization).
func TestPathologicalInputs_P2C_Group2_StructuralPathologies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label      string
		transcript string
		// wantTruncated: if true, the excerpt should be 501
		// runes (500 + U+2026). If false, the excerpt should
		// be the full transcript (≤ 500 runes).
		wantTruncated bool
	}{
		// HTML preserved verbatim (SUT BUG 2).
		{"HTML in transcript, truncated",
			"<script>alert(1)</script>" + strings.Repeat("Round 1 ", 200), true},
		{"HTML-only short transcript, not truncated",
			"<b>Round 1</b>", false},

		// JSON preserved verbatim (SUT BUG 2). Input is
		// deliberately > 500 runes to force truncation.
		{"JSON in transcript, truncated",
			`{"key":"value","nested":{"a":1}}` + strings.Repeat(" ", 500), true},
		{"JSON-only short transcript, not truncated",
			`{"a":1}`, false},

		// Very long unbroken line (no newlines, no spaces).
		// SUT BUG 5: no line wrapping. Rendered on one line.
		{"10K unbroken ASCII line, truncated", strings.Repeat("a", 10000), true},
		{"10K unbroken CJK line, truncated", strings.Repeat("世", 10000), true},

		// Markdown preserved verbatim. Input is deliberately
		// > 500 runes to force truncation.
		{"Markdown in transcript, truncated",
			"# Title\n**bold** *italic* [link](http://x) " + strings.Repeat("x", 500), true},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			const clipID = "p2c-2"
			clip := newP2CAsset(clipID, "G2 clip", "", tc.transcript, "https://drive/"+clipID)
			ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{clipID})
			require.NoError(t, err)
			require.NotNil(t, ev)

			// Truncation must be rune-safe regardless of content.
			assert.True(t, utf8.ValidString(ev.AssembledText), "valid UTF-8")
			assert.NotContains(t, ev.AssembledText, "\uFFFD", "no U+FFFD")
			excerpt := extractP2CExcerpt(t, ev.AssembledText)

			if tc.wantTruncated {
				assert.Equal(t, 501, utf8.RuneCountInString(excerpt), "truncated to 500 + U+2026")
				assert.True(t, strings.HasSuffix(excerpt, "\u2026"), "ellipsis suffix when truncated")
			} else {
				assert.Equal(t, utf8.RuneCountInString(tc.transcript), utf8.RuneCountInString(excerpt), "untouched short transcript")
				assert.False(t, strings.HasSuffix(excerpt, "\u2026"), "no ellipsis when not truncated")
			}

			// SUT BUG 2: HTML/JSON preserved verbatim (when
			// within the 500-rune budget). We assert the
			// substring is present in the excerpt iff the
			// transcript is short enough to include it
			// unmodified.
			if !tc.wantTruncated {
				if strings.Contains(tc.transcript, "<script>") {
					assert.Contains(t, excerpt, "alert(1)", "HTML body preserved in short excerpt (SUT BUG 2)")
				}
				if strings.HasPrefix(tc.transcript, "{") {
					assert.Contains(t, excerpt, `"a":1`, "JSON body preserved in short excerpt (SUT BUG 2)")
				}
			}
		})
	}
}

// ── Group 3: Control/Corrupt ───────────────────────────────────────────
//
// Pins the rune-safe truncation under control character injection.
// The contract is rune-safety ONLY: \x00, \r, \n, \t are counted as
// runes and included in the excerpt (SUT BUG 1: no control-char
// sanitization). This is intentional for now — the quality gate
// at the engine layer can reject transcripts with control chars;
// the truncation layer is purely rune-safe.
func TestPathologicalInputs_P2C_Group3_ControlCorrupt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label      string
		transcript string
	}{
		// Null bytes preserved as runes (SUT BUG 1).
		{"null bytes mid-transcript",
			"Round 1\x00Pacquiao\x00lands\x00a\x00left\x00hand " + strings.Repeat("x", 200)},
		{"null bytes only",
			strings.Repeat("\x00", 600)},

		// CRLF preserved as runes (SUT BUG 1).
		{"CRLF in transcript",
			"Round 1\r\nRound 2\r\nRound 3\r\n" + strings.Repeat("y", 200)},

		// Tab-separated preserved as runes (SUT BUG 1).
		{"tab-separated",
			"col1\tcol2\tcol3\t" + strings.Repeat("z", 200)},

		// Mixed control chars (BEL, BS, VT, FF, SO, SI, etc.).
		// All counted as runes, none stripped.
		{"mixed control chars",
			"Round\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d1" + strings.Repeat("w", 200)},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			const clipID = "p2c-3"
			clip := newP2CAsset(clipID, "G3 clip", "", tc.transcript, "https://drive/"+clipID)
			ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{clipID})
			require.NoError(t, err)
			require.NotNil(t, ev)

			// Truncation must be rune-safe regardless of control
			// chars.
			assert.True(t, utf8.ValidString(ev.AssembledText), "valid UTF-8")
			assert.NotContains(t, ev.AssembledText, "\uFFFD", "no U+FFFD")
			excerpt := extractP2CExcerpt(t, ev.AssembledText)
			assert.LessOrEqual(t, utf8.RuneCountInString(excerpt), 501, "rune budget")
			if utf8.RuneCountInString(tc.transcript) > 500 {
				assert.Equal(t, 501, utf8.RuneCountInString(excerpt), "truncated to 500 + U+2026")
				assert.True(t, strings.HasSuffix(excerpt, "\u2026"), "ellipsis suffix when truncated")
			}

			// SUT BUG 1: control chars preserved (not stripped).
			// We assert the null byte survives into the excerpt
			// (when the input has nulls and is within budget).
			if strings.Contains(tc.transcript, "\x00") && utf8.RuneCountInString(tc.transcript) <= 500 {
				assert.Contains(t, excerpt, "\x00", "null byte preserved in excerpt (SUT BUG 1)")
			}
		})
	}
}

// ── Group 4: Missing Name (display name fallback chain) ────────────────
//
// Pins the display-name fallback chain: Name → Filename → ID. The
// chain is whitespace-trimmed at each level (so "   " is treated
// as empty). The chain does NOT have a max display name length
// cap (SUT BUG 3: a 1MB Name is rendered in full).
func TestPathologicalInputs_P2C_Group4_MissingName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label    string
		name     string
		filename string
		wantName string
	}{
		{"Name present, Filename ignored", "My Clip", "file.mp4", "My Clip"},
		{"Name empty, Filename present", "", "file.mp4", "file.mp4"},
		{"Name whitespace-only, Filename present", "   ", "file.mp4", "file.mp4"},
		{"Name tabs-only, Filename present", "\t\t", "file.mp4", "file.mp4"},
		{"Both empty, fallback to ID", "", "", "p2c-4"},
		{"Name empty, Filename whitespace, fallback to ID", "", "   ", "p2c-4"},
		{"Both whitespace, fallback to ID", "  ", "\t", "p2c-4"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			const clipID = "p2c-4"
			clip := newP2CAsset(clipID, tc.name, tc.filename, "hello world", "https://drive/"+clipID)
			ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{clipID})
			require.NoError(t, err)
			require.NotNil(t, ev)
			assert.Equal(t, tc.wantName, ev.ClipNames[clipID], "display name fallback chain (Name → Filename → ID)")
		})
	}

	// SUT BUG 3: no max display name length cap.
	t.Run("very_long_name_not_truncated_SUT_BUG_3", func(t *testing.T) {
		t.Parallel()
		const clipID = "p2c-4-long"
		longName := strings.Repeat("X", 100000)
		clip := newP2CAsset(clipID, longName, "", "hello", "https://drive/"+clipID)
		ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{clipID})
		require.NoError(t, err)
		require.NotNil(t, ev)
		// SUT BUG 3: rendered in full (no max cap).
		assert.Equal(t, longName, ev.ClipNames[clipID], "no max display name length cap (SUT BUG 3)")
	})
}

// ── Group 5: Pathological Keys (clip ID dedup edge cases) ──────────────
//
// Pins the dedupTrimmedClipIDs edge cases: empty IDs are filtered,
// whitespace IDs are trimmed + filtered, duplicate IDs are
// deduped, and very long IDs are passed through verbatim
// (SUT BUG 4: no max ID length cap).
func TestPathologicalInputs_P2C_Group5_PathologicalKeys(t *testing.T) {
	t.Parallel()

	t.Run("empty_ids_filtered", func(t *testing.T) {
		t.Parallel()
		const clipID = "valid"
		clip := newP2CAsset(clipID, "Valid", "", "hello", "https://drive/"+clipID)
		ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{"", clipID})
		require.NoError(t, err)
		require.NotNil(t, ev)
		assert.Equal(t, []string{clipID}, ev.AcceptedClipIDs, "empty ID filtered, valid ID kept")
	})

	t.Run("whitespace_ids_filtered", func(t *testing.T) {
		t.Parallel()
		const clipID = "valid"
		clip := newP2CAsset(clipID, "Valid", "", "hello", "https://drive/"+clipID)
		ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{"   ", "\t\t", clipID})
		require.NoError(t, err)
		require.NotNil(t, ev)
		assert.Equal(t, []string{clipID}, ev.AcceptedClipIDs, "whitespace IDs filtered, valid ID kept")
	})

	t.Run("duplicate_ids_deduped", func(t *testing.T) {
		t.Parallel()
		const clipID = "dup"
		clip := newP2CAsset(clipID, "Dup", "", "hello", "https://drive/"+clipID)
		ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{clipID, clipID, clipID})
		require.NoError(t, err)
		require.NotNil(t, ev)
		assert.Equal(t, []string{clipID}, ev.AcceptedClipIDs, "duplicates deduped, order preserved (first-wins)")
	})

	t.Run("duplicate_ids_preserve_first", func(t *testing.T) {
		t.Parallel()
		// Order check: if the same ID appears with different
		// cases / whitespace, dedup keeps the FIRST occurrence.
		const clipID = "first"
		clip := newP2CAsset(clipID, "First", "", "hello", "https://drive/"+clipID)
		ev, err := runP2CBuild(t, map[string]*asset.Asset{clipID: clip}, []string{clipID, "  " + clipID + "  "})
		require.NoError(t, err)
		require.NotNil(t, ev)
		// After trim+dedup, both map to "first", so only one entry.
		assert.Equal(t, []string{clipID}, ev.AcceptedClipIDs, "trim+dedup, first occurrence wins")
	})

	t.Run("all_empty_ids_returns_error", func(t *testing.T) {
		t.Parallel()
		_, err := runP2CBuild(t, nil, []string{"", "   ", "\t\t"})
		require.Error(t, err, "all-empty IDs must error")
		assert.Contains(t, err.Error(), "no valid clip IDs", "canonical error message")
	})

	t.Run("mix_of_valid_and_invalid_ids", func(t *testing.T) {
		t.Parallel()
		const clipA = "valid-A"
		const clipB = "valid-B"
		clipAObj := newP2CAsset(clipA, "A", "", "hello A", "https://drive/"+clipA)
		clipBObj := newP2CAsset(clipB, "B", "", "hello B", "https://drive/"+clipB)
		ev, err := runP2CBuild(t, map[string]*asset.Asset{
			clipA: clipAObj,
			clipB: clipBObj,
		}, []string{"", "   ", clipA, "\t", clipB, clipA, "  "})
		require.NoError(t, err)
		require.NotNil(t, ev)
		assert.Equal(t, []string{clipA, clipB}, ev.AcceptedClipIDs, "invalid filtered, duplicates deduped, order preserved")
	})

	// SUT BUG 4: no max ID length cap.
	t.Run("very_long_id_passed_through_SUT_BUG_4", func(t *testing.T) {
		t.Parallel()
		longID := strings.Repeat("a", 10000)
		clip := newP2CAsset(longID, "LongID", "", "hello", "https://drive/"+longID)
		ev, err := runP2CBuild(t, map[string]*asset.Asset{longID: clip}, []string{longID})
		require.NoError(t, err)
		require.NotNil(t, ev)
		// SUT BUG 4: no max length cap. ID is passed through verbatim.
		assert.Equal(t, []string{longID}, ev.AcceptedClipIDs, "no max ID length cap (SUT BUG 4)")
		assert.Equal(t, "LongID", ev.ClipNames[longID], "display name resolved for very long ID")
	})
}

// p2cMetaBackedTranscriptReader is the P2.C test suite's
// TextTrackReader stub. It mirrors metaBackedTranscriptReader in
// the P0.E suite (different name because they live in different
// test packages — P2.C is in usecase_test, P0.E is in usecase).
// Routes legacy metadata_json["transcript"] content through the
// canonical Fase 4 strict-cutover TextTrackReader.FindReady.
// Hermetic: in-memory, no DB / Whisper involvement.
type p2cMetaBackedTranscriptReader struct {
	transcripts map[string]string
}

func (r *p2cMetaBackedTranscriptReader) FindReady(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	if text, ok := r.transcripts[assetID]; ok && kind == asset.TextTrackTranscript {
		return &asset.TextTrack{
			AssetID:       assetID,
			LanguageCode:  languageCode,
			TextKind:      asset.TextTrackTranscript,
			TextContent:   text,
			TextHash:      "p2c-stub-" + assetID,
			SourceVersion: "v1",
			Status:        asset.TextTrackReady,
		}, nil, nil
	}
	return nil, nil, nil
}

func (r *p2cMetaBackedTranscriptReader) ListReadyLanguages(_ context.Context, assetID string, _ asset.TextTrackKind) ([]string, error) {
	if _, ok := r.transcripts[assetID]; ok {
		return []string{"en"}, nil
	}
	return nil, nil
}
