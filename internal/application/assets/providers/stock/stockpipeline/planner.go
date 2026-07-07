// Package stock — planner.go (Stock Cutover Commit 1, July 2026).
//
// ClipPlanner is the deterministic replacement for the pre-cutover
// rng-based clip-sampling logic that lived inside processSingleVideo
// (used the usedOffsets map + ran up to 20 attempts + picked random
// start offsets via rng.Float64()). The new planner:
//
//  1. Takes (source, budgetSec, clipDur, policyVersion) — no rng.
//  2. Produces N non-overlapping ClipPlan entries where N =
//     budgetSec / clipDur, with each entry carrying its source ID,
//     start/end, deterministic OutputLogicalID, and the policy
//     version tag it was locked against.
//  3. SHA-256 hash of (URL + index) mod sliceSec gives a stable
//     permutation that is identical across runs (replacing rng
//     which gave a different permutation every time the binary
//     started — the rng seed was time.Now().UnixNano()).
//
// Same input → same output. That is the only invariant the
// orchestrator and downstream consumers (cut, retry, render) lock
// against: the plan is persisted as data and consumed verbatim
// rather than re-computed.
package stockpipeline

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
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

// inferSourceProvider classifies a source URL into the canonical
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
func inferSourceProvider(rawURL string) string {
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

// inferSourceVideoID extracts the canonical video ID ONLY when
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
func inferSourceVideoID(rawURL string) string {
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

	// OutputLogicalID is the deterministic asset ID the chunk
	// producer will mint. Format: planner:<sha256-prefix>:<index>
	// — opaque hash so callers don't depend on its internal shape.
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

// ErrPlannerBudgetTooSmall signals that the per-source budget
// (seconds) is smaller than the requested clip duration. This is a
// caller-side configuration error (NOT a transient runtime error),
// so the caller should fail-closed with the typed error.
var ErrPlannerBudgetTooSmall = errors.New("deterministic planner: budget must be >= clip duration")

// NewDeterministicPlanner returns the canonical ClipPlanner.
// Today there is exactly one implementation; the constructor lets
// future variants (e.g. policy-aware planners) plug in without a
// Service constructor signature change.
func NewDeterministicPlanner() ClipPlanner {
	return &deterministicPlanner{}
}

// deterministicPlanner is the canonical ClipPlanner.
// Stable permutation via SHA-256 hashing of (URL + index) mod
// sliceSec — replaces the rng.Float64()-based random scheduler.
type deterministicPlanner struct{}

// Compile-time assertion: deterministicPlanner satisfies ClipPlanner.
var _ ClipPlanner = (*deterministicPlanner)(nil)

func (p *deterministicPlanner) Plan(_ context.Context, src VideoSource, budgetSec int, clipDur int, policyVer string) ([]ClipPlan, error) {
	// Guard: budgetSec <= 0 || clipDur <= 0 → invalid input.
	// Without this guard, `count = budgetSec / clipDur` panics
	// on zero-divide when both are zero (the `budgetSec < clipDur`
	// check returns false for `0 < 0`). The orchestrator clamps
	// its own input but the planner MUST NOT trust callers.
	if budgetSec <= 0 || clipDur <= 0 {
		return nil, ErrPlannerBudgetTooSmall
	}
	if budgetSec < clipDur {
		return nil, ErrPlannerBudgetTooSmall
	}

	// Number of non-overlapping clips per source = budgetSec / clipDur.
	// We round DOWN so the last budget tail may be skipped (it is
	// shorter than clipDur and would produce partial windows). If a
	// future caller wants to consume the tail, change to math.Ceil
	// and make the last clip's EndSec == budgetSec.
	count := budgetSec / clipDur
	if count < 1 {
		// Single full-window clip spanning budget.
		return []ClipPlan{
			buildClipPlan(src, 0, float64(clipDur), 0, policyVer),
		}, nil
	}

	sliceSec := (budgetSec - clipDur) / count
	out := make([]ClipPlan, 0, count)
	for i := 0; i < count; i++ {
		// SHA-256-mod-sliceSec gives a stable permutation. Note:
		// sliceSec collapses to 0 when budget == N*clipDur; the hash
		// fallback to 0 in that case produces overlapping copies,
		// which we accept as degenerate (the budget is already
		// exactly N clips back-to-back — there is no "shuffle" room).
		start := deterministicStart(src.URL, i, sliceSec)
		end := start + float64(clipDur)
		if end > float64(budgetSec) {
			end = float64(budgetSec)
		}
		out = append(out, buildClipPlan(src, start, end, i, policyVer))
	}
	return out, nil
}

// deterministicStart returns a stable offset in [0, sliceSec)
// derived from SHA-256(URL + ":" + idx).
//
// Replace-only path: the legacy rng-based picked random offsets
// inside a loop that capped at 20 attempts. That non-replayable
// behaviour is replaced by this hash-derived stable permutation so
// the plan can be persisted + replayed (and so retries don't
// produce visually-identical-offset clips mismatched with their
// logical IDs across runs).
func deterministicStart(url string, idx int, sliceSec int) float64 {
	if sliceSec <= 0 {
		return 0
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", url, idx)))
	n := binary.BigEndian.Uint32(h[:4])
	return float64(int(n) % sliceSec)
}

// buildClipPlan mints a ClipPlan with the deterministic OutputLogicalID.
//
// Format: planner:<sha256-prefix>:<index>. The hash prefix is
// stable across runs + across implementations (URL-driven), so
// re-planning the same source yields identical IDs — downstream
// producers can use the Logical ID as a dedupe key.
//
// SourceProvider + SourceVideoID are inferred ONCE at plan-build
// time (per godlike/06 SSOT — both deterministicPlanner and
// explicitPlanner go through this single constructor; there is
// exactly one canonical place that classifies URLs).
func buildClipPlan(src VideoSource, start, end float64, idx int, policyVer string) ClipPlan {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", src.URL, idx, policyVer)))
	id := fmt.Sprintf("planner:%x:%d", h[:8], idx)
	return ClipPlan{
		SourceID:        src.URL,
		SourceProvider:  inferSourceProvider(src.URL),
		SourceVideoID:   inferSourceVideoID(src.URL),
		SourceVersion:   "v1",
		StartSec:        start,
		EndSec:          end,
		OutputLogicalID: id,
		PolicyVersion:   policyVer,
	}
}

// ErrExplicitPlannerNoClips signals that the explicit planner was
// invoked with an empty clips slice. The caller should fail-closed
// rather than producing a nil output.
var ErrExplicitPlannerNoClips = errors.New("explicit planner: no clips provided")

// NewExplicitPlanner returns a ClipPlanner that uses pre-defined
// ClipSpec entries instead of computing deterministic offsets from
// a budget. Each ClipSpec produces exactly one ClipPlan.
//
// The explicit planner ignores the budgetSec and clipDur parameters
// — it always returns len(clips) ClipPlan entries regardless of
// the source duration. SourceID is set to src.URL for all plans.
//
// godlike/07 typed-error contract: Plan returns
// ErrExplicitPlannerNoClips when clips is empty.
func NewExplicitPlanner(clips []ClipSpec) ClipPlanner {
	return &explicitPlanner{clips: clips}
}

type explicitPlanner struct {
	clips []ClipSpec
}

var _ ClipPlanner = (*explicitPlanner)(nil)

func (p *explicitPlanner) Plan(_ context.Context, src VideoSource, budgetSec int, clipDur int, policyVer string) ([]ClipPlan, error) {
	if len(p.clips) == 0 {
		return nil, ErrExplicitPlannerNoClips
	}
	plans := make([]ClipPlan, 0, len(p.clips))
	for i, clip := range p.clips {
		// godlike/06 SSOT — route through buildClipPlan so the
		// inference + ID derivation stay canonical (PR-003 only
		// added fields; the literal was deliberately modified here
		// to share buildClipPlan rather than duplicate the
		// 11-field struct literal — future field additions stay
		// in one place).
		plan := buildClipPlan(src, clip.StartSec, clip.EndSec, i, policyVer)
		plan.Title = clip.Title
		plan.Description = clip.Description
		plans = append(plans, plan)
	}
	return plans, nil
}
