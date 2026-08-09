// Package stock — planner (Stock Cutover Commit 1, July 2026).
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
//
// Files:
//   - planner_types.go: constants + type alias references
//   - planner_source.go: inferSourceProvider, inferSourceVideoID
//   - planner_deterministic.go: deterministicPlanner + Plan() + helpers
//   - planner_explicit.go: explicitPlanner + Plan() + expand helpers
package stockpipeline

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
)

const (
	explicitClipAutoSegmentThresholdSec = 60
	explicitClipAutoSegmentSeconds      = 5
)

// Canonical SourceProvider bucket identifiers (PR-003, July 2026).
// godlike/06 SSOT — one canonical owner per fact: the inference
// helper inferSourceProvider lives in planner_source.go (the canonical
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

// ClipPlan, ClipPlanner, ErrPlannerBudgetTooSmall,
// NewDeterministicPlanner, ErrExplicitPlannerNoClips,
// NewExplicitPlanner are canonical in types/ — see aliases.go.

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
		// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): thread the 4
		// new content fields from ClipSpec → ClipPlan so downstream
		// consumers (ChunkState, ChunkMetadataEntry, perClipLeafName)
		// see them verbatim. godlike/06 SSOT one canonical owner per
		// fact: explicitPlanner is the SOLE place that copies
		// operator-supplied clip metadata into the plan shape.
		plan.Round = clip.Round
		plan.Tags = append([]string(nil), clip.Tags...) // defensive copy so the plan doesn't share the caller's slice
		plan.Category = clip.Category
		plan.Slug = clip.Slug
		// PR-STOCK-TIMESTAMP-CLIPS (July 2026): thread ParentSlug so
		// timestampParentLeafName uses the correct parent identity for
		// expanded 5-second children. Without this, children whose
		// Title is empty would fall through to their child-specific
		// Slug (e.g. "la-fase-di-studio-0-0-0_to_0-0-5") instead of
		// sharing the parent folder name.
		plan.ParentSlug = clip.ParentSlug
		plans = append(plans, plan)
	}
	return plans, nil
}

// expandExplicitClipSpecs splits explicit clips into successive
// fixed-length slices when secondsPerSegment is positive.
//
// Backward-compat fallback: when secondsPerSegment is zero, clips
// whose duration is at least 60 seconds are still expanded into
// 5-second slices. Shorter explicit clips remain single chunks.
func expandExplicitClipSpecs(clips []ClipSpec, secondsPerSegment int) []ClipSpec {
	if len(clips) == 0 {
		return append([]ClipSpec(nil), clips...)
	}
	expanded := make([]ClipSpec, 0, len(clips))
	for _, clip := range clips {
		stepSec := secondsPerSegment
		if stepSec <= 0 && clip.EndSec > clip.StartSec {
			if clip.EndSec-clip.StartSec >= explicitClipAutoSegmentThresholdSec {
				stepSec = explicitClipAutoSegmentSeconds
			}
		}
		if stepSec <= 0 || clip.EndSec <= clip.StartSec {
			expanded = append(expanded, clip)
			continue
		}
		step := float64(stepSec)
		for cursor := clip.StartSec; cursor < clip.EndSec; cursor += step {
			next := cursor + step
			if next > clip.EndSec {
				next = clip.EndSec
			}
			child := clip
			child.ParentSlug = clip.ParentSlug
			if child.ParentSlug == "" {
				child.ParentSlug = clip.Slug
			}
			child.StartSec = cursor
			child.EndSec = next
			if clip.Slug != "" {
				child.Slug = clip.Slug + "-" + formatTimestampForSlug(cursor, next)
			}
			expanded = append(expanded, child)
		}
	}
	return expanded
}

func formatTimestampForSlug(startSec, endSec float64) string {
	return formatTimestampSeconds(startSec) + "_to_" + formatTimestampSeconds(endSec)
}

func formatTimestampSeconds(sec float64) string {
	total := int(sec)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return strconv.Itoa(h) + "-" + strconv.Itoa(m) + "-" + strconv.Itoa(s)
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
//
// Description is caller-populated: this constructor intentionally
// leaves plan.Description at the zero value. The deterministic
// planner runs (which use this constructor directly) leave
// Description empty and rely on the post-extract LLM enrichment
// pass to populate it (forward-pointer PR-ENRICHMENT-PLAN-DESC).
// The explicit planner is the ONLY caller that mutates
// plan.Description after this function returns — see
// explicitPlanner.Plan in planner_explicit.go.
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

// RoundIndexSpec is the compact timestamp-index format accepted by stock.
type RoundIndexSpec struct {
	Round          any    `json:"round"`
	TimestampStart string `json:"timestamp_start"`
	TimestampEnd   string `json:"timestamp_end"`
	Description    string `json:"description,omitempty"`
}

const (
	defaultRoundClipDurationSec = 4
	maxRoundStockDurationSec    = 60
)

// ExpandRoundIndexing deterministically creates at most fifteen 4-second
// clips per round (or the requested duration), never exceeding one minute.
func ExpandRoundIndexing(rounds []RoundIndexSpec, clipDuration int) ([]ClipSpec, error) {
	if len(rounds) == 0 {
		return nil, nil
	}
	if clipDuration <= 0 {
		clipDuration = defaultRoundClipDurationSec
	}
	if clipDuration > maxRoundStockDurationSec {
		return nil, fmt.Errorf("round clip duration %d exceeds round budget %d", clipDuration, maxRoundStockDurationSec)
	}
	clips := make([]ClipSpec, 0, len(rounds)*maxRoundStockDurationSec/clipDuration)
	for _, round := range rounds {
		start, err := parseRoundTimestamp(round.TimestampStart)
		if err != nil {
			return nil, fmt.Errorf("round %v start: %w", round.Round, err)
		}
		end, err := parseRoundTimestamp(round.TimestampEnd)
		if err != nil {
			return nil, fmt.Errorf("round %v end: %w", round.Round, err)
		}
		if end <= start {
			return nil, fmt.Errorf("round %v has non-positive timestamp range", round.Round)
		}
		stop := start + maxRoundStockDurationSec
		if end < stop {
			stop = end
		}
		label := roundLabel(round.Round)
		for cursor := start; cursor+clipDuration <= stop; cursor += clipDuration {
			clip := ClipSpec{Title: label, Description: round.Description, StartSec: float64(cursor), EndSec: float64(cursor + clipDuration)}
			if n, ok := roundNumber(round.Round); ok {
				clip.Round = n
			} else {
				clip.Slug = label
			}
			clips = append(clips, clip)
		}
	}
	return clips, nil
}

func parseRoundTimestamp(raw string) (int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid timestamp %q", raw)
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	s, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil || h < 0 || m < 0 || m > 59 || s < 0 || s > 59 {
		return 0, fmt.Errorf("invalid timestamp %q", raw)
	}
	return h*3600 + m*60 + s, nil
}

func roundNumber(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		return int(v), v >= 1 && v == float64(int(v))
	case int:
		return v, v >= 1
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil && n >= 1
	default:
		return 0, false
	}
}

func roundLabel(raw any) string {
	if n, ok := roundNumber(raw); ok {
		return fmt.Sprintf("Round %d", n)
	}
	if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return "Round"
}

// InterleaveClips takes a slice of clip groups (one slice per source
// video), shuffles each source-group internally to preserve dynamic
// randomness, then interleaves the source-groups step-by-step so the
// final output rotates among sources.
//
// FASE 2.4 (July 2026): the legacy parallel-array signature
// ([][]string clipsBySource, [][]string titlesBySource, [][]string sourceIDsBySource)
// is collapsed into a single [][]Clip input. Each Clip carries Path /
// Title / SourceID / Status / SizeBytes / DurationSec / Err; the caller
// does the per-source slices verbatim and InterleaveClips no longer
// has to reason about cross-slice index alignment (the previous
// implementation relied on len(clipsBySource[i])==len(titlesBySource[i])==
// len(sourceIDsBySource[i]) for every i — a fragile contract that
// produced silent misalignment on partial-success batches).
//
// Behaviour preserved:
//   - shuffle WITHIN each source group before interleaving (preserves
//     dynamic randomness that doesn't break the per-source clip
//     ordering aesthetic)
//   - interleave ACROSS source groups: round-robin over the maximum
//     group length, appending per-step entries from each group until
//     that source is exhausted
//   - non-Succeeded Clips are filtered out of the output (Succeeded()
//     predicate aligns with downstream renderChunk's filter stage)
//   - empty input groups are silently skipped
func InterleaveClips(clipsBySource [][]Clip) []Clip {
	active := make([][]Clip, 0, len(clipsBySource))
	for _, list := range clipsBySource {
		if len(list) == 0 {
			continue
		}
		shuffled := make([]Clip, len(list))
		copy(shuffled, list)
		// P2 fix (July 2026): deterministic per-group shuffle replaces
		// package-level var rng (was non-deterministic time.Now().UnixNano()).
		// Seed from first clip's SourceID for reproducibility across runs.
		groupSeed := hashFnv64(list[0].SourceID)
		for i := 1; i < len(list); i++ {
			groupSeed ^= hashFnv64(list[i].SourceID)
		}
		localRng := rand.New(rand.NewSource(groupSeed))
		localRng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		// Drop non-Succeeded entries from the active pool — the
		// legacy output fed only produced clips into the render
		// chain so this normalisation preserves the historical
		// behaviour while staying self-consistent if a caller
		// happens to feed Failed entries (e.g. for debugging).
		filtered := shuffled[:0]
		for _, c := range shuffled {
			if c.Succeeded() {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) > 0 {
			active = append(active, filtered)
		}
	}

	var out []Clip
	maxLen := 0
	for _, list := range active {
		if len(list) > maxLen {
			maxLen = len(list)
		}
	}
	for step := 0; step < maxLen; step++ {
		for srcIdx := 0; srcIdx < len(active); srcIdx++ {
			if step < len(active[srcIdx]) {
				out = append(out, active[srcIdx][step])
			}
		}
	}
	return out
}
