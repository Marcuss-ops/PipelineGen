package core

import (
	"context"
	"net/url"
	"strings"

	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Canonical SourceProvider bucket identifiers (PR-003, July 2026).
// godlike/06 SSOT — one canonical owner per fact: the inference
// helper inferSourceProvider lives in this file (the canonical
// place where SourceID is consumed). All downstream consumers
// (ChunkState, ChunkMetadataEntry, StepRunner publishers) reference
// these constants via plan.SourceProvider — no stringly-typed
// branch anywhere in the pipeline.
const (
	SourceProviderYouTube = "youtube"
	SourceProviderPexels  = "pexels"
	SourceProviderPixabay = "pixabay"
	// SourceProviderUnknown is the godlike/07 NO-FAKE-AVAILABILITY
	// sentinel for non-classifiable URLs (direct mp4 blobs, Vimeo,
	// archive.org, etc.). Inference emits this literal — empty
	// string is reserved for "not yet inferred" (defensive only;
	// build paths always fill this field on a populated plan).
	SourceProviderUnknown = "unknown"
)

// InferSourceProvider classifies a source URL into the canonical
// 4-bucket provider taxonomy. Single canonical implementer per
// godlike/06 SSOT — every consumer reads plan.SourceProvider which
// is set ONCE at plan-build time.
//
// godlike/07 NO-FAKE-AVAILABILITY: parsing the hostname (not
// substring-matching the full URL) closes the SSRF-style false-
// positive class — a URL like
// `https://fake-youtube.com.attacker.io/video.mp4` would have
// matched the substring-based classification. We delegate to
// net/url.Parse + Hostname() + suffix/exact-host match so the
// only counts are on real YouTube/Pexels/Pixabay domains. Bare
// non-URL inputs (parse error OR empty host) collapse to
// SourceProviderUnknown — the bucket is observable and never
// silent-empty.
func InferSourceProvider(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return SourceProviderUnknown
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return SourceProviderUnknown
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return SourceProviderUnknown
	}
	switch {
	case host == "youtu.be",
		host == "youtube.com",
		strings.HasSuffix(host, ".youtube.com"):
		return SourceProviderYouTube
	case host == "pexels.com", strings.HasSuffix(host, ".pexels.com"):
		return SourceProviderPexels
	case host == "pixabay.com", strings.HasSuffix(host, ".pixabay.com"):
		return SourceProviderPixabay
	}
	return SourceProviderUnknown
}

// InferSourceVideoID extracts the canonical video ID ONLY when
// the URL belongs to the YouTube provider bucket. Returns "" for
// non-YouTube URLs (provider mismatch — caller can rely on
// plan.SourceProvider too) AND for malformed YouTube URLs where
// ExtractVideoID errors (channel pages, playlists, bare /UCxxx).
//
// godlike/07 fail-open rationale: SourceVideoID is an
// observability field (not a gate). Failing the entire chunk
// build because a YouTube channel-page URL has no watch-ID would
// surface false-positive FAILED jobs for an otherwise valid
// SourceURL. Callers that NEED the ID can re-run the extraction
// verbatim — the function is pure + deterministic.
func InferSourceVideoID(rawURL string) string {
	vid, err := urlutil.ExtractVideoID(rawURL)
	if err != nil {
		return ""
	}
	return vid
}

// ClipPlan is the deterministic output of the ClipPlanner.
// The plan is persisted as data so subsequent phases (download,
// cut, retry) consume it without re-computing offsets.
type ClipPlan struct {
	// SourceID is the canonical identifier of the source (URL or
	// VideoID). Carried through the pipeline so retries can
	// re-fetch without re-planning.
	SourceID string
	// StageKey identifies the section-local staged file when the run uses
	// sections_only. Empty means the whole source is staged once.
	StageKey string

	// SourceProvider is the canonical provider bucket for
	// SourceID — inference happens at plan-build time so all
	// downstream consumers (stager, cutter, publish step,
	// metadata.json) read the same value. Possible values
	// (canonical constants above): youtube / pexels / pixabay /
	// unknown. Locked to inferSourceProvider() — call sites
	// MUST NOT re-parse the URL.
	SourceProvider string

	// SourceVideoID is the canonical provider-native identifier
	// (YouTube video ID when SourceProvider == youtube; ""
	// otherwise). Extracted via pkg/urlutil.ExtractVideoID at
	// plan-build time. Empty string is the canonical zero
	// value for non-YouTube providers AND for YouTube URLs that
	// are not watch pages (channel, playlist, live, embed —
	// see pkg/urlutil::ExtractVideoID for the exact contract).
	SourceVideoID string

	// SourceVersion is a content-derived hash that locks the plan
	// to a specific source snapshot. v1 today (producers don't
	// compute one); future PRs will hash the source bytes.
	SourceVersion string

	// StartSec / EndSec are the canonical clip-window boundaries.
	// Cutters consume them verbatim; retry paths pre/post-compute
	// any drift with the planner's stable permutation.
	StartSec float64
	EndSec   float64

	// Title carries the human-readable clip/timestamp label when the
	// plan originates from explicit clips. Deterministic planner runs
	// leave it empty.
	Title string

	// Description carries the human-readable English summary for the
	// timestamp. Explicit clip payloads can populate it per segment;
	// deterministic planner runs leave it empty.
	Description string

	// Round carries the boxing-style round number when set. Surfaced
	// into ChunkState + ChunkMetadataEntry + pgvector semantic payload
	// (PR-STOCK-TIMESTAMP-CLIPS Front 2, July 2026). Zero is the
	// canonical "not set" value (godlike/07 NO-FAKE-AVAILABILITY —
	// never use -1 or a magic literal). Deterministic planner runs
	// leave it at zero.
	Round int

	// Tags is the free-form per-clip tag list. Carries people, theme,
	// technique — any metadata that should surface in BM25 sparse
	// search. Empty slice = no tags; nil/empty are byte-equivalent
	// for the wire shape.
	Tags []string

	// Category is the content category for this clip (boxing / running
	// etc.). Surfaces in metadata.json + pgvector payload for semantic
	// filtering. Empty = not specified.
	Category string

	// Slug is the explicit operator-supplied Drive folder slug for
	// this clip. Per godlike/07 user-spec directive: Slug is the
	// EXPLICIT OVERRIDE that wins over Title-derived slug in
	// perClipLeafName. Use this when the title contains characters
	// that don't slugify cleanly (e.g. accented Portuguese "veredito"
	// stays verbatim) or when the operator wants a canonical
	// machine-friendly folder name.
	Slug string

	// ParentSlug is the explicit timestamp-group folder slug for
	// expanded 5-second children. When explicit clips are split into
	// child slices, ParentSlug preserves the original parent folder
	// identity while Slug may carry the child-specific timestamp
	// suffix. Nil/empty means "derive from Title / other cascade".
	ParentSlug string

	// OutputLogicalID is the deterministic asset ID the chunk
	// producer will mint. Format: planner:<sha256-prefix>:<index>
	// — opaque hash so callers don't depend on its internal shape.
	// The hash covers the clip window (start/end) so clips of the
	// same source at different timestamps never collide.
	OutputLogicalID string

	// PolicyVersion is the planner policy version at plan-time.
	// Stored on the plan so a future policy upgrade can detect
	// (and re-plan) old plans instead of silently miscomputing.
	PolicyVersion string
}

// ClipPlanner produces a deterministic clip plan from (source, budget).
//
// Contract:
//   - Same input tuple (src, budgetSec, clipDur, policyVer) always
//     produces the same plan slice (same length, same OutputLogicalIDs,
//     same StartSec/EndSec) regardless of clock, process restart,
//     or parallel worker. This replaces the rng-based path that gave
//     a different permutation on every invocation.
//   - Returns nil, ErrPlannerBudgetTooSmall if budget < clipDur so
//     callers can short-circuit on impossible requests.
type ClipPlanner interface {
	Plan(ctx context.Context, src VideoSource, budgetSec int, clipDur int, policyVer string) ([]ClipPlan, error)
}
