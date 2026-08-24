// Package youtube — ollama_clip_metadata_builder.go: concrete
// ClipMetadataBuilder over the canonical Ollama client.
//
// PR-C-YouTube-Cutover Commit 4/6 (June 2026, P1 #15): the
// previous MetadataService.GenerateClipMetadata was a stub
// returning nil. The verdict mandates a real Ollama-driven
// builder with a DETERMINISTIC FALLBACK (NOT the legacy
// `quality_score = 0.5` default) that derives the score from
// (clip_duration, transcript_word_count, semantic_coverage).
//
// The deterministic fallback is the **primary contract**:
// Ollama is best-effort, and any unavailability / timeout /
// invalid-JSON path falls through to the formula. The formula
// is identical to the one in
// internal/capabilities/youtube/metadata/service.go::calculateQualityScore
// so the production and fallback score ranges are
// indistinguishable.
//
// Why a separate package (infrastructure/youtube, not
// application/youtube/metadata): the Ollama client is an
// infrastructure concern — it owns the network call, the
// retry policy, the prompt template, and the response
// parser. The application-layer port (metadata.ClipMetadataBuilder)
// only sees the typed envelope.
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// Compile-time conformance assertion (AGENTS.md Pattern 0).
var _ metadata.ClipMetadataBuilder = (*OllamaClipMetadataBuilder)(nil)

// OllamaClipMetadataBuilder is the concrete builder. It owns
// the Ollama client handle, the model name, the prompt
// template, and the retry policy. The deterministic fallback
// is delegated to the package-level helper so the formula
// stays in one place (tested independently of the network).
type OllamaClipMetadataBuilder struct {
	client   *ollamaclient.Client
	model    string
	timeout  time.Duration
	log      *zap.Logger
	now      func() time.Time // injectable clock for tests
	isOllama func(ctx context.Context) bool
}

// NewOllamaClipMetadataBuilder constructs the concrete builder.
// model may be empty (the builder falls back to the client's
// default model). timeout is the per-attempt Ollama deadline
// (default 60s when zero). isOllama is an optional health probe
// (e.g. HTTP HEAD on the Ollama URL); when nil the builder
// always attempts the call and lets the retry.Do seam classify
// the outcome.
func NewOllamaClipMetadataBuilder(
	client *ollamaclient.Client,
	model string,
	timeout time.Duration,
	log *zap.Logger,
) *OllamaClipMetadataBuilder {
	if log == nil {
		log = zap.NewNop()
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OllamaClipMetadataBuilder{
		client:   client,
		model:    model,
		timeout:  timeout,
		log:      log,
		now:      time.Now,
		isOllama: nil,
	}
}

// Build produces the typed metadata envelope. The flow:
//
//  1. If the client is nil or the health probe (isOllama)
//     returns false → fallback envelope (deterministic).
//  2. Else attempt the Ollama call (retry.Do with
//     isTransientExtractionError-style predicate for
//     network blips). On a non-retryable error (e.g. model
//     not found, schema mismatch) → fallback envelope.
//  3. Parse the JSON response. On unmarshal failure →
//     fallback envelope (we do not crash a clip write
//     because the LLM produced malformed JSON).
//  4. Populate QualityScore, SponsorSegment, SourceVersion
//     from the parsed response + the typed input.
//
// The fallback envelope is the canonical 100% deterministic
// path — see fallbackMetadata in metadata/service.go for
// the formula. The Ollama path only differs in the
// Summary / Topics / Speakers / MentionedPeople fields;
// QualityScore still goes through the formula so the two
// paths produce a comparable score (the user's "real
// formula" requirement).
func (b *OllamaClipMetadataBuilder) Build(
	ctx context.Context,
	in youtubetypes.ClipMetadataInput,
) (youtubetypes.CanonicalClipMetadata, error) {
	if in.ClipID == "" {
		return youtubetypes.CanonicalClipMetadata{}, fmt.Errorf("OllamaClipMetadataBuilder.Build: ClipID is required")
	}

	if b.client == nil {
		b.log.Warn("OllamaClipMetadataBuilder.Build: client is nil; using deterministic fallback",
			zap.String("clip_id", in.ClipID))
		return metadataFallback(in), nil
	}
	if b.isOllama != nil && !b.isOllama(ctx) {
		b.log.Warn("OllamaClipMetadataBuilder.Build: health probe false; using deterministic fallback",
			zap.String("clip_id", in.ClipID))
		return metadataFallback(in), nil
	}

	model := b.model
	if model == "" {
		model = b.client.Model()
	}
	prompt := b.renderPrompt(in)

	var response string
	_, err := retry.DoWithValue(ctx, func() (struct{}, error) {
		if b.timeout > 0 {
			callCtx, cancel := context.WithTimeout(ctx, b.timeout)
			defer cancel()
			out, callErr := b.client.SimpleGenerate(callCtx, model, prompt, b.timeout, nil)
			if callErr != nil {
				return struct{}{}, callErr
			}
			response = out
			return struct{}{}, nil
		}
		out, callErr := b.client.SimpleGenerate(ctx, model, prompt, 0, nil)
		if callErr != nil {
			return struct{}{}, callErr
		}
		response = out
		return struct{}{}, nil
	}, retry.Options{
		MaxAttempts:    2,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     5 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0.2,
		IsRetryable:    retry.IsTransient,
	})
	if err != nil {
		b.log.Warn("OllamaClipMetadataBuilder.Build: ollama call failed; using deterministic fallback",
			zap.String("clip_id", in.ClipID),
			zap.Error(err))
		return metadataFallback(in), nil
	}

	parsed, parseErr := b.parseResponse(response)
	if parseErr != nil {
		b.log.Warn("OllamaClipMetadataBuilder.Build: response parse failed; using deterministic fallback",
			zap.String("clip_id", in.ClipID),
			zap.Error(parseErr))
		return metadataFallback(in), nil
	}

	return b.composeMetadata(in, parsed), nil
}

// composeMetadata merges the parsed Ollama response with the
// typed input. QualityScore and SponsorSegment still go
// through the deterministic formula (per the verdict — the
// LLM's self-reported score is a hint, not a final value),
// so the production + fallback score ranges stay
// comparable.
//
// All helpers (word count, formula, sponsor regex, source
// hash) now come from the metadata package — there is ONE
// canonical algorithm, two callers (Compose path + Fallback
// path). The post-Commit-4 review's "duplicate copy in the
// infra package" finding is now closed.
func (b *OllamaClipMetadataBuilder) composeMetadata(
	in youtubetypes.ClipMetadataInput,
	parsed ollamaMetadataResponse,
) youtubetypes.CanonicalClipMetadata {
	transcriptWordCount := metadata.CountWords(in.Transcript)
	score := metadata.CalculateQualityScore(
		transcriptWordCount,
		in.ClipDuration,
		len(parsed.Topics),
		len(parsed.Speakers),
		len(parsed.MentionedPeople),
	)
	summary := parsed.ClipSummary
	if summary == "" {
		summary = in.Title
		if len(summary) > 240 {
			summary = summary[:240]
		}
	}
	transcriptPath := parsed.TranscriptPath
	if transcriptPath == "" {
		// Use the conventional layout: <local_path>.txt
		// adjacent to the clip. The writer doesn't read
		// the file; it just records the path so the
		// re-indexer can pick it up.
		transcriptPath = transcriptPathFor(in.ClipID)
	}
	sponsor := parsed.SponsorSegment || metadata.IsSponsorSegment(in.Transcript)
	if sponsor {
		// Apply the canonical sponsor penalty on top of the
		// formula-derived score. The formula already
		// computes a non-penalised value; we subtract
		// here so the penalty is applied regardless of
		// whether the LLM flagged the segment.
		score -= youtubetypes.QualityScoreSponsorPenalty
		if score < 0 {
			score = 0
		}
	}
	group := in.Group
	if group == "" {
		group = "general"
	}
	return youtubetypes.CanonicalClipMetadata{
		ClipID:          in.ClipID,
		AssetID:         in.ClipID,
		Summary:         summary,
		Topics:          dedupeStrings(parsed.Topics),
		Speakers:        dedupeStrings(parsed.Speakers),
		MentionedPeople: dedupeStrings(parsed.MentionedPeople),
		QualityScore:    score,
		SponsorSegment:  sponsor,
		TranscriptPath:  transcriptPath,
		SourceURL:       in.SourceURL,
		NormalizedGroup: group,
		SourceVersion:   deriveBuilderSourceVersion(in.ClipID, parsed, score),
		JobID:           "",
		// Upstream signal from cmd.Segment. The Ollama-derived
		// Topics/Speakers/MentionedPeople win when non-empty
		// (post-parse deduplication); the cmd.Segment fields
		// (passed in via the buildClipAsset / Step 10 wiring)
		// preserve Hook + SearchVisibility verbatim — these
		// are meta-fields the LLM is NOT asked to generate
		// (they are operator-controlled metadata). Writer
		// persists them via metadata_json.hook + .search_visibility.
		Hook:             in.Hook,
		SearchVisibility: in.SearchVisibility,
	}
}

// renderPrompt builds the canonical prompt. Mirrors the
// legacy adapters/metadata_service_helpers.go prompt so the
// trained model behaviour is preserved across the cutover.
func (b *OllamaClipMetadataBuilder) renderPrompt(in youtubetypes.ClipMetadataInput) string {
	transcript := in.Transcript
	if len(transcript) > 3000 {
		transcript = transcript[:3000]
	}
	return fmt.Sprintf(`You are an assistant that generates rich metadata for a YouTube clip.
Analyze only the clip transcript below. Do not invent events from the description.
Use the title only as lightweight context for names/entities.

Title: %s
Transcript: %s

Return only JSON with these fields:
{
  "clip_summary": "2-3 sentence summary of the actual clip",
  "topics": ["concept 1", "concept 2"],
  "speakers": ["primary speaker", "host"],
  "mentioned_people": ["person mentioned", "another person"],
  "sponsor_segment": false,
  "transcript_path": ""
}

Rules:
- clip_summary must be faithful to the transcript only
- topics must be concepts or themes, not filler words
- speakers are the people actually speaking in the clip when inferable
- mentioned_people are people named in the clip, distinct from speakers
- sponsor_segment should be true when the transcript contains a sponsorship read
- transcript_path is empty by default
- Return ONLY the JSON object, no explanation`, in.Title, transcript)
}

// ollamaMetadataResponse is the JSON shape we expect back
// from Ollama. Deliberately a subset of the legacy
// ClipRichMetadata — the typed envelope produced by the
// builder is youtubetypes.CanonicalClipMetadata, NOT the
// legacy ClipRichMetadata.
type ollamaMetadataResponse struct {
	ClipSummary     string   `json:"clip_summary"`
	Topics          []string `json:"topics"`
	Speakers        []string `json:"speakers"`
	MentionedPeople []string `json:"mentioned_people"`
	SponsorSegment  bool     `json:"sponsor_segment"`
	TranscriptPath  string   `json:"transcript_path"`
}

// parseResponse extracts the first JSON object from the
// response (Ollama sometimes wraps the JSON in markdown
// fences) and unmarshals it. Returns an error when the
// response is empty, contains no JSON, or fails to parse.
func (b *OllamaClipMetadataBuilder) parseResponse(response string) (ollamaMetadataResponse, error) {
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return ollamaMetadataResponse{}, fmt.Errorf("empty response")
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 || end <= start {
		return ollamaMetadataResponse{}, fmt.Errorf("no JSON object in response")
	}
	body := trimmed[start : end+1]
	var out ollamaMetadataResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return ollamaMetadataResponse{}, fmt.Errorf("unmarshal: %w", err)
	}
	return out, nil
}

// transcriptPathFor returns the conventional transcript
// path layout. The path is not validated — the writer
// records it verbatim and the re-indexer reads the file
// if it exists. Empty when the clipID is empty.
func transcriptPathFor(clipID string) string {
	if clipID == "" {
		return ""
	}
	return clipID + ".txt"
}

// deriveBuilderSourceVersion hashes the clipID + summary +
// topics + score into a deterministic fingerprint. Uses
// the metadata package's exported Sha256Short so the
// fallback path (metadata.DeriveFallbackSourceVersion) and
// the Ollama path produce comparable 16-hex-char
// fingerprints from the same helper.
func deriveBuilderSourceVersion(clipID string, parsed ollamaMetadataResponse, score float64) string {
	if clipID == "" {
		return ""
	}
	payload := clipID + "|" + parsed.ClipSummary + "|" + strings.Join(parsed.Topics, ",") + "|" + fmt.Sprintf("%.4f", score)
	return metadata.Sha256Short(payload)
}

// dedupeStrings removes duplicates while preserving order.
// Empty strings are dropped.
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ── Deterministic fallback (delegated to metadata package) ─────

// metadataFallback is the deterministic envelope produced
// when Ollama is unavailable / errors. Delegates to the
// metadata package's exported FallbackMetadata helper so
// there is ONE canonical algorithm (consumed by both the
// compose path and the fallback path). Closes the
// post-Commit-4 review's "duplicate copy in the infra
// package" finding.
func metadataFallback(in youtubetypes.ClipMetadataInput) youtubetypes.CanonicalClipMetadata {
	out := metadata.FallbackMetadata(in)
	// Apply the sponsor penalty here so the LLM-orchestrated
	// path (composeMetadata) and the fallback path remain
	// in lockstep. The metadata package's FallbackMetadata
	// does NOT apply the penalty itself (the verdict's
	// sponsor_segment is the regex-detection source of
	// truth; the penalty is the caller's responsibility).
	if out.SponsorSegment {
		out.QualityScore -= youtubetypes.QualityScoreSponsorPenalty
		if out.QualityScore < 0 {
			out.QualityScore = 0
		}
	}
	// Stamp a transcript path so the indexing worker can
	// find the .txt produced by Step 6/7 of the pipeline.
	if out.TranscriptPath == "" {
		out.TranscriptPath = transcriptPathFor(out.ClipID)
	}
	return out
}
