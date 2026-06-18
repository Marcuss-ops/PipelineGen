package scriptcore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"

	"go.uber.org/zap"
)

// ── Evidence types ──────────────────────────────────────────────────────

// EvidenceChunk is a transcript segment with temporal bounds.
type EvidenceChunk struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// ClipEvidence is the compact representation of a single clip for the LLM.
type ClipEvidence struct {
	ClipID          string          `json:"clip_id"`
	Title           string          `json:"title"`
	YouTubeTitle    string          `json:"youtube_title,omitempty"`
	Summary         string          `json:"summary,omitempty"`
	Description     string          `json:"description,omitempty"`
	DriveLink       string          `json:"drive_link,omitempty"`
	Topics          []string        `json:"topics,omitempty"`
	Speakers        []string        `json:"speakers,omitempty"`
	MentionedPeople []string        `json:"mentioned_people,omitempty"`
	Hook            string          `json:"hook,omitempty"`
	Language        string          `json:"language,omitempty"`
	DurationSec     int             `json:"duration_sec,omitempty"`
	QualityScore    float64         `json:"quality_score,omitempty"`
	TranscriptWords int             `json:"transcript_words"`
	EvidenceChunks  []EvidenceChunk `json:"evidence_chunks,omitempty"`
	Excluded        bool            `json:"excluded,omitempty"`
	ExcludeReason   string          `json:"exclude_reason,omitempty"`
}

// ClipSourcePack is the result of hydrating and validating clip IDs.
type ClipSourcePack struct {
	Clips         []ClipEvidence `json:"clips"`
	ExcludedClips []ClipEvidence `json:"excluded_clips,omitempty"`
	Requested     int            `json:"requested"`
	Accepted      int            `json:"accepted"`
}

// ── Narrative plan types ────────────────────────────────────────────────

// NarrativePlan is the output of the first LLM call (narrative planning).
type NarrativePlan struct {
	Title        string        `json:"title"`
	NarrativeArc string        `json:"narrative_arc"`
	OrderedClips []OrderedClip `json:"ordered_clips"`
	Warnings     []string      `json:"warnings,omitempty"`
}

// OrderedClip is a single clip in the narrative plan.
//
// PR4 enrichment: in addition to the structural role (Position in the arc),
// the planner may now attach a per-clip Purpose, a ComedicAngle (or
// narrative angle for non-comedy types) and a TargetWords budget. The
// writer uses these to lean on per-clip intent instead of guessing.
//
// Old plans written before PR4 omit the new fields; they are kept
// optional (omitempty on Marshal, zero values on Unmarshal) so the
// field is fully backward-compatible.
type OrderedClip struct {
	ClipID       string `json:"clip_id"`
	Role         string `json:"role"`
	Reason       string `json:"reason"`
	Purpose      string `json:"purpose,omitempty"`
	ComedicAngle string `json:"comedic_angle,omitempty"`
	TargetWords  int    `json:"target_words,omitempty"`
}

// ── Section types ───────────────────────────────────────────────────────

// ScriptSection is a section of the generated script grounded in specific clips.
type ScriptSection struct {
	ID       string          `json:"id"`
	Order    int             `json:"order"`
	Text     string          `json:"text"`
	ClipIDs  []string        `json:"clip_ids"`
	Usage    string          `json:"usage"` // "primary" | "secondary"
	Evidence []EvidenceChunk `json:"evidence,omitempty"`
}

// ── ClipScriptResult ────────────────────────────────────────────────────

// ClipScriptResult is the final output of Clip→Script generation.
type ClipScriptResult struct {
	ScriptID          int64           `json:"script_id"`
	Title             string          `json:"title"`
	Script            string          `json:"script"`
	WordCount         int             `json:"word_count"`
	Language          string          `json:"language"`
	SourceFingerprint string          `json:"source_fingerprint"`
	ClipCoverage      ClipCoverage    `json:"clip_coverage"`
	Sections          []ScriptSection `json:"sections"`
	ExcludedClips     []ClipEvidence  `json:"excluded_clips,omitempty"`
	Warnings          []string        `json:"warnings,omitempty"`
	NarrativePlan     *NarrativePlan  `json:"narrative_plan,omitempty"`
}

// ClipCoverage reports how many clips were used vs excluded.
type ClipCoverage struct {
	Requested int `json:"requested"`
	Accepted  int `json:"accepted"`
	Used      int `json:"used"`
	Excluded  int `json:"excluded"`
}

// ── ReporterOptions ─────────────────────────────────────────────────────

// ClipGenerationOptions controls clip hydration, validation, and generation.
type ClipGenerationOptions struct {
	Language           string
	Tone               string
	Title              string
	Model              string
	TargetWords        int
	MaxCharsPerScene   int    // 0 = no limit; caps chars per script paragraph
	SourceText         string // optional: user-provided source text to rewrite with clip evidence
	TranscriptPolicy   string // "auto", "full", "evidence_only", "summary_only"
	OrderingStrategy   string // "auto", "chronological", "thematic"
	MinQualityScore    float64
	MinTranscriptWords int
	MaxClips           int // 0 = use all

	// AllowNoTranscript, quando true, accetta clip anche senza transcript.
	// Utile per curation/search dove il titolo/summary sono sufficienti
	// e il transcript potrebbe non essere stato ancora processato.
	AllowNoTranscript bool

	// StyleInstructions è un prompt opzionale per guidare lo stile della
	// scrittura LLM. Viene iniettato nelle CRITICAL INSTRUCTIONS.
	// Esempio: "Write a comedy script: use setup/punchline structure,
	// avoid calling the subject a 'legend' or 'icon', end with a callback."
	StyleInstructions string

	// Type definisce la STRUTTURA dello script:
	//   "" o "documentary" (default): narrazione fluida con intro/outro
	//   "compilation": presenta ogni clip prima, poi descrive cosa succede
	//   "story": arco narrativo a tre atti
	//   "interview": struttura a domanda/risposta
	Type string
}

// ── ClipSourceBuilder ───────────────────────────────────────────────────

// ClipSourceBuilder orchestrates the Clip→Script pipeline.
type ClipSourceBuilder struct {
	clipsRepo   *clips.Repository
	ollamaCli   *client.Client
	vectorSvc   *vectorstore.Service
	rerankerCli *reranker.Client
	log         *zap.Logger
}

// NewClipSourceBuilder creates a ClipSourceBuilder.
func NewClipSourceBuilder(clipsRepo *clips.Repository, ollamaCli *client.Client, log *zap.Logger) *ClipSourceBuilder {
	return &ClipSourceBuilder{
		clipsRepo: clipsRepo,
		ollamaCli: ollamaCli,
		log:       log,
	}
}

// SetVectorStore sets the optional vector store for semantic search.
func (b *ClipSourceBuilder) SetVectorStore(vs *vectorstore.Service) {
	b.vectorSvc = vs
}

// SetReranker sets the optional CrossEncoder reranker for result reordering.
func (b *ClipSourceBuilder) SetReranker(cli *reranker.Client) {
	b.rerankerCli = cli
}

// BuildPack loads clip IDs, validates them, and builds Evidence cards.
func (b *ClipSourceBuilder) BuildPack(ctx context.Context, clipIDs []string, opts *ClipGenerationOptions) (*ClipSourcePack, error) {
	if len(clipIDs) == 0 {
		return nil, fmt.Errorf("at least one clip ID is required")
	}

	b.log.Info("BuildPack: starting clip hydration",
		zap.Int("requested", len(clipIDs)),
		zap.Int("min_transcript_words", opts.MinTranscriptWords),
		zap.Float64("min_quality", opts.MinQualityScore))

	pack := &ClipSourcePack{
		Requested: len(clipIDs),
	}

	for _, id := range clipIDs {
		asset, err := b.clipsRepo.GetClip(ctx, id)
		if err != nil || asset == nil {
			b.log.Warn("clip not found, excluding", zap.String("clip_id", id), zap.Error(err))
			pack.ExcludedClips = append(pack.ExcludedClips, ClipEvidence{
				ClipID:        id,
				Excluded:      true,
				ExcludeReason: "not_found",
			})
			continue
		}

		evidence := b.buildEvidence(asset, opts)
		if evidence.Excluded {
			b.log.Warn("clip excluded",
				zap.String("clip_id", id),
				zap.String("name", asset.Name),
				zap.String("reason", evidence.ExcludeReason))
			pack.ExcludedClips = append(pack.ExcludedClips, evidence)
			continue
		}

		b.log.Info("clip accepted",
			zap.String("clip_id", id),
			zap.String("name", asset.Name),
			zap.Int("transcript_words", evidence.TranscriptWords),
			zap.Int("evidence_chunks", len(evidence.EvidenceChunks)),
			zap.String("description", fmt.Sprintf("%.80s", evidence.Description)))

		pack.Clips = append(pack.Clips, evidence)
	}

	b.log.Info("BuildPack: done",
		zap.Int("accepted", len(pack.Clips)),
		zap.Int("excluded", len(pack.ExcludedClips)))

	pack.Accepted = len(pack.Clips)
	return pack, nil
}

// ── Pipeline entry point (for job handler) ──────────────────────────────

// BuildClipContext runs the Clip→Script pre-generation pipeline: hydrate clips,
// exclude invalid ones, and produce a narrative plan. Returns the source pack,
// narrative plan, and a source text ready for engine.WriteScript.
//
// The caller SHOULD pass the source text to engine.WriteScript() for the
// actual script generation, so that MemoryGate, NormalizeLength, SaveScript,
// and all post-processing are applied uniformly.
func (b *ClipSourceBuilder) BuildClipContext(ctx context.Context, clipIDs []string, opts *ClipGenerationOptions) (*ClipSourcePack, *NarrativePlan, string, error) {
	if opts == nil {
		opts = &ClipGenerationOptions{
			Language:         "en",
			Tone:             "documentary",
			TranscriptPolicy: "auto",
			OrderingStrategy: "auto",
		}
	}

	b.log.Info("BuildClipContext: start",
		zap.Int("clip_ids", len(clipIDs)),
		zap.String("language", opts.Language),
		zap.String("tone", opts.Tone),
		zap.Int("target_words", opts.TargetWords),
		zap.Int("max_chars_per_scene", opts.MaxCharsPerScene),
		zap.Bool("has_source_text", opts.SourceText != ""),
		zap.Int("source_text_chars", len(opts.SourceText)))

	// Step 1: Hydrate and validate
	pack, err := b.BuildPack(ctx, clipIDs, opts)
	if err != nil {
		return nil, nil, "", err
	}
	if len(pack.Clips) == 0 {
		return nil, nil, "", fmt.Errorf("no valid clips after validation")
	}

	// Step 2: Narrative planning (LLM)
	plan, err := b.PlanNarrative(ctx, pack, opts)
	if err != nil {
		return nil, nil, "", fmt.Errorf("narrative planning failed: %w", err)
	}

	// Step 3: Build source text for engine.WriteScript
	sourceText := b.BuildSourceText(pack, plan, opts)
	b.log.Info("BuildClipContext: source text ready",
		zap.Int("source_chars", len(sourceText)),
		zap.Int("clips_in_plan", len(plan.OrderedClips)))

	return pack, plan, sourceText, nil
}

// Ensure unused imports are referenced
var _ = asset.MediaAsset{}
var _ = time.Now
var _ = strings.TrimSpace
