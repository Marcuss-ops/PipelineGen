package youtube

import (
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// newMinimalCmdBuilder constructs a *ytdlp.CommandBuilder with minimal
// cfg for the BaseArgs delegation tests. Mirrors the pattern in
// internal/infrastructure/youtube/subtitles_test.go::newSubtitlesCmdBuilder.
// Per the prior dead-code disclosure in the infrastructure ctor, the
// builder falls back to the empty-config form when nil is passed; the
// newMinimalCmdBuilder mirrors the production use case where a real
// cfg is wired.
func newMinimalCmdBuilder(t *testing.T, jsRuntimePath string, useCookies bool) *ytdlp.CommandBuilder {
	t.Helper()
	cfg := &ytcfg.ExternalConfig{
		YtdlpPath:            "yt-dlp",
		YouTubeJSRuntimePath: jsRuntimePath,
		YouTubeCookiesPath:   "/secure/youtube.cookies.txt", // fixture path only; file is never read
	}
	_ = useCookies // cookiesPath is read directly by the builder; useCookies is a per-call flag
	return ytdlp.NewCommandBuilder(&ytcfg.Config{External: *cfg})
}

// TestYTDLPSubtitleAdapter_DelegatesToBaseArgs is the load-bearing
// PR-WIRE-SUBTITLE-FETCHER-ADAPTER regression guard: it asserts the
// canonical yt-dlp argv prefix from ytdlp.BaseArgs is present in the
// built args slice. Pre-PR the adapter manually appended only
// --write-auto-subs / --write-subs / --skip-download / --sub-langs en /
// --sub-format vtt / -o / <url> and bypassed the helper entirely,
// dropping --cookies / --js-runtime / --remote-components /
// --no-warnings / --extractor-args youtube:player_client=web,android.
// This test fails if a future agent reverts the BaseArgs delegation.
func TestYTDLPSubtitleAdapter_DelegatesToBaseArgs(t *testing.T) {
	cmdBuilder := newMinimalCmdBuilder(t, "/usr/bin/node", false)
	a := &YTDLPSubtitleAdapter{
		ytdlp:      &downloader.YTDLPDownloader{}, // not exercised by buildSubtitleArgs
		cmdBuilder: cmdBuilder,
		useCookies: false,
		log:        zap.NewNop(),
	}

	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")

	// Canonical BaseArgs output must be present.
	baseArgs := cmdBuilder.BaseArgs(videoURL, false)
	baseArgsStr := strings.Join(baseArgs, " ")
	for _, expected := range []string{"--no-warnings", "--extractor-args"} {
		if !strings.Contains(baseArgsStr, expected) {
			t.Fatalf("BaseArgs did not produce %q; the helper itself regressed (test infra issue, not the adapter)", expected)
		}
		if !containsFlag(args, expected) {
			t.Fatalf("expected %q in built args (BaseArgs delegation regressed); got %v", expected, args)
		}
	}

	// Operation-specific flags must be present (NOT drift — these are
	// the subtitle adapter's caller-specific args per cmd_builder.go
	// godoc: "Callers MUST append their operation-specific flags...
	// to the returned slice.").
	for _, expected := range []string{
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en", "--sub-format", "vtt", "-o", videoURL,
	} {
		if !containsFlagOrValue(args, expected) {
			t.Fatalf("expected %q in built args; got %v", expected, args)
		}
	}
}

// TestYTDLPSubtitleAdapter_PlayerClientNeverWebOnly asserts the
// canonical --extractor-args youtube:player_client=web,android is
// present (NOT the legacy web-only client). Pre-PR the adapter
// dropped --extractor-args entirely (a godlike/07 silent-success
// regression on YouTube's n-challenge + age-restricted videos).
func TestYTDLPSubtitleAdapter_PlayerClientNeverWebOnly(t *testing.T) {
	a := &YTDLPSubtitleAdapter{
		ytdlp:      &downloader.YTDLPDownloader{},
		cmdBuilder: newMinimalCmdBuilder(t, "/usr/bin/node", false),
		useCookies: false,
		log:        zap.NewNop(),
	}

	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")

	extractorArgIdx := -1
	for i, a := range args {
		if a == "--extractor-args" && i+1 < len(args) {
			extractorArgIdx = i + 1
			break
		}
	}
	if extractorArgIdx == -1 {
		t.Fatalf("--extractor-args not found; got %v", args)
	}
	val := args[extractorArgIdx]
	if !strings.Contains(val, "youtube:player_client=") {
		t.Fatalf("expected --extractor-args value to contain 'youtube:player_client=', got %q", val)
	}
	if strings.Contains(val, "youtube:player_client=web") && !strings.Contains(val, "android") {
		t.Fatalf("--extractor-args is web-only; the f3f1ee90 web-first policy requires web,android. got %q", val)
	}
}

// TestYTDLPSubtitleAdapter_EmptyConfigDefaultsToNode asserts that
// --js-runtime IS injected with value "node" when
// YouTubeJSRuntimePath is empty (the NewCommandBuilder fallback
// contract: empty → "node" so yt-dlp always gets JS runtime for
// signature extraction, preventing 262-byte empty downloads).
func TestYTDLPSubtitleAdapter_EmptyConfigDefaultsToNode(t *testing.T) {
	a := &YTDLPSubtitleAdapter{
		ytdlp:      &downloader.YTDLPDownloader{},
		cmdBuilder: newMinimalCmdBuilder(t, "", false), // empty JS runtime path
		useCookies: false,
		log:        zap.NewNop(),
	}

	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")

	if !containsFlag(args, "--js-runtime") {
		t.Fatalf("--js-runtime must appear when YouTubeJSRuntimePath is empty (defaults to 'node'); got %v", args)
	}
	// Verify the defaulted value is "node"
	runtimeIdx := -1
	for i, a := range args {
		if a == "--js-runtime" && i+1 < len(args) {
			runtimeIdx = i + 1
			break
		}
	}
	if runtimeIdx == -1 || args[runtimeIdx] != "node" {
		t.Fatalf("--js-runtime value must be 'node' when YouTubeJSRuntimePath is empty; got %v", args)
	}
}

// TestYTDLPSubtitleAdapter_NChallengeReachable is the canonical
// PR-WIRE-SUBTITLE-FETCHER-ADAPTER n-challenge regression guard.
// Pre-PR the adapter bypassed BaseArgs entirely, so --cookies was
// NEVER injected — n-challenge / age-restricted YouTube videos
// silently failed. Post-PR (useCookies=true) the BaseArgs prefix
// injects --cookies, and the n-challenge case is reachable.
func TestYTDLPSubtitleAdapter_NChallengeReachable(t *testing.T) {
	cfg := &ytcfg.ExternalConfig{
		YtdlpPath:          "yt-dlp",
		YouTubeCookiesPath: "/secure/youtube.cookies.txt",
	}
	a := &YTDLPSubtitleAdapter{
		ytdlp:      &downloader.YTDLPDownloader{},
		cmdBuilder: ytdlp.NewCommandBuilder(&ytcfg.Config{External: *cfg}),
		useCookies: true, // n-challenge path
		log:        zap.NewNop(),
	}

	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")

	// --cookies must be present (useCookies=true).
	cookiesIdx := -1
	for i, a := range args {
		if a == "--cookies" && i+1 < len(args) {
			cookiesIdx = i + 1
			break
		}
	}
	if cookiesIdx == -1 {
		t.Fatalf("--cookies not found when useCookies=true; the n-challenge case is unreachable. got %v", args)
	}
	if got := args[cookiesIdx]; got != "/secure/youtube.cookies.txt" {
		t.Fatalf("--cookies value mismatch: expected %q, got %q", "/secure/youtube.cookies.txt", got)
	}

	// Inverse case: useCookies=false must NOT inject --cookies.
	a.useCookies = false
	args2 := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")
	if containsFlag(args2, "--cookies") {
		t.Fatalf("--cookies must NOT appear when useCookies=false; got %v", args2)
	}
}

// TestNewYTDLPSubtitleAdapter_NilCmdBuilderFallsBackToDefault verifies
// the nil-fallback contract (godlike/07 minimum-blast-radius): when
// CmdBuilder is nil, the constructor falls back to
// ytdlp.NewCommandBuilder(&ytcfg.Config{}) so the adapter degrades
// gracefully rather than nil-dereferencing at fetch time.
func TestNewYTDLPSubtitleAdapter_NilCmdBuilderFallsBackToDefault(t *testing.T) {
	a := NewYTDLPSubtitleAdapter(Deps{
		Ytdlp:      &downloader.YTDLPDownloader{},
		CmdBuilder: nil, // explicit nil → fallback expected
		UseCookies: false,
		Log:        zap.NewNop(),
	})
	if a.cmdBuilder == nil {
		t.Fatalf("expected cmdBuilder to fall back to default (ytdlp.NewCommandBuilder(&ytcfg.Config{})); got nil")
	}

	// Case 1: non-YouTube URL — the empty-config fallback should
	// produce NO anti-bot args (BaseArgs isYouTube-gated).
	nonYTURL := "https://vimeo.com/12345"
	args := a.buildSubtitleArgs(nonYTURL, "/tmp/test/subs.%(ext)s")
	if containsFlag(args, "--extractor-args") {
		t.Fatalf("empty-config fallback should not inject --extractor-args for non-YouTube URL; got %v", args)
	}
	if containsFlag(args, "--cookies") {
		t.Fatalf("empty-config fallback should not inject --cookies (useCookies=false); got %v", args)
	}
	if containsFlag(args, "--js-runtime") {
		t.Fatalf("empty-config fallback should not inject --js-runtime for non-YouTube URL; got %v", args)
	}

	// Case 2: YouTube URL — the canonical anti-bot args are
	// present (godlike/06 SSOT contract: ytdlp.BaseArgs is the SOLE
	// emitter of --no-warnings + --extractor-args for YouTube URLs,
	// regardless of the cfg content). --js-runtime defaults to
	// "node" when cfg is empty (the fallback contract).
	ytURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args2 := a.buildSubtitleArgs(ytURL, "/tmp/test/subs.%(ext)s")
	if !containsFlag(args2, "--no-warnings") {
		t.Fatalf("empty-config fallback should still inject --no-warnings for YouTube URL; got %v", args2)
	}
	if !containsFlag(args2, "--extractor-args") {
		t.Fatalf("empty-config fallback should still inject --extractor-args for YouTube URL (godlike/06 SSOT); got %v", args2)
	}
	if containsFlag(args2, "--cookies") {
		t.Fatalf("empty-config fallback should not inject --cookies (useCookies=false + empty cfg); got %v", args2)
	}
	// --js-runtime defaults to "node" when cfg is empty
	if !containsFlag(args2, "--js-runtime") {
		t.Fatalf("empty-config fallback should inject --js-runtime with default 'node' for YouTube URL; got %v", args2)
	}
	// Verify the defaulted value is "node"
	runtimeIdx := -1
	for i, a := range args2 {
		if a == "--js-runtime" && i+1 < len(args2) {
			runtimeIdx = i + 1
			break
		}
	}
	if runtimeIdx == -1 || args2[runtimeIdx] != "node" {
		t.Fatalf("--js-runtime value must be 'node' when cfg is empty; got %v", args2)
	}
}

// containsFlag reports whether args contains the exact flag string.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// containsFlagOrValue reports whether args contains the flag OR a
// value matching needle. Used for the operation-specific flag check
// where some needles are flags ("--sub-langs") and others are values
// ("en", the URL).
func containsFlagOrValue(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}

// ── parseVTTBlock unit tests (PR-VTT-STYLE-ANNOTATED-TEST, July 2026) ──

// TestParseVTTBlock_StyleAnnotatedTimestamp is the load-bearing
// regression guard for the PR-GEMMA-EXTRACT-IMPORTANT VTT parser fix
// (commit e925d674). YouTube auto-subs VTT appends align:/position:
// style info after the end timestamp:
//
//	00:00:02.310 --> 00:00:04.309 align:start position:0%
//
// Pre-fix, parseTimestampSeconds received "00:00:04.309 align:start
// position:0%" as one string, split on ":" into 7+ parts, and fell
// through to the default returning 0.0. Then end<=start and the block
// was silently discarded (579+ entries lost).
//
// Post-fix (strings.Fields[0]), only the bare "00:00:04.309" is
// passed to the parser. This test fails if the fix is reverted.
func TestParseVTTBlock_StyleAnnotatedTimestamp(t *testing.T) {
	block := "00:00:02.310 --> 00:00:04.309 align:start position:0%\nhello world"
	start, end, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false for style-annotated timestamp; regression of commit e925d674 (strings.Fields[0] fix)")
	}
	if start != 2.310 {
		t.Fatalf("start: expected 2.310, got %v", start)
	}
	if end != 4.309 {
		t.Fatalf("end: expected 4.309, got %v", end)
	}
	if text != "hello world" {
		t.Fatalf("text: expected 'hello world', got %q", text)
	}
}

// TestParseVTTBlock_StyleAnnotatedTimestamp_AlignOnly tests the case
// where only align: is appended (no position:).
func TestParseVTTBlock_StyleAnnotatedTimestamp_AlignOnly(t *testing.T) {
	block := "00:00:05.000 --> 00:00:10.500 align:start\nsome text here"
	start, end, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false for align:only style-annotated timestamp")
	}
	if start != 5.0 {
		t.Fatalf("start: expected 5.0, got %v", start)
	}
	if end != 10.5 {
		t.Fatalf("end: expected 10.5, got %v", end)
	}
	if text != "some text here" {
		t.Fatalf("text: expected 'some text here', got %q", text)
	}
}

// TestParseVTTBlock_StyleAnnotatedTimestamp_PositionOnly tests the case
// where only position: is appended (no align:).
func TestParseVTTBlock_StyleAnnotatedTimestamp_PositionOnly(t *testing.T) {
	block := "00:01:00.000 --> 00:01:30.000 position:15%\nposition-only test"
	start, end, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false for position:only style-annotated timestamp")
	}
	if start != 60.0 {
		t.Fatalf("start: expected 60.0, got %v", start)
	}
	if end != 90.0 {
		t.Fatalf("end: expected 90.0, got %v", end)
	}
	if text != "position-only test" {
		t.Fatalf("text: expected 'position-only test', got %q", text)
	}
}

// TestParseVTTBlock_StyleAnnotatedTimestamp_MultiLineText verifies that
// style-annotated timestamps work with multi-line VTT text bodies.
func TestParseVTTBlock_StyleAnnotatedTimestamp_MultiLineText(t *testing.T) {
	block := "00:00:00.080 --> 00:00:02.310 align:start position:0%\nline one\nline two\nline three"
	start, end, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false for multi-line text with style-annotated timestamp")
	}
	if start != 0.080 {
		t.Fatalf("start: expected 0.080, got %v", start)
	}
	if end != 2.310 {
		t.Fatalf("end: expected 2.310, got %v", end)
	}
	expectedText := "line one line two line three"
	if text != expectedText {
		t.Fatalf("text: expected %q, got %q", expectedText, text)
	}
}

// TestParseVTTBlock_StyleAnnotatedTimestamp_AlignPositionLinesFiltered
// verifies that align:/position: cue lines WITHIN the text body are
// filtered out (not treated as text content).
func TestParseVTTBlock_StyleAnnotatedTimestamp_AlignPositionLinesFiltered(t *testing.T) {
	block := "00:00:01.000 --> 00:00:03.000 align:start position:0%\nalign:start\nposition:0%\nreal text here"
	_, _, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false")
	}
	// The align:start and position:0% lines within the body must be filtered.
	if text != "real text here" {
		t.Fatalf("text: expected 'real text here' (align:/position: body lines filtered), got %q", text)
	}
}

// TestParseVTTBlock_CleanTimestamp_NoAnnotations tests the baseline
// case with no style annotations on the timestamp line.
func TestParseVTTBlock_CleanTimestamp_NoAnnotations(t *testing.T) {
	block := "00:01:00.000 --> 00:01:30.000\nbaseline text"
	start, end, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false for clean timestamp (no annotations)")
	}
	if start != 60.0 {
		t.Fatalf("start: expected 60.0, got %v", start)
	}
	if end != 90.0 {
		t.Fatalf("end: expected 90.0, got %v", end)
	}
	if text != "baseline text" {
		t.Fatalf("text: expected 'baseline text', got %q", text)
	}
}

// TestParseVTTBlock_StripXMLTags verifies that inline XML tags are
// stripped from the text content.
func TestParseVTTBlock_StripXMLTags(t *testing.T) {
	block := "00:00:00.000 --> 00:00:05.000\n<c>bold text</c> and <i>italic</i> normal"
	_, _, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false for XML-tagged text")
	}
	if text != "bold text and italic normal" {
		t.Fatalf("text: expected 'bold text and italic normal', got %q", text)
	}
}

// TestParseVTTBlock_StyleAnnotatedTimestamp_StartSide verifies
// that style annotations on the START timestamp are also handled.
func TestParseVTTBlock_StyleAnnotatedTimestamp_StartSide(t *testing.T) {
	block := "00:00:02.310 align:start --> 00:00:04.309\ntext"
	start, end, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false for style-annotated start timestamp")
	}
	if start != 2.310 {
		t.Fatalf("start: expected 2.310, got %v", start)
	}
	if end != 4.309 {
		t.Fatalf("end: expected 4.309, got %v", end)
	}
	if text != "text" {
		t.Fatalf("text: expected 'text', got %q", text)
	}
}

// TestParseVTTBlock_StyleAnnotatedTimestamp_BothSides verifies that
// style annotations on BOTH timestamps are handled correctly.
func TestParseVTTBlock_StyleAnnotatedTimestamp_BothSides(t *testing.T) {
	block := "00:00:02.310 align:start --> 00:00:04.309 position:0%\ntext"
	start, end, text, ok := parseVTTBlock(block)
	if !ok {
		t.Fatalf("parseVTTBlock returned ok=false for both-sides style-annotated timestamps")
	}
	if start != 2.310 {
		t.Fatalf("start: expected 2.310, got %v", start)
	}
	if end != 4.309 {
		t.Fatalf("end: expected 4.309, got %v", end)
	}
	if text != "text" {
		t.Fatalf("text: expected 'text', got %q", text)
	}
}

// ── Negative cases (godlike/07 fail-closed at input) ──

// TestParseVTTBlock_EmptyBlock returns ok=false.
func TestParseVTTBlock_EmptyBlock(t *testing.T) {
	_, _, _, ok := parseVTTBlock("")
	if ok {
		t.Fatalf("parseVTTBlock should return ok=false for empty block")
	}
}

// TestParseVTTBlock_NoTimestampLine returns ok=false.
func TestParseVTTBlock_NoTimestampLine(t *testing.T) {
	_, _, _, ok := parseVTTBlock("just some text\nno timestamp here")
	if ok {
		t.Fatalf("parseVTTBlock should return ok=false for block with no timestamp line")
	}
}

// TestParseVTTBlock_TimestampOnly_NoText returns ok=false.
func TestParseVTTBlock_TimestampOnly_NoText(t *testing.T) {
	_, _, _, ok := parseVTTBlock("00:00:01.000 --> 00:00:02.000")
	if ok {
		t.Fatalf("parseVTTBlock should return ok=false for timestamp-only block (no text)")
	}
}

// TestParseVTTBlock_EndBeforeStart returns ok=false.
func TestParseVTTBlock_EndBeforeStart(t *testing.T) {
	_, _, _, ok := parseVTTBlock("00:01:00.000 --> 00:00:30.000\ntext")
	if ok {
		t.Fatalf("parseVTTBlock should return ok=false when end <= start")
	}
}

// TestParseVTTBlock_MalformedTimestamp_NoArrow returns ok=false.
func TestParseVTTBlock_MalformedTimestamp_NoArrow(t *testing.T) {
	_, _, _, ok := parseVTTBlock("00:00:01.000 00:00:02.000\ntext")
	if ok {
		t.Fatalf("parseVTTBlock should return ok=false when timestamp line has no '-->' arrow")
	}
}

// TestParseVTTBlock_EndEqualsStart returns ok=false (duration must be > 0).
func TestParseVTTBlock_EndEqualsStart(t *testing.T) {
	_, _, _, ok := parseVTTBlock("00:01:00.000 --> 00:01:00.000\ntext")
	if ok {
		t.Fatalf("parseVTTBlock should return ok=false when end == start (zero duration)")
	}
}

// ── stripVTTHeader tests ──

// TestStripVTTHeader_RemovesHeader strips the WEBVTT header block.
func TestStripVTTHeader_RemovesHeader(t *testing.T) {
	input := "WEBVTT\nKind: captions\nLanguage: en\n\n00:00:01.000 --> 00:00:02.000\nfirst cue"
	result := stripVTTHeader(input)
	if strings.HasPrefix(result, "WEBVTT") {
		t.Fatalf("stripVTTHeader should remove the WEBVTT header; got prefix still present")
	}
	if !strings.HasPrefix(strings.TrimSpace(result), "00:00:01.000") {
		t.Fatalf("stripVTTHeader should leave the first cue after the blank line; got: %q", result[:50])
	}
}

// TestStripVTTHeader_NoHeader_Passthrough returns the content unchanged
// when there is no WEBVTT header.
func TestStripVTTHeader_NoHeader_Passthrough(t *testing.T) {
	input := "00:00:01.000 --> 00:00:02.000\nfirst cue"
	result := stripVTTHeader(input)
	if result != input {
		t.Fatalf("stripVTTHeader should passthrough content without WEBVTT header unchanged")
	}
}

// ── stripXMLTags tests ──

// TestStripXMLTags_RemovesInlineTags strips <c>, <i>, <b> tags.
func TestStripXMLTags_RemovesInlineTags(t *testing.T) {
	cases := []struct {
		name, input, expected string
	}{
		{"simple italic", "<i>italic text</i>", "italic text"},
		{"simple bold", "<b>bold text</b>", "bold text"},
		{"class tag", "<c>class text</c>", "class text"},
		{"nested", "<i><b>nested</b></i>", "nested"},
		{"mid sentence", "hello <i>world</i> test", "hello world test"},
		{"no tags", "plain text", "plain text"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := stripXMLTags(tc.input)
			if result != tc.expected {
				t.Fatalf("stripXMLTags(%q): expected %q, got %q", tc.input, tc.expected, result)
			}
		})
	}
}

// ── parseTimestampSeconds tests ──

// TestParseTimestampSeconds_AllFormats verifies HH:MM:SS.mmm, MM:SS.mmm,
// and bare seconds.
func TestParseTimestampSeconds_AllFormats(t *testing.T) {
	cases := []struct {
		input    string
		expected float64
	}{
		{"01:30:45.500", 1*3600 + 30*60 + 45.5},
		{"05:30.250", 5*60 + 30.25},
		{"42.750", 42.75},
		{"00:00:00.080", 0.080},
		{"00:00.000", 0.0},
		{" 00:00:05.000 ", 5.0}, // whitespace trimmed
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			result := parseTimestampSeconds(tc.input)
			if result != tc.expected {
				t.Fatalf("parseTimestampSeconds(%q): expected %v, got %v", tc.input, tc.expected, result)
			}
		})
	}
}
