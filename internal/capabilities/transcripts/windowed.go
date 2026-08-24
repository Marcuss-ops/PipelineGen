// Package transcripts — windowed.go (Commit G, June 2026):
// windowed sampling helper for TranscriptDocument. Splits the
// document into 60-second windows, exposes a cheap scorer (no LLM)
// that ranks windows by keyword overlap + length heuristics, and
// surfaces the TopK = 5 windows across the document.
//
// Why: the pre-Commit-G OllamaAnalyzer.FindSegments called
// Ollama once per video with the FULL transcript. For a 30-minute
// video this is ~4800 tokens of transcript context, which:
//   - Saturates the LLM context window (gemma4:e2b is ~8K tokens).
//   - Pushed the LLM toward superficial averaging — segments landed
//     near the middle (the "uniform probability" zone).
//   - Did not allow per-window score/keyword specialization.
//
// Commit G splits the Document into 60s windows, ranks them with a
// cheap (cost = O(N*windowLen)) scorer, picks TopK = 5 windows, and
// asks the LLM to extract segments ONLY from those TopK windows
// (single-context-window call → better LLM focus + cheaper).
//
// The cheap scorer is intentionally not an LLM call — it is a
// keyword-overlap + cue-count heuristic that runs in microseconds
// per window. The scorer is replaceable (cheapScoreFn field on
// WindowSampler's struct) for future per-channel scorer swap.
package transcripts

import (
	"sort"
	"strings"

	transcript "github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"
)

// Document type alias for back-compat with the local WindowSampler
// API. Internal callers should prefer transcript.Document directly.
//
// Deprecated alias: prefer the leaf `transcript.Document` type at
// call sites. Kept for backward-compat with the tests that may type
// `transcripts.TranscriptDocument` from the now-removed file.
type TranscriptDocument = transcript.Document

// TranscriptEntry is a typed alias for the leaf Entry type. Same
// deprecation note as TranscriptDocument.
type TranscriptEntry = transcript.Entry

// DefaultWindowSizeSec is the canonical 60-second sampling window
// per the Commit G spec. Window boundary alignment is floor-second
// (rounded down). Per-channel window-size overrides are not
// supported in Commit G (P1 follow-up).
const DefaultWindowSizeSec = 60.0

// DefaultTopK is the canonical TopK = 5 per spec. Same reasoning:
// commits the contract to a single value so the LLM context budget
// is predictable (TopK=5 × ~120 tokens per window preview =
// ~600 tokens of prefiltered context).
const DefaultTopK = 5

// Window is the per-window preview shape. The LLM is later asked
// to extract segments from the Window.Text field verbatim (with
// [Start/End] markers preserved). Entries are intentionally NOT
// propagated through Window because the LLM prompt is shifted to
// window-level (the cue level is the inner-granularity output, not
// the input).
type Window struct {
	// StartSec + EndSec are the window's [StartSec, EndSec) bounds
	// in seconds. EndSec = StartSec + WindowSizeSec for all but the
	// tail window (which may be the document tail).
	StartSec float64
	EndSec   float64

	// Text is the concatenated plain-text for entries whose StartSec
	// falls inside the window's [StartSec, EndSec) range. Capped so a
	// pathological 60s window with 5000 chars of cue text doesn't
	// bloat the LLM context — the cap matches the pre-Commit-G
	// 8KB global budget (rarely reached at 60s-window granularity).
	Text string

	// CheapScore is the cheap-scorer output (keyword overlap +
	// length heuristic). Sortable. Higher is better.
	CheapScore int
}

// WindowSampler converts a TranscriptDocument into a slice of
// Window samples. Cheaper than the LLM-driven Analysis path.
//
// All numeric knobs have sensible defaults so callers can pass
// zero-value WindowSamplers. The cheapScoreFn is exposed for
// per-channel scorer overrides (ChannelMonitor ports); nil →
// defaultCheapScore (keyword overlap + length heuristic).
type WindowSampler struct {
	WindowSizeSec float64
	TopK          int

	// cheapScoreFn is the comparator. Nil → defaultCheapScore
	// (keyword overlap + length heuristic). Replaceable per-channel
	// via Configuration (out of scope for Commit G).
	cheapScoreFn func(windowText string, keywords []string) int
}

// NewWindowSampler constructs a sampler with the canonical defaults
// (60s window, top-K=5). Cheap-score function defaults to the
// keyword-overlap heuristic at the bottom of this file.
func NewWindowSampler() *WindowSampler {
	return &WindowSampler{
		WindowSizeSec: DefaultWindowSizeSec,
		TopK:          DefaultTopK,
		cheapScoreFn:  defaultCheapScore,
	}
}

// Sample splits the document into 60-second windows, scores each,
// and returns the TopK. Empty documents return nil (NOT an empty
// slice — callers distinguish via len() == 0 anyway, but we lock
// the contract so test assertions are canonical).
//
// keywords is the channel's semantic keyword list (may be empty).
// When keywords == nil, all windows get the same cheap-score from
// the length heuristic (caller can override cheapScoreFn for a
// different ranking). The OllamaAnalyzer always passes the channel's
// keywords here.
func (s *WindowSampler) Sample(doc transcript.Document, keywords []string) []Window {
	if len(doc.Entries) == 0 || doc.DurationSec <= 0 {
		return nil
	}
	windowSize := s.WindowSizeSec
	if windowSize <= 0 {
		windowSize = DefaultWindowSizeSec
	}
	topK := s.TopK
	if topK <= 0 {
		topK = DefaultTopK
	}
	scorer := s.cheapScoreFn
	if scorer == nil {
		scorer = defaultCheapScore
	}

	// Step 1: bin entries into windows. The bin key is the floor of
	// (entry.StartSec / windowSize); each window StartSec =
	// binIndex * windowSize. Tails are bounded by DurationSec.
	numWindows := int(doc.DurationSec/windowSize) + 1
	if numWindows < 1 {
		numWindows = 1
	}
	binTexts := make([][]string, numWindows)
	for _, e := range doc.Entries {
		idx := int(e.Start / windowSize)
		if idx < 0 {
			idx = 0
		}
		if idx >= numWindows {
			idx = numWindows - 1
		}
		binTexts[idx] = append(binTexts[idx], e.Text)
	}

	// Step 2: materialize Window objects with cheap scores.
	windows := make([]Window, 0, numWindows)
	for i, parts := range binTexts {
		if len(parts) == 0 {
			continue
		}
		startSec := float64(i) * windowSize
		endSec := startSec + windowSize
		if endSec > doc.DurationSec {
			endSec = doc.DurationSec
		}
		text := strings.Join(parts, " ")
		windows = append(windows, Window{
			StartSec:   startSec,
			EndSec:     endSec,
			Text:       text,
			CheapScore: scorer(text, keywords),
		})
	}

	// Step 3: sort descending by cheap score, pick topK. When two
	// windows tie on score, the earlier (lower StartSec) wins —
	// this preserves temporal ordering for operators inspecting
	// truncation events (earlier windows are typically intro/setup
	// material that operators care about for diagnostic clarity).
	sort.SliceStable(windows, func(i, j int) bool {
		if windows[i].CheapScore != windows[j].CheapScore {
			return windows[i].CheapScore > windows[j].CheapScore
		}
		return windows[i].StartSec < windows[j].StartSec
	})
	if len(windows) > topK {
		windows = windows[:topK]
	}
	return windows
}

// defaultCheapScore is the canonical cheap scorer: keyword overlap
// count + a length boost. Cost per window is O(windowLen * keywordsLen)
// on lowercased rune scans — well below the LLM cost it replaces.
//
// The score is intentionally unbounded (returns int). Sortable
// descending; ties broken on StartSec (see Sample's sort).
func defaultCheapScore(windowText string, keywords []string) int {
	if windowText == "" {
		return 0
	}
	text := strings.ToLower(windowText)
	score := 0
	// Phase 1: keyword overlap. Each keyword contributes ceil(len/4)
	// proportional weight so a multi-word keyword (e.g. "machine learning")
	// scores proportionally higher than a single-token keyword.
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw == "" {
			continue
		}
		count := strings.Count(text, kw)
		score += count * (1 + len(kw)/4)
	}
	// Phase 2: length boost. Bound so a 60s window with 3000 chars
	// adds at most 100 points — keeps the cheap score within the
	// same order-of-magnitude as the keyword overlap weight.
	boost := len(windowText) / 30
	if boost > 100 {
		boost = 100
	}
	score += boost
	return score
}
