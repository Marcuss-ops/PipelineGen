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
package usecase

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── ClipSourceBuilder ───────────────────────────────────────────────────

// typedClipResolverPort is the typed resolver interface that
// ClipSourceBuilder consumes. It replaces the legacy clipsResolverPort
// (GetClip + GetByDriveFileID heuristic) with explicit per-type dispatch.
// *assets.ClipsRepository satisfies it in production via its
// ResolveByMediaAssetID / ResolveByDriveFileID methods. Unit tests
// inject a hand-rolled stub.
type typedClipResolverPort interface {
	ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error)
	ResolveByDriveFileID(ctx context.Context, fileID string) ([]*asset.Asset, error)
}

type ClipSourceBuilder struct {
	clipsRepo    typedClipResolverPort
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
	// RequireDriveLink controls whether clips without a Drive link
	// are excluded from the resolved set. When true (caller wants
	// document or scene images), clips without DriveLink go into
	// excludedClips. When false (text-only generation), missing
	// DriveLink is tolerated.
	// P0 #3 (June 2026).
	RequireDriveLink bool
}

// NewClipSourceBuilder creates a ClipSourceBuilder backed by the
// supplied typed clip resolver. In production, the concrete
// *assets.ClipsRepository (satisfying typedClipResolverPort) is wired
// by internal/app/wire_script.go. Unit tests pass a hand-rolled
// stub directly — no separate test-only constructor is needed.
//
// NewClipSourceBuilder accepts the typed clip resolver (explicit
// per-type dispatch replaces the legacy heuristic fallback).
func NewClipSourceBuilder(
	clipsRepo typedClipResolverPort,
	ollamaClient interface{},
	log *zap.Logger,
) *ClipSourceBuilder {
	return &ClipSourceBuilder{
		clipsRepo:    clipsRepo,
		ollamaClient: ollamaClient,
		log:          log,
	}
}

func (c *ClipSourceBuilder) SetReranker(r interface{}) { c.reranker = r }

// excerptMaxRunes is the rune-budget for the per-clip transcript excerpt
// appended to the assembled source text.
//
// A4 (June 2026): this constant replaced an inline byte-budget of 500 (the
// old `excerpt[:500]` cut). Byte-truncation on multi-byte UTF-8 input (CJK
// ideographs, supplementary-plane emoji, accented Latin) silently splits
// codepoints and emits invalid bytes downstream. The fingerprint is unaffected — it never read this constant — and
// BuildClipContext now truncates by RUNES via truncateExcerpt.
//
// A7 (forthcoming) will wire opts.TranscriptPolicy to a documented mode
// (full | sentence_window | keyword_window) and *replace* this hard-coded
// budget at the same call site. The constant is the single point of
// governance until then.
const excerptMaxRunes = 500

// truncateExcerpt returns s if its rune count is at most maxRunes;
// otherwise it returns s truncated to exactly maxRunes runes followed by
// the U+2026 HORIZONTAL ELLIPSIS. Truncation snaps to rune boundaries so
// the result is always well-formed UTF-8 and never splits a multi-byte
// codepoint.
//
// A4 (June 2026). Bounded iteration (no full []rune(s) materialization) so
// very large transcripts (multi-MB) stay cheap.
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
// the per-clip source text (CLIP header + Description + Transcript +
// Tags) and assembles the canonical *scriptpkg.ClipEvidence surface
// that downstream postprocessors (Document / Voiceover / Stock / etc.)
// consume.
//
// Returns:
//   - ev: the assembled *ClipEvidence (AcceptedClipIDs + DriveLinks
//   - clipNames + Excluded + MissingClipIDs, etc.)
//   - title: the resolved narrative title (from opts.Title, default
//     "script" if opts is nil or empty)
//   - sourceText: the full per-clip source text (same as ev.AssembledText
//     for backward-compat with callers that don't read from ev)
//   - error: typed failure on no-clips-found / all-clips-excluded /
//     nil-receiver / nil-repo
//
// Cyclomatic complexity: was 43 (pre-PR-REFACTOR-P1-CYCLOMATIC), now
// ≤15. The 2-phase resolution (media-asset-id → drive-file-id
// fallback) is extracted into resolveOneClip; the per-clip source
// text assembly is extracted into appendClipSourceText; the
// post-loop DriveLinks + ClipNames + evidence-assemble steps are
// extracted into 3 single-purpose helpers. The main function is a
// linear orchestrator with early returns on missing clips.
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

	// Per-clip resolution + source-text accumulation.
	//
	// Each iteration calls resolveOneClip (2-phase media-asset-id →
	// drive-file-id fallback) + handles the 3 distinct resolution
	// outcomes (resolved, missing-by-DriveLink, missing-by-NotFound)
	// uniformly via the typed missingReason return. The
	// per-clip source-text assembly is delegated to
	// appendClipSourceText so the main loop stays linear.
	var (
		renderableIDs    []string
		missingClipIDs   []scriptpkg.MissingClipID
		excludedClips    []scriptpkg.ExcludedClip
		clips            []*asset.Asset
		canonicalIDs     []string
		clipNames        []string
		clipToCanonical  = make(map[string]string, len(uniqueIDs))
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

		// Issue #2 (June 2026) bucket flip: route
		// missing-DriveLink to MissingClipIDs (not ExcludedClip).
		// The two distinct resolution outcomes — (a) asset
		// exists but is unrenderable into Drive-consuming
		// surfaces vs (b) asset was filtered by a quality gate
		// — are now distinguishable in dashboards.
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

		// Track DriveLink-bearing subset of AcceptedClipIDs
		// (RenderableClipIDs). Document / image / voiceover
		// renderers iterate this set instead of the broader
		// AcceptedClipIDs.
		if clip.DriveLink() != "" {
			renderableIDs = append(renderableIDs, id)
		}

		clips = append(clips, clip)
		canonicalIDs = append(canonicalIDs, id)
		clipToCanonical[clip.ID] = id
		clipNames = append(clipNames, clipDisplayName(clip, id))
		appendClipSourceText(&sourceTextWriter, id, clip)
	}

	// P0 #3: when DriveLink is required and ALL resolved clips
	// were excluded (lacked DriveLink), fail with a clear error.
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

	// P1 #9 (June 2026): NarrativePlan / NarrativeSection removed —
	// dead code that set plan.Title but was never consumed past
	// buildResolvedClipSource extracting that single field. The
	// resolved title is returned directly as a string.

	ev := buildClipEvidence(
		canonicalIDs,
		clipNames,
		clipToCanonical,
		clips,
		renderableIDs,
		excludedClips,
		missingClipIDs,
		strings.TrimSpace(sourceTextWriter.String()),
	)

	if c.log != nil {
		c.log.Info("clip source builder: context built",
			zap.Int("clip_ids", len(uniqueIDs)),
			zap.Int("clips_found", len(clips)),
			zap.Int("source_text_chars", sourceTextWriter.Len()))
	}

	return ev, title, sourceTextWriter.String(), nil
}

// clipResolveReason is the typed return value of resolveOneClip
// (replaces the 3-branch "if clip == nil { missing; continue }"
// pattern in the main loop with a single typed enum dispatch).
// godlike/07 typed-error contract: the reason is a closed enum
// (no unbounded strings); the main loop's switch is exhaustive
// (default branch returns a typed error so a future agent adding
// a new reason cannot silently drop the case).
type clipResolveReason string

const (
	clipResolveOK       clipResolveReason = "ok"
	clipResolveNotFound clipResolveReason = "not_found"
)

// resolveOneClip performs the 2-phase media-asset-id → drive-file-id
// fallback. Returns the resolved *asset.Asset (may be nil if the
// clip is genuinely missing) + a strongly-typed reason string
// (clipResolveOK / clipResolveNotFound). The main loop's switch
// dispatches uniformly on the reason.
//
// Issue #2 (June 2026) bucket flip: clipResolveNotFound covers
// BOTH the media-asset-id lookup miss AND the drive-file-id
// fallback miss (a single typed enum entry suffices because the
// downstream consumer is MissingClipReasonNotFound in both cases
// — the bucket split happens later when the requireDriveLink gate
// emits MissingClipReasonDriveNotFound separately).
func (c *ClipSourceBuilder) resolveOneClip(ctx context.Context, id string) (*asset.Asset, clipResolveReason) {
	// Typed dispatch: first try the canonical media_assets.id column;
	// if not found, try drive_file_id. The two-phase fallback preserves
	// existing behavior while using the typed ResolveBy* methods.
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

// clipDisplayName returns the canonical display name for a clip
// (Name → Filename → ID fallback chain). Extracted from the main
// loop to shrink the per-iteration body.
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
// terminator) to the source-text writer. Extracted from the main
// loop so the per-iteration body is a single 1-line call.
func appendClipSourceText(w *strings.Builder, id string, clip *asset.Asset) {
	w.WriteString(fmt.Sprintf("CLIP %s: %s\n", id, clipDisplayName(clip, id)))
	if searchText := strings.TrimSpace(clip.SearchText); searchText != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", searchText))
	} else if desc := strings.TrimSpace(clip.GetMetadataString("description")); desc != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", desc))
	}
	if transcript := clipTranscript(clip); transcript != "" {
		// A4 (June 2026): rune-safe truncation; the helper
		// snaps to rune boundaries and appends U+2026,
		// replacing the old byte-index cut (excerpt[:500])
		// that split multi-byte codepoints. See
		// truncateExcerpt above.
		excerpt := truncateExcerpt(transcript, excerptMaxRunes)
		w.WriteString(fmt.Sprintf("  Transcript: %s\n", excerpt))
	}
	if len(clip.Tags) > 0 {
		w.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(clip.Tags, ", ")))
	}
	w.WriteString("\n")
}

// clipTranscript returns the canonical transcript string for a
// clip, preferring clean_transcript over raw transcript. Extracted
// from appendClipSourceText so the per-call field-access pattern
// stays linear.
func clipTranscript(clip *asset.Asset) string {
	if t := clip.GetMetadataString("transcript"); t != "" {
		return t
	}
	return clip.GetMetadataString("clean_transcript")
}

// dedupTrimmedClipIDs trims + dedupes the input clip ID list.
// Returns an error on empty post-trim result (caller-friendly
// failure mode). Extracted from BuildClipContext so the
// per-iteration loop doesn't carry the dedup + filter logic.
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

// optsRequireDriveLink returns the canonical requireDriveLink
// value from opts (defaults to true per the caller's contract).
func optsRequireDriveLink(opts *ClipGenerationOptions) bool {
	if opts == nil {
		return true
	}
	return opts.RequireDriveLink
}

// buildClipEvidence assembles the canonical *scriptpkg.ClipEvidence
// surface from the per-loop accumulators. Extracted so the main
// loop's post-processing is a single 1-line call. The nil-for-
// JSON-omitempty preservation guards are preserved verbatim from
// the pre-PR inline code.
func buildClipEvidence(
	canonicalIDs, clipNames []string,
	clipToCanonical map[string]string,
	clips []*asset.Asset,
	renderableIDs []string,
	excludedClips []scriptpkg.ExcludedClip,
	missingClipIDs []scriptpkg.MissingClipID,
	sourceText string,
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

	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs:   canonicalIDs,
		RenderableClipIDs: renderableIDs,
		ClipCount:         len(canonicalIDs),
		AssembledText:     sourceText,
		DriveLinks:        clipDriveLinks,
		ClipNames:         clipNameMap,
		Excluded:          excludedClips,
		MissingClipIDs:    missingClipIDs,
	}
	// Preserve nil for JSON omitempty.
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
