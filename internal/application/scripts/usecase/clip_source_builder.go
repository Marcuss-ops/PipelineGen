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

// clipsResolverPort is the narrow resolver interface that
// ClipSourceBuilder consumes. *assets.ClipsRepository satisfies it
// in production; unit tests inject a hand-rolled stub.
//
// P1 #7 (June 2026): the production constructor now accepts
// clipsResolverPort instead of the concrete *assets.ClipsRepository,
// removing the SQLite infra import from the use case layer.
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
	// RequireDriveLink controls whether clips without a Drive link
	// are excluded from the resolved set. When true (caller wants
	// document or scene images), clips without DriveLink go into
	// excludedClips. When false (text-only generation), missing
	// DriveLink is tolerated.
	// P0 #3 (June 2026).
	RequireDriveLink bool
}

// NewClipSourceBuilder creates a ClipSourceBuilder backed by the
// supplied clip resolver. In production, the concrete
// *assets.ClipsRepository (satisfying clipsResolverPort) is wired
// by internal/app/wire_script.go. Unit tests pass a hand-rolled
// stub directly — no separate test-only constructor is needed.
//
// P1 #7 (June 2026): parameter changed from *assets.ClipsRepository
// to clipsResolverPort, removing the SQLite infra import from the
// use case layer.
func NewClipSourceBuilder(
	clipsRepo clipsResolverPort,
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
// codepoints and emits invalid bytes downstream. The fingerprint
// (ComputeFingerprint) is unaffected — it never read this constant — and
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

		// P0 #3: check DriveLink before accepting the clip.
		// When DriveLink is required and the clip lacks one,
		// exclude it (but keep metadata for logging).
		hasDriveLink := clip.DriveLink() != ""
		if requireDriveLink && !hasDriveLink {
			excludedClips = append(excludedClips, scriptpkg.ExcludedClip{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonDriveNotFound,
			})
			if c.log != nil {
				c.log.Warn("clip source builder: clip lacks drive link (excluded)",
					zap.String("clip_id", id))
			}
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

	// P1 #6 (June 2026): Build ClipEvidence directly instead of an
	// untyped map[string]any pack + separate BuildClipEvidence call.
	ev := &scriptpkg.ClipEvidence{
		ClipIDs:        canonicalIDs,
		ClipCount:      len(canonicalIDs),
		AssembledText:  strings.TrimSpace(sourceTextBuilder.String()),
		DriveLinks:     clipDriveLinks,
		ClipNames:      clipNameMap,
		Excluded:       excludedClips,
		MissingClipIDs: missingClipIDs,
	}
	// Preserve nil for JSON omitempty.
	if len(ev.MissingClipIDs) == 0 {
		ev.MissingClipIDs = nil
	}
	if len(ev.Excluded) == 0 {
		ev.Excluded = nil
	}

	if c.log != nil {
		c.log.Info("clip source builder: context built",
			zap.Int("clip_ids", len(uniqueIDs)),
			zap.Int("clips_found", len(clips)),
			zap.Int("source_text_chars", sourceTextBuilder.Len()))
	}

	return ev, title, sourceTextBuilder.String(), nil
}

// ──────────────────────────────────────────────────────────────────────────
// AUDIT — P0#5 next iteration (snapshot 2026-06-29, HEAD = 54f47591).
//
// ComputeFingerprint (below) is the cache + idempotency key for the clip-
// evidence path. handler_legacy_adapters.go::warnIgnoredLegacyFields (lines
// ~616-684) emits a server-only WARN for every row in this table when the
// client supplies the field but the use case ignores it. This audit pins,
// for each affected field, exactly four facts:
//
//   1. Where the field is DECLARED in the request contract.
//   2. Where the use case continues to IGNORE the field at runtime.
//   3. Whether ComputeFingerprint ADMITS the value (cache key impact).
//   4. The disposition per godlike/07: IMPLEMENT | REJECT 400 | SUNSET-WARN.
//
// Per docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md, a "warning-only"
// field MUST pick exactly ONE of:
//   * REAL behaviour change — wire the field into BuildClipContext so the
//     warning becomes a no-op (the cache key may need to bump).
//   * HARD REJECT 400 — return BadRequest with a clear sunset_date,
//     to bound the legacy surface in prod.
//   * SOFT SUNSET — keep client compatibility for one release, but echo
//     `{field, sunset_date, replacement}` back in the response body
//     (A6) so the warning reaches the caller, not just the server log.
//
// Decision matrix (file:line references pinned at HEAD 54f47591):
//
//   Field                JSON              Declared (handler)              Ignored at                                                  v1 fingerprint impact            Candidate
//   ───────────────────  ────────────────  ─────────────────────────────  ──────────────────────────────────────────────────────────  ──────────────────────────────  ────────────
//   source_text          source_text       handler_legacy_adapters.go     clip_source_builder.go::ComputeFingerprint EXCLUDES          NOT included.                   IMPLEMENT
//                                            L116 (LegacyGenerate-         opts.SourceText; BuildClipContext never reads              (Built sourceText is the
//                                            FromClipsRequest)             opts.SourceText either (clip-evidence text               clip-evidence text only.)
//                                            + L283 (LegacyGenerate-        is composed from the resolved clips, never
//                                            BatchItem.SourceText)         from opts.SourceText.)
//   transcript_policy    transcript_policy handler_legacy_adapters.go     BuildClipContext transcript is hardcoded                   INCLUDED (transcript=v) but    IMPLEMENT
//                                            L149                          `excerpt[:500] + "..."` regardless of policy                the policy value is NEVER
//                                                                                                                                    applied to transcript
//                                                                                                                                    assembly.
//   min_quality_score    min_quality_score handler_legacy_adapters.go     BuildClipContext accepts clips unconditionally;            NOT included in                  IMPLEMENT
//                                            L142 (omitempty)               transferred to SourceSpec.MinQualityScore but             clip_source_builder.go         (needs clipsResolverPort
//                                                                                                                                    fingerprint. INCLUDED in        to expose GetQuality)
//                                                                                                                                    source_resolver_clips.go
//                                                                                                                                    via buildFingerprint `minq`.
//   min_transcript_words min_transcript_   NOT YET in                     Same as min_quality_score — value copied                   NOT included in any             IMPLEMENT
//                          words            handler_legacy_adapters.go     through SourceSpec but never filters short                fingerprint.
//                                            (forward-compat gap            transcripts. A5 must wire this declaration
//                                            introduced in PR1.7;          before sunset-warn semantics become
//                                            field exists on the           applicable.)
//                                            SourceSpec directly.)
//   intro_clip_ids       intro_clip_ids    handler_legacy_adapters.go     Merged into a single `introIDs` slice and stored           NOT a distinct fingerprint      SUNSET-WARN
//                                            L124                          on SourceSpec.IntroClipIDs (lines ~156-184)               segment — the merged            (becomes deprecated
//                                                                                                                                    list folds into clipIDs          when P0#X narrative-
//                                                                                                                                    upstream of ComputeFinger-       plan lands, at which
//                                                                                                                                    print, callers have no           point the merged
//                                                                                                                                    model behaviour to opt into.      `introIDs` is dropped.)
//   intro_clips          intro_clips       handler_legacy_adapters.go     Same as intro_clip_ids (mirror acceptance                  Same as intro_clip_ids.          SUNSET-WARN (mirror)
//                                            L125                          path).
//   clips[].title        clips[].title     handler_legacy_adapters.go     BuildClipContext uses clip.Name / clip.Filename            NOT included (fingerprint       REJECT 400
//                                            L108 (LegacyClipSpec)         from the repo — request title is never                   keyed on clip.ID only.)
//                                                                                                                                    persisted nor echoed into
//                                                                                                                                    the assembled prompt.
//   clips[].url          clips[].url       handler_legacy_adapters.go     BuildClipContext uses clip.DriveLink() from the            NOT included.                   REJECT 400
//                                            L109 (LegacyClipSpec)         repo — request url is never used; canonical               (DriveLink is the canonical
//                                                                                                                                    locator.)                       link.
//
// Two fingerprints exist in this codebase; this table reflects ONLY the one
// keyed on the "auto-generate from clip_ids" path. The OTHER fingerprint
// (source_resolver_clips.go::buildFingerprint, used by the curated envelope
// that goes through SourceSpec + SourceText) is structurally separate and
// will be audited in a follow-up bite (tracked alongside A2).
//
// When A2 — FINGERPRINT WHITELIST lands, every "NOT included" cell MUST
// stay not-included (and the cells where the value is "INCLUDED but
// ignored" must move to "INCLUDED and applied") to honour the cache-
// invalidation invariant this table pins.
// ──────────────────────────────────────────────────────────────────────────

// fingerprintVersionTag is the canonical cache-key version marker
// embedded in every ComputeFingerprint output.
//
// The presence of this segment in v2 retires every cached entry
// produced under the previous (v1-default) scheme that lacked the
// "applied-only" guarantee (A1 audit, A2 refactor, June 2026). When
// the whitelist itself changes again, bump the version tag (e.g. to
// "v3-...") so all prior fingerprints become invalid without any
// operator intervention.
const fingerprintVersionTag = "fp=v2-apply-only"

// ComputeFingerprint builds a stable cache + idempotency key for the
// auto-generate-from-clip-ids path.
//
// v2 (June 2026, A2 refactor): the key is a strict whitelist of the
// inputs the assembled clip-evidence / script-generation pipeline
// actually reads. The previous (v1) scheme ad-hoc-included
// opts.SourceText and opts.TranscriptPolicy — both of which callers
// routinely set without the use case applying them, so changing the
// client-side value mutated the fingerprint but NOT the output.
// Cache-key poison. See A1 AUDIT matrix in this file.
//
// WHITELIST (folded in, with the apply-only invariant):
//
//	✓ title          opts.Title
//	✓ lang           opts.Language
//	✓ tone           opts.Tone
//	✓ model          opts.Model
//	✓ transcript     opts.TranscriptPolicy      (A7f will switch
//	                                             BuildClipContext to
//	                                             implement this; until
//	                                             then it stays in the
//	                                             key because callers
//	                                             set it as a forward-
//	                                             looking input.)
//	✓ order          opts.OrderingStrategy
//
// STRUCTURAL EXCLUSION (folded in ZERO, by name, even when set):
//
//	✗ opts.SourceText          — text-source path only.
//	✗ opts.SegmentTopics       — text-source path only (the "Topic"
//	                             field on the request envelope).
//	✗ opts.MinQualityScore     — A7g pending — clip-quality filter.
//	✗ opts.MinTranscriptWords  — A7h pending — sentence-level drop.
//
// When the clip path is active (`ev != nil` at the call site), opts.SourceText
// and opts.SegmentTopics are deliberately *not* in the key even when callers
// set them — they only flow into the text-source path and including them
// would re-introduce the v1 poison. This is enforced structurally, not by
// runtime branching: the exclusion is unconditional.
//
// fpCtx (interface{}) is preserved for caller back-compat; v2 does
// NOT fold fpCtx's `model` / `prompt_model` map keys into the
// fingerprint because they duplicate opts.Model in the typical call
// and including both would re-introduce the v1 ambiguity. Bumping
// `fingerprintVersionTag` to "v3-..." (or later) is the clean way to
// recover fpCtx integration when needed.
func (c *ClipSourceBuilder) ComputeFingerprint(
	clipIDs []string,
	opts *ClipGenerationOptions,
	fpCtx interface{},
) string {
	_ = fpCtx

	parts := []string{"clips"}
	for _, id := range clipIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			parts = append(parts, id)
		}
	}

	addIfSet := func(key, val string) {
		val = strings.TrimSpace(val)
		if val == "" {
			return
		}
		parts = append(parts, key+"="+val)
	}

	if opts != nil {
		// WHITELIST ONLY — strict.
		addIfSet("title", opts.Title)
		addIfSet("lang", opts.Language)
		addIfSet("tone", opts.Tone)
		addIfSet("model", opts.Model)
		addIfSet("transcript", opts.TranscriptPolicy)
		addIfSet("order", opts.OrderingStrategy)
		// INTENTIONALLY NOT ADDED: opts.SourceText, opts.SegmentTopics
		// (request topic), opts.MinQualityScore, opts.MinTranscriptWords.
		// See whitelist / exclusion matrix in the doc comment above.
	}

	parts = append(parts, fingerprintVersionTag)
	return strings.Join(parts, "|")
}

func NewFingerprintContext(model, promptModel string) interface{} {
	return map[string]string{
		"model":        strings.TrimSpace(model),
		"prompt_model": strings.TrimSpace(promptModel),
	}
}
