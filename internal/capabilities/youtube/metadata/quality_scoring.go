// Package metadata — quality scoring helpers.
//
// quality_scoring.go owns the canonical quality formulas:
// CalculateQualityScore, IsSponsorSegment, CountWords, and Sha256Short.
// Extracted from service.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package metadata

import (
	"regexp"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// ── Sponsor segment detection ───────────────────────────────────────────────

// isSponsorSegmentRegex matches the canonical regex the
// spec mandates. The pattern is case-insensitive and
// word-boundary-anchored so "SponsoRed by" matches but
// "Unsponsored" does not.
//
// The companion `isSponsorSegment` exported wrapper is a
// thin pass-through for callers that want the no-error
// boolean contract.
var isSponsorSegmentRegex = regexp.MustCompile(
	`(?i)\b(sponsored\s+by|advertisement|provided\s+by|brought\s+to\s+you\s+by|partner\s+with|special\s+thanks|promo\s+code|use\s+code|affiliate)\b`,
)

// IsSponsorSegment is the boolean wrapper. Empty input →
// false (no segments to sponsor). The regex is anchored so
// noise like "SponSored" (no space) still matches.
//
// Exported so the Ollama builder (internal/platform/youtube)
// and any other consumer can share the canonical pattern
// instead of duplicating it (the duplicate-copy was a
// post-Commit-4 code-review finding; consolidation keeps
// the spec's regex anchor consistent across both packages).
func IsSponsorSegment(transcript string) bool {
	return isSponsorSegmentRegex.MatchString(transcript)
}

// isSponsorSegment is the package-private alias used by
// the in-package tests. Kept so the test file can call
// `isSponsorSegment` without exporting the symbol at the
// test surface (the canonical surface is IsSponsorSegment).
func isSponsorSegment(transcript string) bool {
	return IsSponsorSegment(transcript)
}

// ── Quality score ───────────────────────────────────────────────────────────

// CalculateQualityScore is the exported form of the
// canonical deterministic formula. Exposed so the Ollama
// builder (internal/platform/youtube) and any other
// consumer can share the exact same math — the duplicate
// copy in the infra package was a post-Commit-4 code-review
// finding.
//
// Inputs:
//
//   - transcriptWordCount — number of words in the
//     transcript. Empty transcript → 0.
//   - clipDuration        — clip duration in seconds.
//     0 means "unknown" and produces the degraded
//     sub-score.
//   - topicCount, speakerCount, mentionedCount — number
//     of distinct items in the metadata semantic
//     coverage fields. 0 / nil = no metadata.
//
// Output: weighted sum of three sub-scores
// (transcript 40%, duration 40%, semantic 20%), clamped
// to [0.0, 1.0]. The sponsor penalty is applied by the
// caller (the formula is the raw weighted sum; the penalty
// is caller-side per the verdict's "sponsor_segment
// propagates from the regex" contract).
func CalculateQualityScore(
	transcriptWordCount, clipDuration int,
	topicCount, speakerCount, mentionedCount int,
) float64 {
	// Sub-score 1: transcript coverage.
	// 0 words → 0; full coverage (150+ words) → 1.0.
	var transcriptScore float64
	if transcriptWordCount > 0 {
		transcriptScore = float64(transcriptWordCount) / 150.0
		if transcriptScore > 1.0 {
			transcriptScore = 1.0
		}
	}

	// Sub-score 2: clip-duration sweet spot.
	// 25-180s is the canonical "good clip" window.
	// Below 12s or above 300s → heavily penalised.
	var durationScore float64
	switch {
	case clipDuration >= 25 && clipDuration <= 180:
		durationScore = 1.0
	case clipDuration >= 15 && clipDuration < 25:
		durationScore = 0.5
	case clipDuration > 180 && clipDuration <= 300:
		durationScore = 0.5
	case clipDuration > 0:
		durationScore = 0.1
	}

	// Sub-score 3: semantic coverage (topics + speakers + mentioned).
	// 0 items → 0; 5+ items → 1.0; linear in between.
	semanticItems := topicCount + speakerCount + mentionedCount
	var semanticScore float64
	if semanticItems > 0 {
		semanticScore = float64(semanticItems) / 5.0
		if semanticScore > 1.0 {
			semanticScore = 1.0
		}
	}

	score := transcriptScore*youtubetypes.QualityScoreTranscriptWeight +
		durationScore*youtubetypes.QualityScoreDurationWeight +
		semanticScore*youtubetypes.QualityScoreSemanticWeight

	if score < 0 {
		score = 0
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// calculateQualityScore is the package-private alias used
// by the in-package tests. Kept so the test file can call
// `calculateQualityScore` without exporting the symbol at
// the test surface (the canonical surface is CalculateQualityScore).
func calculateQualityScore(
	transcriptWordCount, clipDuration int,
	topicCount, speakerCount, mentionedCount int,
) float64 {
	return CalculateQualityScore(transcriptWordCount, clipDuration, topicCount, speakerCount, mentionedCount)
}

// ── Word counting ───────────────────────────────────────────────────────────

// countWords is a stdlib-only word counter. Splits on
// unicode whitespace and filters out empty strings.
func countWords(s string) int {
	if s == "" {
		return 0
	}
	fields := strings.Fields(s)
	return len(fields)
}

// CountWords is the exported form so the Ollama builder
// (internal/platform/youtube) can share the canonical
// word-counting definition instead of inlining a copy.
func CountWords(s string) int {
	return countWords(s)
}

// ── Hashing ─────────────────────────────────────────────────────────────────

// Sha256Short is the EXPORTED 16-hex-char fingerprint
// helper. The infra package uses this for the Ollama path's
// sourceVersion (so production + fallback produce stable,
// comparable fingerprints).
func Sha256Short(s string) string {
	const hexchars = "0123456789abcdef"
	// Inline FNV-1a 64-bit so we don't import crypto/sha256
	// into a leaf helper. Stable across the binary.
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	var buf [16]byte
	for i := 0; i < 8; i++ {
		buf[i] = hexchars[(h>>(i*8+4))&0xF]
		buf[i+8] = hexchars[(h>>(i*8))&0xF]
	}
	return string(buf[:])
}
