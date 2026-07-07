// Package transcripts — ytdlp_subtitles.go slim orchestrator (PR-SPLIT-YTDLP-SUBTITLES, July 2026).
//
// Concrete adapter that satisfies monitor.TranscriptProvider.
//
// Step 9 commit 2 (June 2026, Channel Monitor Blocco 6 architectural
// rewrite): the YTDLPSubtitleAdapter is the canonical place where the
// pre-Step-9 monitor's "matchSemantically" VTT download + "vtt_helpers.go"
// regex parsing + temp-file lifecycle migrated. Per AGENTS.md
// Pattern 0 (port abstraction), the monitor package NEVER imports
// os/exec, the VTT regex helpers, or temp-file cleanup — those concerns
// are owned here (the application-layer sibling package of monitor/).
//
// Surface area:
//
//   - GetTranscript(ctx, videoURL) (string, error): the port method.
//     Returns the concatenated plain-text transcript. 8000-char cap, 10-word
//     sanity threshold (both produce typed errors rather than empty
//     strings, so the analyzer can distinguish "no transcript available"
//     from "transcript yielded nothing meaningful").
//
//   - GetTimedTranscript(ctx, videoURL) ([]TranscriptEntry, error):
//     SIBLING-ACCESSIBLE (NOT a port method). The OllamaAnalyzer reads
//     this when constructing the timestamped FindSegments prompt; the
//     port surface intentionally does NOT expose it because no other
//     consumer outside this adapter-siblings pair needs it. The underlying
//     VTT download + parse is identical to GetTranscript but returns
//     the timed entries instead of joining to a single string.
//
// Sibling-adapter layout:
//
//	monitor/         (the orchestrator; calls transcript port only)
//	    |
//	    uses ─────► transcripts/  (this file; owns VTT + os/exec)
//	                     ▲
//	                     │
//	                properties (GetTimedTranscript is accessed across)
//	                     │
//	                semantic/ (OllamaAnalyzer consumes GetTimedTranscript
//	                           when assembling FindSegments LLM prompt).
//
// 2-file split (PR-SPLIT-YTDLP-SUBTITLES, P2, deadline 2026-08-08):
//   - ytdlp_subtitles.go (this file) — THIN orchestrator: Deps +
//     YTDLPSubtitleAdapter struct + NewYTDLPSubtitleAdapter +
//     GetTranscript + GetTimedTranscript + Fetch + inheritOrWithTimeout +
//     buildSubtitleArgs + fetchTimedTranscript + var _ compile-time pin.
//   - ytdlp_subtitles_vtt.go — the 4 VTT parser helpers (stripVTTHeader
//   - parseVTTBlock + parseTimestampSeconds + stripXMLTags).
package transcripts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	monitor "github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Deps is the ctor payload for NewYTDLPSubtitleAdapter. The ytdlp
// dependency is concrete (NewYTDLP(cfg) at composition time) — there
// is no intermediate port for it because the channel-monitor and the
// subtitles adapter share the same binary path / cookies / JS-runtime
// config; abstracting this further would only add plumbing.
//
// PR-WIRE-SUBTITLE-FETCHER-ADAPTER (2026-07-06): CmdBuilder + UseCookies
// added so the adapter delegates the canonical yt-dlp argv prefix to
// ytdlp.BaseArgs (same Pattern 0 port the infrastructure-layer
// SubtitleFetcherAdapter uses post PR-SUBTITLES-BASEARGS-MIGRATION).
// Pre-PR the adapter manually appended --write-auto-subs / --write-subs
// / --skip-download / --sub-langs / --sub-format / -o / <url> via
// exec.CommandContext, DROPPING --cookies (required for n-challenge +
// age-restricted YouTube videos), --js-runtime + --remote-components
// ejs:github, --no-warnings, and --extractor-args
// youtube:player_client=web,android. CmdBuilder is the canonical
// owner of those 4-5 args (godlike/06 SSOT, see
// internal/infrastructure/ytdlp/cmd_builder.go); nil falls back to
// ytdlp.NewCommandBuilder(&ytcfg.Config{}) so the adapter degrades
// gracefully rather than nil-dereferencing. UseCookies=true is
// required for age-restricted and n-challenge-protected YouTube
// videos (the canonical n-challenge case); false for public
// auto-generated subtitles. Both flags are set at wire-time.
type Deps struct {
	Ytdlp      *downloader.YTDLPDownloader
	CmdBuilder *ytdlp.CommandBuilder
	UseCookies bool
	Log        *zap.Logger
}

// YTDLPSubtitleAdapter implements monitor.TranscriptProvider. Holds the
// concrete *downloader.YTDLPDownloader + the live logging handle. The
// three numeric knobs (maxTranscriptLen, minTranscriptWords, timeout)
// are pre-set to the pre-Step-9 values and not exposed as setters —
// if a future operator wants to tune them, surface them through cfg +
// the ctor.
type YTDLPSubtitleAdapter struct {
	ytdlp              *downloader.YTDLPDownloader
	cmdBuilder         *ytdlp.CommandBuilder
	useCookies         bool
	log                *zap.Logger
	maxTranscriptLen   int
	minTranscriptWords int
	timeout            time.Duration
}

// NewYTDLPSubtitleAdapter constructs the adapter with the canonical
// pre-Step-9 defaults baked in (8000-char truncation, 10-word minimum,
// 60-second subprocess timeout).
func NewYTDLPSubtitleAdapter(d Deps) *YTDLPSubtitleAdapter {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	if d.CmdBuilder == nil {
		d.CmdBuilder = ytdlp.NewCommandBuilder(&ytcfg.Config{})
	}
	return &YTDLPSubtitleAdapter{
		ytdlp:              d.Ytdlp,
		cmdBuilder:         d.CmdBuilder,
		useCookies:         d.UseCookies,
		log:                d.Log,
		maxTranscriptLen:   8000,
		minTranscriptWords: 10,
		timeout:            60 * time.Second,
	}
}

// GetTranscript satisfies monitor.TranscriptProvider.
//
// Steps:
//  1. Spawn `yt-dlp --write-auto-subs --write-subs --skip-download
//     --sub-langs en --sub-format vtt -o <tempdir>/subs <videoURL>`.
//     The `en` lang cap + vtt format match the pre-Step-9 behavior.
//  2. Read the downloaded `subs.*.vtt` file from the temp dir.
//  3. Strip the WEBVTT header (regexRemoveVTTHeader-style split on
//     the first \n\n after "WEBVTT").
//  4. Filter lines: skip empties, skip timestamp lines (-->), skip
//     NOTE blocks; strip XML tags via the per-rune scanner.
//  5. Join the surviving lines into a single string; truncate to
//     8000 chars (avoids LLM context overflow); reject < 10 words
//     (transcript-miss signal).
//
// Errors are typed:
//   - "create temp dir: ..." (path-related, usually permission issues)
//   - "subtitle download failed: ..." (subprocess exit non-zero)
//   - "no subtitle file found for video: ..." (VTT file absent after run)
//   - "read vtt file: ..." (file read failure)
//   - "transcript too short (%d words), skipping"
//
// The subprocess uses exec.CommandContext with d.timeout ignored —
// the timeout comes from the parent ctx (analyzer.go builds the
// per-video ctx with a sensible cancellation).
//
// Commit G forward-pointer: GetTranscript delegates to Fetch thendrops
// the timed Entries; Fetch returns TranscriptDocument so callers can
// re-emit cues without re-downloading the VTT file.
// Deprecated since Commit G: prefer Fetch for new callers. Kept as
// sibling method on the adapter for back-compat — the legacy
// monitor.TranscriptProvider.GetTranscript contract is unchanged.
func (a *YTDLPSubtitleAdapter) GetTranscript(ctx context.Context, videoURL string) (string, error) {
	entries, err := a.fetchTimedTranscript(ctx, videoURL)
	if err != nil {
		return "", err
	}
	// Render entries as a single transcript string.
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Text)
		sb.WriteString(" ")
	}
	transcript := strings.TrimSpace(sb.String())

	if len(transcript) > a.maxTranscriptLen {
		transcript = transcript[:a.maxTranscriptLen]
	}
	if wordCount := len(strings.Fields(transcript)); wordCount < a.minTranscriptWords {
		return "", fmt.Errorf("transcript too short (%d words), skipping", wordCount)
	}

	a.log.Debug("YTDLPSubtitleAdapter GetTranscript succeeded",
		zap.Int("entries", len(entries)),
		zap.Int("transcript_len", len(transcript)),
		zap.Int("word_count", len(strings.Fields(transcript))))
	return transcript, nil
}

// GetTimedTranscript returns the VTT-cued entries as a structured slice.
// Accessible to sibling adapters (OllamaAnalyzer consume it for the
// FindSegments prompt); NOT exposed through monitor.TranscriptProvider
// because no consumer outside this adapter-siblings pair needs it.
//
// Implementation note: shares the `fetchTimedTranscript` helper with
// GetTranscript — the only difference is what gets returned to the
// caller (timed entries vs joined string). This means a single VTT
// download + parse per call (no double-fetch).
//
// Commit G forward-pointer: GetTimedTranscript is the legacy sibling-
// adapter API; new callers should use Fetch(ctx, videoURL) which
// returns a TranscriptDocument containing the same Entries slice.
// Kept here for OllamaAnalyzer.FindSegments back-compat (the
// OLLAM-A path still re-fetches via GetTimedTranscript when called
// via the legacy 3-method VideoAnalyzer surface; new AnalyzeFull
// path uses Fetch once and reads doc.Entries).
func (a *YTDLPSubtitleAdapter) GetTimedTranscript(ctx context.Context, videoURL string) ([]TranscriptEntry, error) {
	return a.fetchTimedTranscript(ctx, videoURL)
}

// Fetch satisfies monitor.TranscriptProvider (Commit G, June 2026).
//
// Returns a TranscriptDocument carrying:
//   - VideoID parsed from videoURL (via pkg/urlutil.ExtractVideoID).
//   - Language — defaults to "en" (matches the canonical
//     YTDLPSubtitleAdapter --sub-langs en; future per-channel
//     language overrides are a P1 follow-up).
//   - Source — "asr" (auto-subs is the only path today;
//     "manual" surfaces via TestWithManualSubs flag on the ctor —
//     a tight loop until production sees a "manual > asr" channel).
//   - Entries — the entire VTT-cued transcript (NOT truncated).
//   - Text — concatenated + capped to maxTranscriptLen (8000 chars).
//   - DurationSec — end-of-last-entry timestamp.
//   - FetchedAt — UTC now() at Fetch return time.
//
// The subprocess invocation is wrapped in an EXPLICIT
// context.WithTimeout(parent, a.timeout). If the parent ctx
// already carries a shorter deadline, that deadline wins
// (do not silently EXTEND a hard parent deadline to a.timeout).
// Returns:
//
//   - "analyzeVideo: Fetch(<videoID>): context deadline exceeded /
//     canceled" (parent deadline fired before yt-dlp exited)
//   - the typed errors from GetTranscript for the persistent
//     error surface
//
// Commit G invariant: this is the SINGLE call site for yt-dlp
// subprocess invocation in the canonical monitor.AnalyzeVideo
// flow. The orchestrator never invokes GetTranscript +
// GetTimedTranscript separately — Fetch's TranscriptDocument
// carries both the joined text (for legacy Score/Classify) and
// the timed Entries (for new AnalyzeFull's windowed sampling).
func (a *YTDLPSubtitleAdapter) Fetch(parent context.Context, videoURL string) (TranscriptDocument, error) {
	// Step 1: explicit timeout wrapping. inheritFrom(parent) returns
	// (ctx, ok=false) when parent carries no deadline; we always wrap
	// the inner context with a fresh WithTimeout(parent, a.timeout).
	ctx, cancel := inheritOrWithTimeout(parent, a.timeout)
	defer cancel()

	// Step 2: run the canonical VTT download + parse.
	entries, err := a.fetchTimedTranscript(ctx, videoURL)
	if err != nil {
		// Preserve ctx-aware error type for callers that need to
		// distinguish ctx.DeadlineExceeded (transient) from
		// "no subtitle file found" (terminal). The wrapped chain
		// carries both: errors.Is(err, context.DeadlineExceeded)
		// works whether the inner is the typed GetTranscript
		// error or the parent cancellation.
		return TranscriptDocument{}, err
	}

	if len(entries) == 0 {
		return TranscriptDocument{}, fmt.Errorf("transcript empty for video (0 timed entries): %s", videoURL)
	}

	// Step 3: assemble the TranscriptDocument.
	videoID := extractVideoID(videoURL)
	var sb strings.Builder
	maxLen := a.maxTranscriptLen
	if maxLen <= 0 {
		maxLen = 8000
	}
	for _, e := range entries {
		sb.WriteString(e.Text)
		sb.WriteString(" ")
		if sb.Len() > maxLen*2 {
			// overshoot guard: stop once we've exceeded 2x the cap;
			// the join below truncates to maxLen anyway.
			break
		}
	}
	text := strings.TrimSpace(sb.String())
	if len(text) > maxLen {
		text = text[:maxLen]
	}
	if wordCount := len(strings.Fields(text)); wordCount < a.minTranscriptWords {
		return TranscriptDocument{}, fmt.Errorf("transcript too short (%d words), skipping", wordCount)
	}

	lastEnd := entries[len(entries)-1].End
	doc := TranscriptDocument{
		VideoID:     videoID,
		Language:    "en",
		Source:      "asr",
		Text:        text,
		DurationSec: lastEnd,
		Entries:     entries,
		FetchedAt:   time.Now().UTC(),
	}
	a.log.Debug("YTDLPSubtitleAdapter Fetch succeeded",
		zap.String("video_id", videoID),
		zap.Int("entries", len(entries)),
		zap.Int("text_len", len(text)),
		zap.Float64("duration_sec", lastEnd),
		zap.Int("word_count", len(strings.Fields(text))))
	return doc, nil
}

// inheritOrWithTimeout returns the parent context unchanged when it
// already carries a deadline (or cancellation); otherwise wraps a
// fresh context.WithTimeout(parent, timeout). The cancel func is
// returned in both cases so the caller can defer cancel() uniformly —
// calling cancel on an already-deadlined parent is a no-op
// (Go stdlib guarantees cancel() is idempotent).
func inheritOrWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := parent.Deadline(); hasDeadline {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

// buildSubtitleArgs assembles the canonical yt-dlp argv for a subtitle
// fetch. Extracted from fetchTimedTranscript so hermetic TDD tests
// can assert the BaseArgs delegation contract WITHOUT spawning a
// real yt-dlp subprocess.
//
// PR-WIRE-SUBTITLE-FETCHER-ADAPTER (2026-07-06): the canonical
// yt-dlp argv is built in 3 layers —
//  1. baseArgs (the canonical 4-5 anti-bot flags from ytdlp.BaseArgs)
//  2. operation-specific flags (--write-auto-subs / --write-subs /
//     --skip-download / --sub-langs en / --sub-format vtt / -o)
//  3. positional URL (appended last; yt-dlp accepts global options
//     before OR after the positional URL)
//
// outputTemplate is the -o flag value (e.g.
// `<tempDir>/subs.%(ext)s`). Test callers pass a placeholder
// string to avoid filesystem dependencies.
func (a *YTDLPSubtitleAdapter) buildSubtitleArgs(videoURL, outputTemplate string) []string {
	baseArgs := a.cmdBuilder.BaseArgs(videoURL, a.useCookies)
	args := append([]string{}, baseArgs...)
	args = append(args,
		"--write-auto-subs",
		"--write-subs",
		"--skip-download",
		"--sub-langs", "en",
		"--sub-format", "vtt",
		"-o", outputTemplate,
		videoURL,
	)
	return args
}

// fetchTimedTranscript downloads the VTT file via os/exec and parses
// the timed entries (start + end + cleaned-text). Used by both
// port-facing methods.
//
// The dedup pass that the pre-Step-9 segment_finder.go ran (substring
// subsumption check) is intentionally NOT replicated here: the
// OllamaAnalyzer.FindSegments prompt is constructed from this slice
// and the LLM-side dedup is what mattered in practice — pre-Step-9
// segment_finder.go run the substring dedup BEFORE handing the
// prompt to the LLM, but the LLM's output was stable either way per
// the operator logs. This is a documented simplification.
func (a *YTDLPSubtitleAdapter) fetchTimedTranscript(ctx context.Context, videoURL string) ([]TranscriptEntry, error) {
	if a.ytdlp == nil {
		return nil, fmt.Errorf("YTDLPSubtitleAdapter: ytdlp not wired (composition bug — call downloader.NewYTDLP(cfg) in lifecycle.go)")
	}

	tempDir, err := os.MkdirTemp("", "ytdlp_subs_*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// yt-dlp subprocess invocation.
	//
	// PR-WIRE-SUBTITLE-FETCHER-ADAPTER (2026-07-06): prepend the
	// canonical yt-dlp argv prefix via a.cmdBuilder.BaseArgs. Pre-PR
	// this slice manually appended --write-auto-subs / --write-subs /
	// --skip-download and bypassed the helper entirely, dropping
	// --cookies (required for n-challenge + age-restricted videos),
	// --js-runtime + --remote-components ejs:github (required for
	// node-based signature resolution), --no-warnings, and
	// --extractor-args youtube:player_client=web,android (the
	// f3f1ee90 web-first policy). Mirror internal/infrastructure/youtube/subtitles.go:128
	// + metadata.go:97 patterns: BaseArgs returns the prefix WITHOUT
	// the URL, so the URL is appended at the end alongside the -o
	// output template (yt-dlp accepts global options before OR after
	// the positional URL).
	//
	// The --write-auto-subs / --write-subs / --skip-download /
	// --sub-langs en / --sub-format vtt flags are caller-specific
	// (per the cmd_builder.go godoc: "Callers MUST append their
	// operation-specific flags... to the returned slice."), so they
	// stay inline — they are NOT drift, they are the subtitle
	// adapter's operation-specific args.
	subArgs := a.buildSubtitleArgs(videoURL, filepath.Join(tempDir, "subs"))
	subCmd := exec.CommandContext(ctx, a.ytdlp.Path(), subArgs...)
	if out, err := subCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("subtitle download failed: %w, output: %s", err, string(out))
	}

	// Find the VTT file (the prefix `subs.` + the suffix `.vtt` make
	// the glob unambiguous on the yt-dlp output template).
	var vttPath string
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, fmt.Errorf("read temp dir: %w", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "subs.") && strings.HasSuffix(entry.Name(), ".vtt") {
			vttPath = filepath.Join(tempDir, entry.Name())
			break
		}
	}
	if vttPath == "" {
		return nil, fmt.Errorf("no subtitle file found for video: %s", videoURL)
	}

	// Read + strip WEBVTT header.
	vttData, err := os.ReadFile(vttPath)
	if err != nil {
		return nil, fmt.Errorf("read vtt file: %w", err)
	}
	content := stripVTTHeader(string(vttData))

	// Parse the timed blocks. Format reminder:
	//   00:00:07.000 --> 00:00:10.000 align:center position:50%
	//   All right, here we go.
	//
	//   00:00:10.000 --> 00:00:13.000
	//   welcome back to Vlad TV.
	var out []TranscriptEntry
	for _, block := range strings.Split(content, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		start, end, text, ok := parseVTTBlock(block)
		if !ok || text == "" {
			continue
		}
		out = append(out, TranscriptEntry{
			Start: start,
			End:   end,
			Text:  text,
		})
	}
	return out, nil
}

// Compile-time assertion: YTDLPSubtitleAdapter must satisfy
// monitor.TranscriptProvider. Per AGENTS.md Pattern 0 / godlike/06
// §"Database and config ownership": any signature drift becomes a
// build failure here, not a runtime panic.
var _ monitor.TranscriptProvider = (*YTDLPSubtitleAdapter)(nil)
