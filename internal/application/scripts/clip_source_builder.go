// Package scripts — clip_source_builder.go replaces the ClipSourceBuilder
// stub with a real implementation that fetches clips from the repository
// and builds context from their metadata + transcripts.
//
// AGENT-3 (June 2026): the previous stub returned hardcoded fake data.
// The real implementation fetches clips by ID from the ClipsRepository,
// builds a source text from their names, search text, and transcripts,
// and computes a deterministic fingerprint for cache key derivation.
//
// PG-029 (June 2026): ClipSourceBuilder struct + ClipGenerationOptions
// consolidated here from the now-deleted types.go.
package scripts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// ClipSourceBuilder builds clip context from explicit clip IDs
// using the clips repository and optional reranker.
// All fields are concrete typed where possible.
//
// PG-034 (June 2026): vectorStore field + SetVectorStore removed — the
// Qdrant capability was deleted.
type ClipSourceBuilder struct {
	clipsRepo    interface{} // *assets.ClipsRepository
	ollamaClient interface{} // *client.Client
	reranker     interface{}
	log          *zap.Logger
}

// ClipGenerationOptions carries options for clip generation.
type ClipGenerationOptions struct {
	Language           string
	Tone               string
	Style              string
	Title              string
	Model              string
	TargetWords        int
	NumClips           int
	SegmentWords       int
	SegmentTopics      []string
	SourceText         string
	TranscriptPolicy   string
	OrderingStrategy   string
	StyleInstructions  string
	MinQualityScore    float64
	MinTranscriptWords int
}

// NewClipSourceBuilder constructs a real ClipSourceBuilder backed by
// the clips repository. Accepts concrete typed args.
//
// clipsRepo is the canonical *assets.ClipsRepository; may be nil
// (BuildClipContext returns an error when nil and clip IDs are provided).
func NewClipSourceBuilder(
	clipsRepo *assets.ClipsRepository,
	ollamaClient interface{},
	log *zap.Logger,
) *ClipSourceBuilder {
	return &ClipSourceBuilder{
		clipsRepo:    clipsRepo,
		ollamaClient: ollamaClient,
		log:          log,
	}
}

// SetReranker attaches a reranker client for future search reranking.
func (c *ClipSourceBuilder) SetReranker(r interface{}) { c.reranker = r }

// BuildClipContext fetches clips by ID from the repository and builds
// a context pack, narrative plan, and source text for the script engine.
//
// Returns:
//   - pack: a map[string]any with clip data (IDs, names, transcripts, metadata)
//   - plan: a NarrativePlan derived from the clip titles
//   - sourceText: a concatenated string of clip names + search text + transcript excerpts
func (c *ClipSourceBuilder) BuildClipContext(
	ctx context.Context,
	clipIDs []string,
	opts *ClipGenerationOptions,
) (interface{}, *NarrativePlan, string, error) {
	if c == nil {
		return nil, nil, "", fmt.Errorf("clip source builder: not constructed")
	}
	if c.clipsRepo == nil {
		return nil, nil, "", fmt.Errorf("clip source builder: clips repository not configured")
	}

	// Deduplicate and trim.
	seen := make(map[string]struct{}, len(clipIDs))
	uniqueIDs := make([]string, 0, len(clipIDs))
	for _, id := range clipIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	if len(uniqueIDs) == 0 {
		return nil, nil, "", fmt.Errorf("clip source builder: no valid clip IDs provided")
	}

	// Fetch clips from repository.
	clipsRepo, ok := c.clipsRepo.(*assets.ClipsRepository)
	if !ok || clipsRepo == nil {
		return nil, nil, "", fmt.Errorf("clip source builder: clipsRepo is not a *assets.ClipsRepository")
	}

	clips := make([]*asset.Asset, 0, len(uniqueIDs))
	clipNames := make([]string, 0, len(uniqueIDs))
	var sourceTextBuilder strings.Builder

	for _, id := range uniqueIDs {
		clip, err := clipsRepo.GetClip(ctx, id)
		if err != nil {
			if c.log != nil {
				c.log.Warn("clip source builder: failed to fetch clip",
					zap.String("clip_id", id),
					zap.Error(err))
			}
		}
		if clip == nil {
			clip, err = clipsRepo.GetByDriveFileID(ctx, id)
			if err != nil {
				if c.log != nil {
					c.log.Warn("clip source builder: failed to fetch clip by drive file id",
						zap.String("clip_id", id),
						zap.Error(err))
				}
				continue
			}
		}
		if clip == nil {
			continue
		}
		clips = append(clips, clip)
		name := strings.TrimSpace(clip.Name)
		if name == "" {
			name = strings.TrimSpace(clip.Filename)
		}
		if name == "" {
			name = id
		}
		clipNames = append(clipNames, name)

		// Build source text from clip metadata.
		sourceTextBuilder.WriteString(fmt.Sprintf("CLIP %s: %s\n", id, name))
		if searchText := strings.TrimSpace(clip.SearchText); searchText != "" {
			sourceTextBuilder.WriteString(fmt.Sprintf("  Description: %s\n", searchText))
		} else if desc := strings.TrimSpace(clip.GetMetadataString("description")); desc != "" {
			sourceTextBuilder.WriteString(fmt.Sprintf("  Description: %s\n", desc))
		}
		transcript := clip.GetMetadataString("transcript")
		if transcript == "" {
			transcript = clip.GetMetadataString("clean_transcript")
		}
		if transcript != "" {
			excerpt := transcript
			if len(excerpt) > 500 {
				excerpt = excerpt[:500] + "..."
			}
			sourceTextBuilder.WriteString(fmt.Sprintf("  Transcript: %s\n", excerpt))
		}
		if len(clip.Tags) > 0 {
			sourceTextBuilder.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(clip.Tags, ", ")))
		}
		sourceTextBuilder.WriteString("\n")
	}

	if len(clips) == 0 {
		return nil, nil, "", fmt.Errorf("clip source builder: no clips found for the provided IDs")
	}

	// Build narrative plan from clip titles.
	title := "script"
	language := ""
	tone := ""
	targetWords := 0
	model := ""
	if opts != nil {
		if v := strings.TrimSpace(opts.Title); v != "" {
			title = v
		}
		language = strings.TrimSpace(opts.Language)
		tone = strings.TrimSpace(opts.Tone)
		targetWords = opts.TargetWords
		model = strings.TrimSpace(opts.Model)
	}

	sectionCount := len(clipNames)
	if opts != nil {
		if opts.NumClips > 0 && opts.NumClips < sectionCount {
			sectionCount = opts.NumClips
		}
		if sectionCount == 0 && len(opts.SegmentTopics) > 0 {
			sectionCount = len(opts.SegmentTopics)
		}
	}
	sections := make([]NarrativeSection, 0, sectionCount)
	for i := 0; i < sectionCount; i++ {
		name := clipNames[i]
		purpose := fmt.Sprintf("Cover content from clip: %s", name)
		if opts != nil && i < len(opts.SegmentTopics) && strings.TrimSpace(opts.SegmentTopics[i]) != "" {
			purpose = fmt.Sprintf("Cover segment topic: %s", strings.TrimSpace(opts.SegmentTopics[i]))
		}
		sections = append(sections, NarrativeSection{
			Role:       fmt.Sprintf("section_%d", i+1),
			Purpose:    purpose,
			WordBudget: targetWords / maxInt(sectionCount, 1),
		})
	}

	plan := &NarrativePlan{
		Title:      title,
		Sections:   sections,
		TotalWords: targetWords,
		Style:      tone,
	}

	// Build drive links map.
	clipDriveLinks := make(map[string]string, len(clips))
	for _, clip := range clips {
		if link := clip.DriveLink(); link != "" {
			clipDriveLinks[clip.ID] = link
		}
	}

	if opts != nil && opts.NumClips > 0 {
		sourceTextBuilder.WriteString(fmt.Sprintf("Requested clip count: %d\n", opts.NumClips))
	}
	if opts != nil && opts.SegmentWords > 0 {
		sourceTextBuilder.WriteString(fmt.Sprintf("Segment words: %d\n", opts.SegmentWords))
	}
	if opts != nil && len(opts.SegmentTopics) > 0 {
		sourceTextBuilder.WriteString("Segment topics:\n")
		for i, topic := range opts.SegmentTopics {
			topic = strings.TrimSpace(topic)
			if topic == "" {
				continue
			}
			sourceTextBuilder.WriteString(fmt.Sprintf("  %d. %s\n", i+1, topic))
		}
	}

	// Build pack with clip data.
	pack := map[string]any{
		"clip_ids":         uniqueIDs,
		"clip_names":       clipNames,
		"clip_drive_links": clipDriveLinks,
		"num_clips": func() int {
			if opts != nil && opts.NumClips > 0 {
				return opts.NumClips
			}
			return 0
		}(),
		"segment_words": func() int {
			if opts != nil {
				return opts.SegmentWords
			}
			return 0
		}(),
		"segment_topics": func() []string {
			if opts != nil {
				return append([]string(nil), opts.SegmentTopics...)
			}
			return nil
		}(),
		"title":      title,
		"language":   language,
		"tone":       tone,
		"model":      model,
		"clip_count": len(clips),
	}

	if c.log != nil {
		c.log.Info("clip source builder: context built",
			zap.Int("clip_ids", len(uniqueIDs)),
			zap.Int("clips_found", len(clips)),
			zap.Int("source_text_chars", sourceTextBuilder.Len()))
	}

	return pack, plan, sourceTextBuilder.String(), nil
}

// ComputeFingerprint builds a deterministic fingerprint string from clip
// IDs and generation options. Used as a cache key for the memory gate.
func (c *ClipSourceBuilder) ComputeFingerprint(
	clipIDs []string,
	pack interface{},
	opts *ClipGenerationOptions,
	fpCtx interface{},
) string {
	parts := []string{"clips"}
	for _, id := range clipIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			parts = append(parts, id)
		}
	}
	if opts != nil {
		if v := strings.TrimSpace(opts.Title); v != "" {
			parts = append(parts, "title="+v)
		}
		if v := strings.TrimSpace(opts.Language); v != "" {
			parts = append(parts, "lang="+v)
		}
		if v := strings.TrimSpace(opts.Tone); v != "" {
			parts = append(parts, "tone="+v)
		}
		if v := strings.TrimSpace(opts.Model); v != "" {
			parts = append(parts, "model="+v)
		}
		if v := strings.TrimSpace(opts.TranscriptPolicy); v != "" {
			parts = append(parts, "transcript="+v)
		}
		if v := strings.TrimSpace(opts.OrderingStrategy); v != "" {
			parts = append(parts, "order="+v)
		}
	}
	return strings.Join(parts, "|")
}

// NewFingerprintContext creates a fingerprint context for cache key derivation.
func NewFingerprintContext(model, promptModel string) interface{} {
	return map[string]string{
		"model":        strings.TrimSpace(model),
		"prompt_model": strings.TrimSpace(promptModel),
	}
}

// maxInt returns the maximum of two ints.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
