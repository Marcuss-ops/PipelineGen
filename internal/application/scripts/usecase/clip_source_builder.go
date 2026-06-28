// Package scripts — clip_source_builder.go replaces the ClipSourceBuilder
// stub with a real implementation that fetches clips from the repository
// and builds context from their metadata + transcripts.
package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// ── Narrative plan / section (companions of ClipSourceBuilder) ────────────

// NarrativeSection describes a single section within a NarrativePlan.
type NarrativeSection struct {
	Role       string
	Purpose    string
	WordBudget int
}

// NarrativePlan is the per-item plan constructed by ClipSourceBuilder
// while resolving clip context.
type NarrativePlan struct {
	Title      string
	Style      string
	TotalWords int
	Sections   []NarrativeSection
}

// ── ClipSourceBuilder (unchanged from prior version) ─────────────────────

// clipsResolverPort is the narrow resolver interface that
// ClipSourceBuilder consumes. *assets.ClipsRepository satisfies it
// in production; unit tests inject a hand-rolled stub via
// NewClipSourceBuilderForTest. Defining the port here (rather than
// in `ports/`) keeps the package self-contained: the builder
// has no other port dependency and tests don't need a new import
// just to wire a stub.
type clipsResolverPort interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	GetByDriveFileID(ctx context.Context, id string) (*asset.Asset, error)
}

type ClipSourceBuilder struct {
	clipsRepo    clipsResolverPort
	ollamaClient interface{} // *client.Client
	reranker     interface{}
	log          *zap.Logger
}

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

// NewClipSourceBuilderForTest is the test-only constructor that
// accepts a clipsResolverPort stub rather than the concrete
// *assets.ClipsRepository. The canonical NewClipSourceBuilder
// preserves its production signature so the composition root
// (internal/app/wire_script.go) keeps wiring the concrete repo
// unchanged; tests reach for this constructor to inject fakes
// without dragging SQLite + asset fixtures into the unit-test
// boundary.
func NewClipSourceBuilderForTest(
	clipsRepo clipsResolverPort,
	log *zap.Logger,
) *ClipSourceBuilder {
	return &ClipSourceBuilder{
		clipsRepo: clipsRepo,
		log:       log,
	}
}

func (c *ClipSourceBuilder) SetReranker(r interface{}) { c.reranker = r }

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

	clipsRepo := c.clipsRepo
	if clipsRepo == nil {
		return nil, nil, "", fmt.Errorf("clip source builder: clips repository not configured")
	}

	clips := make([]*asset.Asset, 0, len(uniqueIDs))
	clipNames := make([]string, 0, len(uniqueIDs))
	canonicalIDs := make([]string, 0, len(uniqueIDs))
	var missingClipIDs []scriptpkg.MissingClipID
	clipToCanonical := make(map[string]string, len(uniqueIDs))
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
				missingClipIDs = append(missingClipIDs, scriptpkg.MissingClipID{
					ClipID: id,
					Reason: scriptpkg.MissingClipReasonNotFound,
				})
				continue
			}
		}
		if clip == nil {
			missingClipIDs = append(missingClipIDs, scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonNotFound,
			})
			continue
		}
		clips = append(clips, clip)
		canonicalIDs = append(canonicalIDs, id)
		clipToCanonical[clip.ID] = id
		name := strings.TrimSpace(clip.Name)
		if name == "" {
			name = strings.TrimSpace(clip.Filename)
		}
		if name == "" {
			name = id
		}
		clipNames = append(clipNames, name)

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

	clipDriveLinks := make(map[string]string, len(clips))
	for _, clip := range clips {
		if link := clip.DriveLink(); link != "" {
			canonicalID := clipToCanonical[clip.ID]
			if canonicalID == "" {
				canonicalID = clip.ID
			}
			clipDriveLinks[canonicalID] = link
		}
	}
	// PR 6: when clips resolved but ALL lack drive links, fail
	// with a typed error so the caller can surface drivenotfound.
	if len(clipDriveLinks) == 0 && len(clips) > 0 {
		return nil, nil, "", fmt.Errorf("clip source builder: all resolved clips lack drive links")
	}

	// Build pack with clip data.
	pack := map[string]any{
		"clip_ids":         canonicalIDs,
		"clip_names":       clipNames,
		"clip_drive_links": clipDriveLinks,
		"missing_clip_ids": missingClipIDs,
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

func NewFingerprintContext(model, promptModel string) interface{} {
	return map[string]string{
		"model":        strings.TrimSpace(model),
		"prompt_model": strings.TrimSpace(promptModel),
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
