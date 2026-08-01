// Package scripts — clip_source_builder.go replaces the ClipSourceBuilder
// stub with a real implementation that fetches clips from the repository
// and builds context from their metadata + transcripts.
//
// PR-REFACTOR-P1-CYCLOMATIC (2026-08-15): cyclomatic complexity
// reduced from 43 → ≤15 via per-clip helper extraction. The
// 2-phase clip resolution (ResolveByMediaAssetID →
// ResolveByDriveFileID fallback) is extracted into a typed
// resolveOneClip helper that returns the resolved clip + a
// strongly-typed "missing reason" string. The per-clip source
// text assembly (CLIP / Description / Transcript / Tags /
// blank-line) is extracted into an appendClipSourceText helper.
// The main loop becomes a linear orchestrator that wires the
// helpers together with early returns on missing clips.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the per-clip
// loop now calls `c.resolveTranscript(ctx, ...)` EXACTLY ONCE
// (previously called twice — once in appendClipSourceText, once
// in appendClipDetail). The resolved transcript string +
// *asset.TextTrack are captured in the per-clip accumulator;
// the append helpers receive the pre-resolved transcript via
// a parameter (no second round-trip). The resolved track feeds
// the 3 new ClipEvidence fingerprint fields (LanguageCode,
// TextTrackVersion, TranscriptHash) populated by
// buildClipEvidence from the FIRST non-nil track.
//
// Layout (July 2026, clip_source_builder LONG-FILES split):
//
//   - clip_source_builder.go: the ClipSourceBuilder orchestrator —
//     struct + options + constructor + setters + BuildClipContext +
//     enrichClipSubtitle (this file).
//   - clip_source_resolve.go: the resolution domain — typedClipResolverPort,
//     resolveOneClip, resolveClipContextResult, clipContextRecord /
//     clipContextResult, clipResolveReason.
//   - clip_source_text.go: the text domain — truncateExcerpt,
//     clipDisplayName, chronologicalSortKey, clipTimeline,
//     parseMetadataMs, dedupTrimmedClipIDs, opts helpers, clipParallelism.
//   - clip_source_evidence.go: the evidence domain — buildClipEvidence /
//     appendClipSourceText / appendNarrativeClipText / appendClipDetail
//     (canonical evidence-construction surface; SSOT lives in the sibling).
//   - clip_source_builder_transcript.go: the transcript-resolution path.
//   - clip_source_builder_errors.go: the typed error surface.
//
// Single-orchestrator-invariant: BuildClipContext is the ONLY entry
// point that assembles the *scriptpkg.ClipEvidence surface — the
// sibling files expose helpers, never a second orchestrator.
package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// ── ClipSourceBuilder ───────────────────────────────────────────────────

type ClipSourceBuilder struct {
	clipsRepo    typedClipResolverPort
	ollamaClient any // *client.Client
	reranker     any
	log          *zap.Logger
	// textTrackReader is the canonical Fase 4 read surface for
	// the video pipeline (PR-PY-CLIPS-CORRETTE-TRADOTTE, July
	// 2026). The canonical post-cutover contract is that this
	// reader is ALWAYS wired by composition (production) or by
	// test fixture. A nil reader surfaces ErrTextTrackNotReady
	// — there is no `metadata_json[\\"transcript\\"]` /
	// `metadata_json[\\"clean_transcript\\"]` fallback path.
	textTrackReader   ports.TextTrackReader
	subtitleArtifacts asset.SubtitleArtifactRepository
}

type ClipGenerationOptions struct {
	Language      string
	Tone          string
	Style         string
	Title         string
	Model         string
	TargetWords   int
	NumClips      int
	SegmentWords  int
	SegmentTopics []string
	// Segments carries the per-block payload. Populated by the
	// curate / clips resolvers via SourceResolutionContext.
	// Currently unread at this layer; FASE 3 (engine_prompt.go)
	// reads plan.Segments directly when rendering per-segment
	// prompt blocks.
	Segments           []scriptpkg.ScriptSegment
	SourceText         string
	TranscriptPolicy   string
	OrderingStrategy   string
	StyleInstructions  string
	MinQualityScore    float64
	MinTranscriptWords int
	// RequireDriveLink controls whether clips without a Drive link
	// are excluded from the resolved set.
	RequireDriveLink bool
}

// NewClipSourceBuilder creates a ClipSourceBuilder backed by the
// supplied typed clip resolver.
func NewClipSourceBuilder(
	clipsRepo typedClipResolverPort,
	ollamaClient any,
	log *zap.Logger,
) *ClipSourceBuilder {
	return &ClipSourceBuilder{
		clipsRepo:    clipsRepo,
		ollamaClient: ollamaClient,
		log:          log,
	}
}

func (c *ClipSourceBuilder) SetReranker(r any) { c.reranker = r }

// ConfigureTextTrackReader wires the canonical Fase 4 read
// surface (TextTrackReader). The composition root calls this
// exactly once at startup, after NewClipSourceBuilder.
//
// godlike/06 SSOT: the call site is
// `internal/app/wire_script_resolvers.go::buildScriptSourceResolvers`.
// The Fase 4 contract is TextTrackReader-only — there is no
// second parameter because there is no longer a legacy
// `metadata_json[\\"transcript\\"]` fallback to gate.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil reader surfaces the
// lazy `*ErrTextTrackNotReady` (AssetID populated, the rest
// empty) — this is the composition-time-validation teller:
// production composition wires a real reader, dev/test
// fixtures must either wire a stub or document the failure
// mode explicitly.
func (c *ClipSourceBuilder) ConfigureTextTrackReader(r ports.TextTrackReader) {
	c.textTrackReader = r
}

// ConfigureSubtitleArtifactRepository wires the canonical READY ASS lookup
// used to enrich clip evidence before the Google Doc is rendered.
func (c *ClipSourceBuilder) ConfigureSubtitleArtifactRepository(r asset.SubtitleArtifactRepository) {
	c.subtitleArtifacts = r
}

// BuildClipContext resolves the supplied clip IDs into assets, builds
// the per-clip source text and assembles the canonical
// *scriptpkg.ClipEvidence surface.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the per-clip
// loop calls c.resolveTranscript EXACTLY ONCE per clip and threads
// the resolved transcript + *asset.TextTrack through the
// accumulator. The 3 new ClipEvidence fingerprint fields
// (LanguageCode, TextTrackVersion, TranscriptHash) are populated
// by buildClipEvidence from the FIRST non-nil resolved track
// (per-evidence language convention; multi-clip evidences
// carry the language of the first resolved clip).
func (c *ClipSourceBuilder) BuildClipContext(
	ctx context.Context,
	clipIDs []string,
	opts *ClipGenerationOptions,
) (*scriptpkg.ClipEvidence, string, string, error) {
	if c == nil {
		return nil, "", "", fmt.Errorf("clip source builder: not constructed")
	}
	if c.clipsRepo == nil {
		return nil, "", "", fmt.Errorf("clip source builder: clips repository not configured")
	}

	uniqueIDs, err := dedupTrimmedClipIDs(clipIDs)
	if err != nil {
		return nil, "", "", err
	}

	requireDriveLink := optsRequireDriveLink(opts)
	language := optsResolveLanguage(opts)
	orderingStrategy := ""
	if opts != nil {
		orderingStrategy = strings.TrimSpace(opts.OrderingStrategy)
	}

	var (
		missingClipIDs []scriptpkg.MissingClipID
		excludedClips  []scriptpkg.ExcludedClip
	)
	results := concurrent.ParallelMap(uniqueIDs, clipParallelism(len(uniqueIDs)), func(_ int, id string) clipContextResult {
		return c.resolveClipContextResult(ctx, id, language, requireDriveLink)
	})

	records := make([]clipContextRecord, 0, len(results))
	for _, result := range results {
		if result.missing != nil {
			missingClipIDs = append(missingClipIDs, *result.missing)
			continue
		}
		if result.record.id == "" || result.record.clip == nil {
			continue
		}
		records = append(records, result.record)
	}

	if len(records) == 0 && len(excludedClips) > 0 {
		return nil, "", "", fmt.Errorf("clip source builder: all %d resolved clips lack drive links", len(excludedClips))
	}
	if len(records) == 0 {
		return nil, "", "", fmt.Errorf("clip source builder: no clips found for the provided IDs")
	}

	if orderingStrategy == "chronological" {
		sort.SliceStable(records, func(i, j int) bool {
			return chronologicalSortKey(records[i].clip, records[i].id) < chronologicalSortKey(records[j].clip, records[j].id)
		})
	}

	var (
		renderableIDs   []string
		clips           []*asset.Asset
		canonicalIDs    []string
		clipNames       []string
		clipToCanonical = make(map[string]string, len(records))
		clipDetails     = make(map[string]scriptpkg.ClipDetail, len(records))
		// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026):
		// resolvedTracks is the per-clip accumulator for the
		// 3 new fingerprint fields. Keyed by the canonical
		// clip ID (the REQUESTED ID, not clip.ID). The
		// post-loop buildClipEvidence call reads the
		// FIRST non-nil entry to populate the evidence-level
		// fingerprint.
		resolvedTracks      = make(map[string]*asset.TextTrack, len(records))
		sourceTextWriter    strings.Builder
		narrativeTextWriter strings.Builder
	)
	for _, record := range records {
		if record.clip.DriveLink() != "" {
			renderableIDs = append(renderableIDs, record.id)
		}
		clips = append(clips, record.clip)
		canonicalIDs = append(canonicalIDs, record.id)
		clipToCanonical[record.clip.ID] = record.id
		clipNames = append(clipNames, clipDisplayName(record.clip, record.id))
		c.appendClipSourceText(&sourceTextWriter, record.id, record.clip, record.transcript)
		c.appendNarrativeClipText(&narrativeTextWriter, len(canonicalIDs)-1, record.clip, record.transcript)
		c.appendClipDetail(clipDetails, record.id, record.clip, record.transcript)
		c.enrichClipSubtitle(ctx, clipDetails, record.id)
		if record.track != nil {
			resolvedTracks[record.id] = record.track
		}
	}

	title := "script"
	if opts != nil {
		if v := strings.TrimSpace(opts.Title); v != "" {
			title = v
		}
	}

	ev := buildClipEvidence(
		canonicalIDs,
		clipNames,
		clipToCanonical,
		clips,
		renderableIDs,
		excludedClips,
		missingClipIDs,
		strings.TrimSpace(sourceTextWriter.String()),
		strings.TrimSpace(narrativeTextWriter.String()),
		clipDetails,
		resolvedTracks,
	)

	if c.log != nil {
		c.log.Info("clip source builder: context built",
			zap.Int("clip_ids", len(uniqueIDs)),
			zap.Int("clips_found", len(clips)),
			zap.Int("source_text_chars", sourceTextWriter.Len()))
	}

	return ev, title, sourceTextWriter.String(), nil
}

func (c *ClipSourceBuilder) enrichClipSubtitle(ctx context.Context, details map[string]scriptpkg.ClipDetail, clipID string) {
	if c == nil || c.subtitleArtifacts == nil || details == nil || clipID == "" {
		return
	}
	artifacts, err := c.subtitleArtifacts.ListByAsset(ctx, clipID)
	if err != nil {
		if c.log != nil {
			c.log.Warn("clip source builder: subtitle artifact lookup failed", zap.String("asset_id", clipID), zap.Error(err))
		}
		return
	}
	if c.log != nil {
		c.log.Debug("clip source builder: subtitle artifacts loaded",
			zap.String("asset_id", clipID),
			zap.Int("artifact_count", len(artifacts)))
	}
	for _, artifact := range artifacts {
		if artifact.Format != asset.SubtitleFormatASS || artifact.Status != asset.SubtitleStatusReady || !artifact.IsCurrent {
			continue
		}
		if strings.TrimSpace(artifact.DriveURL) == "" || strings.TrimSpace(artifact.DriveFileID) == "" {
			continue
		}
		detail := details[clipID]
		detail.SubtitleLink = artifact.DriveURL
		detail.SubtitleFileID = artifact.DriveFileID
		details[clipID] = detail
		return
	}
}
