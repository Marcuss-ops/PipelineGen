// Package indexing — vlm_aggregator.go owns the [Render] layer of
// the VLM VisualSummary pipeline: the deterministic dedup, cap and
// truncation of VLMInferenceResponse slices into a canonical
// VisualSummary row.
//
// Split rationale (cx0030 build / cx0031 embed / cx0032 render):
//   - frame_sampler.go  : [Build]   — FFmpeg frame extraction.
//   - vlm_client.go     : [Embed]   — Python /vlm/visual-tag
//     sidecar HTTP transport.
//   - vlm_aggregator.go : [Render]  — THIS FILE. Pure function,
//     no I/O, no ports, no log.
//   - visual_summary.go : Orchestrator.
//
// godlike/06 SSOT: this file ALSO is the canonical agg-rules
// reference (the aggregation invariants below). Any change to
// dedup, cap or truncation is a SourceHash substrate change →
// bumps DefaultVLMPreprocessingVersion, NOT this file's behavior.
package indexing

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// AggregateVLMResponses deduplicates and truncates a slice of VLM
// responses into the canonical VisualSummary row fields.
//
// godlike/06 SSOT aggregation rules:
//   - VisibleActions = union/dedup of VisualObjects across frames
//     (capped at detail.MaxVisibleItems=32, deterministic sort order).
//   - VisibleEntities = union/dedup of TextOnScreen across frames
//     (same cap + sort order).
//   - VisualSummaryText = the LONGEST RawDescription across frames
//     (truncated to detail.MaxVisualSummaryChars=512).
//
// Why "longest": empirical inspection of the canonical Python VLM
// responses shows shorter descriptions are partial captions of a
// single frame, while the longest describes the full visual context.
// The longest is most likely the canonical aggregate caption.
func AggregateVLMResponses(frames []*VLMInferenceResponse) (text string, actions []string, entities []string) {
	if len(frames) == 0 {
		return "", nil, nil
	}
	actionSet := make(map[string]struct{})
	entitySet := make(map[string]struct{})
	var longestRaw string
	for _, f := range frames {
		if f == nil {
			continue
		}
		for _, a := range f.VisualObjects {
			a = strings.TrimSpace(a)
			if a != "" {
				actionSet[a] = struct{}{}
			}
		}
		for _, e := range f.TextOnScreen {
			e = strings.TrimSpace(e)
			if e != "" {
				entitySet[e] = struct{}{}
			}
		}
		if len(f.RawDescription) > len(longestRaw) {
			longestRaw = f.RawDescription
		}
	}
	actions = sortedKeys(actionSet)
	if len(actions) > detail.MaxVisibleItems {
		actions = actions[:detail.MaxVisibleItems]
	}
	entities = sortedKeys(entitySet)
	if len(entities) > detail.MaxVisibleItems {
		entities = entities[:detail.MaxVisibleItems]
	}
	if len(longestRaw) > detail.MaxVisualSummaryChars {
		longestRaw = longestRaw[:detail.MaxVisualSummaryChars]
	}
	return longestRaw, actions, entities
}

// sortedKeys returns the keys of a string-set as a sorted []string
// (godlike/06 SSOT deterministic ordering). godlike/07 fail-closed:
// it never returns nil — empty set → empty slice (caller distinguishes
// empty via len, not via nil-vs-non-nil).
//
// kept private + colocated with its sole caller (AggregateVLMResponses)
// to make the dedup substrate readable without bouncing between
// files.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
