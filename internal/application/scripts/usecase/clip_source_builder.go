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
// Commit (refactor Step 7 — clip_source_builder split, July 2026):
// buildClipEvidence / appendClipSourceText / appendClipDetail moved
// to clip_source_evidence.go (single-orchestrator-invariants — these
// are the canonical evidence-construction surface; SSOT lives in the
// sibling file). cross-refs intra-package preserved.
package usecase

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// ── ClipSourceBuilder ───────────────────────────────────────────────────

type typedClipResolverPort interface {
	ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error)
	ResolveByDriveFileID(ctx context.Context, fileID string) ([]*asset.Asset, error)
}

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
	// — there is no `metadata_json[\"transcript\"]` /
	// `metadata_json[\"clean_transcript\"]` fallback path.
	textTrackReader ports.TextTrackReader
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
// `metadata_json[\"transcript\"]` fallback to gate.
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

const excerptMaxRunes = 500
const defaultClipParallelism = 4

// truncateExcerpt returns s if its rune count is at most maxRunes;
// otherwise it returns s truncated to exactly maxRunes runes followed by
// the U+2026 HORIZONTAL ELLIPSIS.
func truncateExcerpt(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := make([]rune, 0, maxRunes+1)
	for _, r := range s {
		if len(runes) == maxRunes {
			break
		}
		runes = append(runes, r)
	}
	return string(runes) + "\u2026"
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

type clipContextRecord struct {
	id         string
	clip       *asset.Asset
	transcript string
	track      *asset.TextTrack
}

type clipContextResult struct {
	record  clipContextRecord
	missing *scriptpkg.MissingClipID
}

func chronologicalSortKey(clip *asset.Asset, id string) int64 {
	if clip != nil {
		startMs, _ := clipTimeline(clip)
		if startMs >= 0 {
			return startMs
		}
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			continue
		}
		j := i + 1
		for j < len(id) && id[j] >= '0' && id[j] <= '9' {
			j++
		}
		if n, err := strconv.ParseInt(id[i:j], 10, 64); err == nil {
			return n
		}
		i = j - 1
	}
	return int64(^uint64(0) >> 1)
}

// clipTimeline is the single timestamp projection for indexed clips. Ingested
// boxing chunks commonly carry chunk_index + chunk_duration_sec instead of
// explicit millisecond offsets; both representations must produce the same
// binding contract downstream.
func clipTimeline(clip *asset.Asset) (int64, int64) {
	if clip == nil {
		return -1, -1
	}
	startMs := int64(clip.GetMetadataInt("start_ms"))
	endMs := int64(clip.GetMetadataInt("end_ms"))
	if endMs > startMs {
		return startMs, endMs
	}
	chunkIndex := int64(clip.GetMetadataInt("chunk_index"))
	chunkDurationSec := int64(clip.GetMetadataInt("chunk_duration_sec"))
	if chunkIndex >= 0 && chunkDurationSec > 0 {
		startMs = chunkIndex * chunkDurationSec * 1000
		return startMs, startMs + chunkDurationSec*1000
	}
	return -1, -1
}

// clipResolveReason is the typed return value of resolveOneClip.
type clipResolveReason string

const (
	clipResolveOK       clipResolveReason = "ok"
	clipResolveNotFound clipResolveReason = "not_found"
)

func (c *ClipSourceBuilder) resolveOneClip(ctx context.Context, id string) (*asset.Asset, clipResolveReason) {
	clip, err := c.clipsRepo.ResolveByMediaAssetID(ctx, id)
	if err != nil && c.log != nil {
		c.log.Warn("clip source builder: failed to fetch clip by media asset id",
			zap.String("clip_id", id),
			zap.Error(err))
	}
	if clip == nil {
		list, driveErr := c.clipsRepo.ResolveByDriveFileID(ctx, id)
		if driveErr != nil {
			if c.log != nil {
				c.log.Warn("clip source builder: failed to fetch clip by drive file id",
					zap.String("clip_id", id),
					zap.Error(driveErr))
			}
			return nil, clipResolveNotFound
		}
		if len(list) > 0 {
			clip = list[0]
		}
	}
	if clip == nil {
		return nil, clipResolveNotFound
	}
	return clip, clipResolveOK
}

func (c *ClipSourceBuilder) resolveClipContextResult(
	ctx context.Context,
	id string,
	language string,
	requireDriveLink bool,
) clipContextResult {
	clip, reason := c.resolveOneClip(ctx, id)
	switch reason {
	case clipResolveOK:
		// fall through to the drive-link / transcript checks below.
	case clipResolveNotFound:
		return clipContextResult{
			missing: &scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonNotFound,
			},
		}
	default:
		if c.log != nil {
			c.log.Warn("clip source builder: unknown resolve reason", zap.String("clip_id", id), zap.String("reason", string(reason)))
		}
		return clipContextResult{
			missing: &scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonNotFound,
			},
		}
	}

	if requireDriveLink && clip.DriveLink() == "" {
		if c.log != nil {
			c.log.Warn("clip source builder: clip lacks drive link (missing — Issue #2 bucket)",
				zap.String("clip_id", id))
		}
		return clipContextResult{
			missing: &scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonDriveNotFound,
			},
		}
	}

	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026):
	// resolveTranscript is called EXACTLY ONCE per clip.
	// The signature is (string, *asset.TextTrack, error) —
	// transcript string first, resolved track second,
	// error third. The resolved transcript feeds both
	// the assembled source text (via appendClipSourceText)
	// and the per-clip ClipDetail.Transcript (via
	// appendClipDetail). The resolved *asset.TextTrack
	// feeds the 3 new fingerprint fields (via the
	// resolvedTracks accumulator + buildClipEvidence).
	transcript, track, resolveErr := c.resolveTranscript(ctx, clip.ID, language, clip)
	if resolveErr != nil && c.log != nil {
		// godlike/07 minimum-blast-radius: the typed error is
		// logged but NOT propagated (the existing BuildClipContext
		// signature is preserved; strict-error surfacing lands in a
		// follow-up PR that threads the error up to the HTTP handler).
		c.log.Warn("clip source builder: text track resolve returned error (continuing with empty transcript)",
			zap.String("clip_id", id),
			zap.String("language", language),
			zap.Error(resolveErr))
	}

	return clipContextResult{
		record: clipContextRecord{
			id:         id,
			clip:       clip,
			transcript: transcript,
			track:      track,
		},
	}
}

func clipDisplayName(clip *asset.Asset, id string) string {
	if name := strings.TrimSpace(clip.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(clip.Filename); name != "" {
		return name
	}
	return id
}

func parseMetadataMs(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return ms
}

func dedupTrimmedClipIDs(clipIDs []string) ([]string, error) {
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
		return nil, fmt.Errorf("clip source builder: no valid clip IDs provided")
	}
	return uniqueIDs, nil
}

func optsRequireDriveLink(opts *ClipGenerationOptions) bool {
	if opts == nil {
		return true
	}
	return opts.RequireDriveLink
}

func optsResolveLanguage(opts *ClipGenerationOptions) string {
	if opts == nil {
		return ""
	}
	return strings.TrimSpace(opts.Language)
}

func clipParallelism(count int) int {
	if count <= 0 {
		return 1
	}
	if count < defaultClipParallelism {
		return count
	}
	return defaultClipParallelism
}
