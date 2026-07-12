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
package usecase

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

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

	var (
		renderableIDs   []string
		missingClipIDs  []scriptpkg.MissingClipID
		excludedClips   []scriptpkg.ExcludedClip
		clips           []*asset.Asset
		canonicalIDs    []string
		clipNames       []string
		clipToCanonical = make(map[string]string, len(uniqueIDs))
		clipDetails     = make(map[string]scriptpkg.ClipDetail, len(uniqueIDs))
		// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026):
		// resolvedTracks is the per-clip accumulator for the
		// 3 new fingerprint fields. Keyed by the canonical
		// clip ID (the REQUESTED ID, not clip.ID). The
		// post-loop buildClipEvidence call reads the
		// FIRST non-nil entry to populate the evidence-level
		// fingerprint.
		resolvedTracks   = make(map[string]*asset.TextTrack, len(uniqueIDs))
		sourceTextWriter strings.Builder
	)
	for _, id := range uniqueIDs {
		clip, reason := c.resolveOneClip(ctx, id)
		switch reason {
		case clipResolveOK:
			// fall through to enrich + append below
		case clipResolveNotFound:
			missingClipIDs = append(missingClipIDs, scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonNotFound,
			})
			continue
		default:
			return nil, "", "", fmt.Errorf("clip source builder: unknown resolve reason %q for id %q", reason, id)
		}

		if requireDriveLink && clip.DriveLink() == "" {
			missingClipIDs = append(missingClipIDs, scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonDriveNotFound,
			})
			if c.log != nil {
				c.log.Warn("clip source builder: clip lacks drive link (missing — Issue #2 bucket)",
					zap.String("clip_id", id))
			}
			continue
		}

		if clip.DriveLink() != "" {
			renderableIDs = append(renderableIDs, id)
		}

		clips = append(clips, clip)
		canonicalIDs = append(canonicalIDs, id)
		clipToCanonical[clip.ID] = id
		clipNames = append(clipNames, clipDisplayName(clip, id))

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
			// godlike/07 minimum-blast-radius: the typed
			// error is logged but NOT propagated (the
			// existing BuildClipContext signature is
			// preserved; strict-error surfacing lands in
			// a follow-up PR that threads the error up
			// to the HTTP handler).
			c.log.Warn("clip source builder: text track resolve returned error (continuing with empty transcript)",
				zap.String("clip_id", id),
				zap.String("language", language),
				zap.Error(resolveErr))
		}
		c.appendClipSourceText(&sourceTextWriter, id, clip, transcript)
		c.appendClipDetail(clipDetails, id, clip, transcript)
		if track != nil {
			resolvedTracks[id] = track
		}
	}

	if len(clips) == 0 && len(excludedClips) > 0 {
		return nil, "", "", fmt.Errorf("clip source builder: all %d resolved clips lack drive links", len(excludedClips))
	}
	if len(clips) == 0 {
		return nil, "", "", fmt.Errorf("clip source builder: no clips found for the provided IDs")
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

func clipDisplayName(clip *asset.Asset, id string) string {
	if name := strings.TrimSpace(clip.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(clip.Filename); name != "" {
		return name
	}
	return id
}

// appendClipSourceText writes the per-clip source text block
// (CLIP header + Description + Transcript + Tags + blank-line
// terminator) to the source-text writer.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the
// transcript string is PRE-RESOLVED by the caller (the main
// per-clip loop calls resolveTranscript exactly once and
// threads the result through). This method does NOT call
// resolveTranscript itself.
func (c *ClipSourceBuilder) appendClipSourceText(w *strings.Builder, id string, clip *asset.Asset, transcript string) {
	w.WriteString(fmt.Sprintf("CLIP %s: %s\n", id, clipDisplayName(clip, id)))
	if searchText := strings.TrimSpace(clip.SearchText); searchText != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", searchText))
	} else if desc := strings.TrimSpace(clip.GetMetadataString("description")); desc != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", desc))
	}
	if transcript != "" {
		excerpt := truncateExcerpt(transcript, excerptMaxRunes)
		w.WriteString(fmt.Sprintf("  Transcript: %s\n", excerpt))
	}
	if len(clip.Tags) > 0 {
		w.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(clip.Tags, ", ")))
	}
	w.WriteString("\n")
}

// appendClipDetail populates the per-clip detail map with the
// primary evidence used for clip-native scene construction.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the
// transcript string is PRE-RESOLVED by the caller.
func (c *ClipSourceBuilder) appendClipDetail(details map[string]scriptpkg.ClipDetail, id string, clip *asset.Asset, transcript string) {
	if details == nil || clip == nil || c == nil {
		return
	}
	desc := strings.TrimSpace(clip.SearchText)
	if desc == "" {
		desc = strings.TrimSpace(clip.GetMetadataString("description"))
	}
	startMs := parseMetadataMs(clip.GetMetadataString("start_ms"))
	endMs := parseMetadataMs(clip.GetMetadataString("end_ms"))
	details[id] = scriptpkg.ClipDetail{
		Name:        clipDisplayName(clip, id),
		Description: desc,
		Transcript:  transcript,
		Tags:        append([]string(nil), clip.Tags...),
		StartMs:     startMs,
		EndMs:       endMs,
		DriveLink:   clip.DriveLink(),
	}
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

// buildClipEvidence assembles the canonical *scriptpkg.ClipEvidence
// surface from the per-loop accumulators.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): takes the
// per-clip resolvedTracks map (keyed by canonical clip ID) and
// populates the 3 new fingerprint fields (LanguageCode,
// TextTrackVersion, TranscriptHash) from the FIRST non-nil
// track. godlike/07 minimum-blast-radius: the fingerprint is
// per-evidence, not per-clip; the "first" track is the
// canonical choice when multiple clips resolve (matches the
// existing per-evidence language convention).
func buildClipEvidence(
	canonicalIDs, clipNames []string,
	clipToCanonical map[string]string,
	clips []*asset.Asset,
	renderableIDs []string,
	excludedClips []scriptpkg.ExcludedClip,
	missingClipIDs []scriptpkg.MissingClipID,
	sourceText string,
	clipDetails map[string]scriptpkg.ClipDetail,
	resolvedTracks map[string]*asset.TextTrack,
) *scriptpkg.ClipEvidence {
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

	clipNameMap := make(map[string]string, len(canonicalIDs))
	for i, id := range canonicalIDs {
		if i < len(clipNames) && clipNames[i] != "" {
			clipNameMap[id] = clipNames[i]
		}
	}

	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): populate
	// the 3 new fingerprint fields from the FIRST non-nil
	// resolved track. When no track is available (legacy
	// path, missing-track path, mixed-with-no-ready), the
	// fields are left empty (the per-evidence fingerprint
	// inherits the per-clip fingerprint only when at least
	// one clip has a READY track).
	var lang, version, hash string
	for _, id := range canonicalIDs {
		if t, ok := resolvedTracks[id]; ok && t != nil {
			lang = t.LanguageCode
			version = t.SourceVersion
			hash = t.TextHash
			break
		}
	}

	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs:   canonicalIDs,
		RenderableClipIDs: renderableIDs,
		ClipCount:         len(canonicalIDs),
		AssembledText:     sourceText,
		DriveLinks:        clipDriveLinks,
		ClipNames:         clipNameMap,
		Excluded:          excludedClips,
		MissingClipIDs:    missingClipIDs,
		ClipDetails:       clipDetails,
		LanguageCode:      lang,
		TextTrackVersion:  version,
		TranscriptHash:    hash,
	}
	if len(ev.MissingClipIDs) == 0 {
		ev.MissingClipIDs = nil
	}
	if len(ev.Excluded) == 0 {
		ev.Excluded = nil
	}
	if len(ev.RenderableClipIDs) == 0 {
		ev.RenderableClipIDs = nil
	}
	return ev
}
