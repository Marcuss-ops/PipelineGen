// Package scripts — clip_source_builder.go replaces the ClipSourceBuilder
// stub with a real implementation that fetches clips from the repository
// and builds context from their metadata + transcripts.
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
// ResolveByMediaAssetID / ResolveByDriveFileID methods (TODO #1 CUTOVER,
// June 2026). Unit tests inject a hand-rolled stub.
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
// TODO #1 CUTOVER (June 2026): parameter changed from clipsResolverPort
// (GetClip + GetByDriveFileID) to typedClipResolverPort (ResolveByMediaAssetID
// + ResolveByDriveFileID), switching from heuristic fallback to explicit
// per-type dispatch.
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
		return nil, "", "", fmt.Errorf("clip source builder: no valid clip IDs provided")
	}

	clipsRepo := c.clipsRepo
	if clipsRepo == nil {
		return nil, "", "", fmt.Errorf("clip source builder: clips repository not configured")
	}

	clips := make([]*asset.Asset, 0, len(uniqueIDs))
	clipNames := make([]string, 0, len(uniqueIDs))
	canonicalIDs := make([]string, 0, len(uniqueIDs))
	var renderableIDs []string // Issue #2 (June 2026): DriveLink-bearing subset of AcceptedClipIDs
	var missingClipIDs []scriptpkg.MissingClipID
	var excludedClips []scriptpkg.ExcludedClip
	clipToCanonical := make(map[string]string, len(uniqueIDs))
	var sourceTextBuilder strings.Builder

	// P0 #3 (June 2026): determine whether DriveLink is required.
	// When false (text-only generation), clips without Drive links
	// are still accepted — only transcript + metadata are needed.
	requireDriveLink := true
	if opts != nil {
		requireDriveLink = opts.RequireDriveLink
	}

	for _, id := range uniqueIDs {
		// TODO #1 CUTOVER (June 2026): typed dispatch replaces the
		// legacy GetClip → GetByDriveFileID heuristic. First try
		// the canonical media_assets.id column; if not found, try
		// drive_file_id. The two-phase fallback preserves existing
		// behavior while using the typed ResolveBy* methods.
		clip, err := clipsRepo.ResolveByMediaAssetID(ctx, id)
		if err != nil {
			if c.log != nil {
				c.log.Warn("clip source builder: failed to fetch clip by media asset id",
					zap.String("clip_id", id),
					zap.Error(err))
			}
		}
		if clip == nil {
			list, driveErr := clipsRepo.ResolveByDriveFileID(ctx, id)
			if driveErr != nil {
				if c.log != nil {
					c.log.Warn("clip source builder: failed to fetch clip by drive file id",
						zap.String("clip_id", id),
						zap.Error(driveErr))
				}
				missingClipIDs = append(missingClipIDs, scriptpkg.MissingClipID{
					ClipID: id,
					Reason: scriptpkg.MissingClipReasonNotFound,
				})
				continue
			}
			if len(list) > 0 {
				clip = list[0]
			}
		}
		if clip == nil {
			missingClipIDs = append(missingClipIDs, scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonNotFound,
			})
			continue
		}

		// Issue #2 (June 2026): bucket flip restructured. The
		// old code appended MissingClipReasonDriveNotFound to
		// ExcludedClip when RequireDriveLink=true && hasDriveLink=
		// false. That conflated two distinct resolution outcomes:
		// (a) the asset exists but is unrenderable into Drive-
		// consuming surfaces (infrastructure) and (b) the asset
		// resolved but was filtered by a quality gate (filter).
		// Post-fix: route (a) to MissingClipIDs with
		// MissingClipReasonDriveNotFound. ExcludedClip remains
		// reserved for (b) — quality-filter rejections. Dashboards
		// can now bucket "asset unavailable to render" separately
		// from "asset filtered by quality gate".
		hasDriveLink := clip.DriveLink() != ""
		if requireDriveLink && !hasDriveLink {
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

		// Issue #2 (June 2026): RenderableClipIDs tracking.
		// Any accepted clip that additionally carries a DriveLink
		// belongs to the renderable subset. Document / image /
		// voiceover renderers iterate this set instead of the
		// broader AcceptedClipIDs. IMPORTANT: renderableIDs only
		// captures clips that survived the requireDriveLink gate
		// above; when requireDriveLink=false, hasDriveLink may
		// be true or false per-clip and we still track here.
		if hasDriveLink {
			renderableIDs = append(renderableIDs, id)
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
			// A4 (June 2026): rune-safe truncation; the helper snaps to
			// rune boundaries and appends U+2026, replacing the old
			// byte-index cut (`excerpt[:500]`) that split multi-byte
			// codepoints. See truncateExcerpt above.
			excerpt := truncateExcerpt(transcript, excerptMaxRunes)
			sourceTextBuilder.WriteString(fmt.Sprintf("  Transcript: %s\n", excerpt))
		}
		if len(clip.Tags) > 0 {
			sourceTextBuilder.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(clip.Tags, ", ")))
		}
		sourceTextBuilder.WriteString("\n")
	}

	// P0 #3: when DriveLink is required and ALL resolved clips were
	// excluded (lacked DriveLink), fail with a clear error.
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

	// P0 #3: build DriveLinks from accepted clips only (all have
	// DriveLink when requireDriveLink is true).
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

	// Build clip name map (clip_id → name) for evidence.
	clipNameMap := make(map[string]string, len(canonicalIDs))
	for i, id := range canonicalIDs {
		if i < len(clipNames) && clipNames[i] != "" {
			clipNameMap[id] = clipNames[i]
		}
	}

	// Issue #2 (June 2026): Build ClipEvidence directly using the
	// new contract. AcceptedClipIDs is the transcript-usable
	// resolved set (renamed from the ambiguous ClipIDs).
	// RenderableClipIDs is the DriveLink-bearing subset.
	// ExcludedClip's MissingClipReasonDriveNotFound bucket is gone
	// (post-fix, that reason only appears in MissingClipIDs).
	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs:   canonicalIDs,
		RenderableClipIDs: renderableIDs,
		ClipCount:         len(canonicalIDs),
		AssembledText:     strings.TrimSpace(sourceTextBuilder.String()),
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

	if c.log != nil {
		c.log.Info("clip source builder: context built",
			zap.Int("clip_ids", len(uniqueIDs)),
			zap.Int("clips_found", len(clips)),
			zap.Int("source_text_chars", sourceTextBuilder.Len()))
	}

	return ev, title, sourceTextBuilder.String(), nil
}
