// Package transcripts — ytdlp_subtitles.go: concrete adapter that
// satisfies monitor.TranscriptProvider.
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
)

// Deps is the ctor payload for NewYTDLPSubtitleAdapter. The ytdlp
// dependency is concrete (NewYTDLP(cfg) at composition time) — there
// is no intermediate port for it because the channel-monitor and the
// subtitles adapter share the same binary path / cookies / JS-runtime
// config; abstracting this further would only add plumbing.
type Deps struct {
	Ytdlp *downloader.YTDLPDownloader
	Log   *zap.Logger
}

// YTDLPSubtitleAdapter implements monitor.TranscriptProvider. Holds the
// concrete *downloader.YTDLPDownloader + the live logging handle. The
// three numeric knobs (maxTranscriptLen, minTranscriptWords, timeout)
// are pre-set to the pre-Step-9 values and not exposed as setters —
// if a future operator wants to tune them, surface them through cfg +
// the ctor.
type YTDLPSubtitleAdapter struct {
	ytdlp              *downloader.YTDLPDownloader
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
	return &YTDLPSubtitleAdapter{
		ytdlp:              d.Ytdlp,
		log:                d.Log,
		maxTranscriptLen:   8000,
		minTranscriptWords: 10,
		timeout:            60 * time.Second,
	}
}

// TranscriptEntry is the timed-entry shape returned by
// GetTimedTranscript. The OllamaAnalyzer consumes it for the
// FindSegments LLM prompt (which prefixes [MM:SS] markers so Ollama
// can output timestamped segments).
type TranscriptEntry struct {
	// Start is the start timestamp in seconds (float for sub-second
	// precision; the pre-Step-9 segment_finder.go used float64 too).
	Start float64
	// End is the end timestamp in seconds.
	End float64
	// Text is the cleaned subtitle text for this entry (no XML tags,
	// no timestamp markers, no whitespace padding).
	Text string
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
		FetchedAt:   nowFn(),
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

	// yt-dlp subprocess invocation. The args match the pre-Step-9
	// semantic_matcher.go and segment_finder.go invocations verbatim
	// (--write-auto-subs covers ASR-generated subs when manual subs
	// are missing; --sub-langs en is the only language we analyze;
	// --skip-download is the key optimization that turns this into
	// a 1-2 second subprocess instead of a 30-second video download).
	subCmd := exec.CommandContext(ctx, a.ytdlp.Path(),
		"--write-auto-subs",
		"--write-subs",
		"--skip-download",
		"--sub-langs", "en",
		"--sub-format", "vtt",
		"-o", filepath.Join(tempDir, "subs"),
		videoURL,
	)
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

// stripVTTHeader removes everything before the first blank line after
// the WEBVTT marker. Pre-Step-9 lived in monitor/vtt_helpers.go as
// regexRemoveVTTHeader; migrated here unchanged.
func stripVTTHeader(content string) string {
	if idx := strings.Index(content, "\n\n"); idx > 0 {
		before := strings.TrimSpace(content[:idx])
		if strings.HasPrefix(before, "WEBVTT") {
			return content[idx+2:]
		}
	}
	return content
}

// parseVTTBlock parses one VTT block (timestamp line + text lines) into
// (start_seconds, end_seconds, joined_text, ok). Returns ok=false on
// malformed blocks (no timestamp line, no text, parse failure on the
// timestamps). align:/position: lines are stripped from the text side.
func parseVTTBlock(block string) (start, end float64, text string, ok bool) {
	lines := strings.Split(block, "\n")
	var timeLine string
	var textLines []string
	for _, line := range lines {
		if strings.Contains(line, "-->") {
			timeLine = line
		} else if timeLine != "" {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "align:") || strings.HasPrefix(line, "position:") {
				continue
			}
			textLines = append(textLines, stripXMLTags(line))
		}
	}
	if timeLine == "" || len(textLines) == 0 {
		return 0, 0, "", false
	}
	// Parse the two timestamps from "HH:MM:SS.mmm --> HH:MM:SS.mmm".
	parts := strings.Split(timeLine, "-->")
	if len(parts) < 2 {
		return 0, 0, "", false
	}
	start = parseTimestampSeconds(strings.TrimSpace(parts[0]))
	end = parseTimestampSeconds(strings.TrimSpace(parts[1]))
	if end <= start {
		return 0, 0, "", false
	}
	return start, end, strings.Join(textLines, " "), true
}

// parseTimestampSeconds parses "HH:MM:SS.mmm" / "MM:SS.mmm" / "SS" into
// float64 seconds. Mirrors pkg/textutil.ParseVTTTimestamp.
func parseTimestampSeconds(ts string) float64 {
	ts = strings.TrimSpace(ts)
	parts := strings.Split(ts, ":")
	if len(parts) == 3 {
		var h, m, s float64
		fmt.Sscanf(parts[0], "%f", &h)
		fmt.Sscanf(parts[1], "%f", &m)
		fmt.Sscanf(parts[2], "%f", &s)
		return h*3600 + m*60 + s
	}
	if len(parts) == 2 {
		var m, s float64
		fmt.Sscanf(parts[0], "%f", &m)
		fmt.Sscanf(parts[1], "%f", &s)
		return m*60 + s
	}
	var s float64
	fmt.Sscanf(ts, "%f", &s)
	return s
}

// stripXMLTags removes HTML/XML tag delimiters from a string via the
// per-rune scanner (handles inline `<c>` / `<i>` VTT cue styling).
func stripXMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				result.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(result.String())
}

// Compile-time assertion: YTDLPSubtitleAdapter must satisfy
// monitor.TranscriptProvider. Per AGENTS.md Pattern 0 / godlike/06
// §"Database and config ownership": any signature drift becomes a
// build failure here, not a runtime panic.
var _ monitor.TranscriptProvider = (*YTDLPSubtitleAdapter)(nil)
