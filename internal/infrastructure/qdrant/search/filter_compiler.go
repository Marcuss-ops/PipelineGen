// PR 5 (June 2026, fix/qdrant-tenant-scope) — filter_compiler.go is
// the SINGLE canonical builder for Qdrant search filters. Both
// search_adapter.go and clip_search_adapter.go call
// CompileQdrantFilter; no other site hand-rolls a `must: [...]`
// slice.
//
// Why a single compiler (rather than split search/clip-search):
//
//   - The QDRANT-004 verdict §8 observed that clip_search_adapter
//     silently dropped the workspace must-clause because the
//     curate path owns no caller-side workspace scope. Splitting
//     the filter logic across two files makes a future drift
//     (e.g. "let's also filter by media_type here for curate")
//     drift-prone: one site will pick up the change and the
//     other won't, and the divergence is invisible until a
//     cross-tenant leak shows up in production.
//   - CompileQdrantFilter is the only function in this package
//     that reads SearchScope.AssetFilter. Defence-in-depth:
//     scope.IsSystem=true is the ONLY escape hatch from the
//     workspace must-clause; everything else fails closed.
//
// Backward compatibility: the previous buildLifecycleAwareFilter
// lives as a Deprecated thin shim that delegates here. New code
// must call CompileQdrantFilter directly. The shim will be removed
// in a follow-up PR once all call sites migrate (track under
// "filter-compiler-migration" in the runbook).
package search

import (
	"fmt"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// CompileQdrantFilter builds the canonical Qdrant filter body for
// the user's request.
//
// Invariants:
//
//  1. WorkspaceID must-clause is ALWAYS present when scope.IsSystem
//     is false. An empty scope.WorkspaceID with IsSystem=false
//     returns an error — callers must either set WorkspaceID or
//     mark the request as a system/admin path. The error is the
//     fail-closed contract that mirrors mediasearch.Service::Search
//     rejection of ErrMissingWorkspace.
//  2. LifecycleState allow-list is ALWAYS present (defaults to
//     {"ACTIVE"} when AssetFilter.LifecycleState is empty). Pre-PR5
//     this rule was implicit in search_adapter.go but absent from
//     clip_search_adapter.go; the missing clause was the source of
//     the curate-path "no fake availability" regression documented
//     in the verdict §8. Centralising it here makes the invariant
//     impossible to forget on either path.
//  3. Optional fields (Source/Category/MediaType/Language) drop
//     out of the filter entirely when empty — no match clauses
//     with zero values that would force a full-collection scan.
//
// Returned Filter is the canonical `must: [...]` map shape that
// `internal/qdrant.schema.SearchRequest.Filter` serialises into the Qdrant
// REST body. Tests pin the byte shape — if Qdrant ever rejects
// the projection, the regression is loud at unit-test time, not
// at first production search.
func CompileQdrantFilter(scope appsearch.SearchScope, filter appsearch.AssetFilter) (map[string]any, error) {
	if !scope.IsSystem {
		if scope.WorkspaceID == "" {
			return nil, fmt.Errorf("qdrant.CompileQdrantFilter: SearchScope.WorkspaceID is required (set IsSystem=true for admin/reconcile/snapshot paths)")
		}
		if scope.WorkspaceID == "default" {
			return nil, fmt.Errorf(`qdrant.CompileQdrantFilter: SearchScope.WorkspaceID is the reserved "default" sentinel; set a real workspace or IsSystem=true`)
		}
	}

	must := make([]map[string]any, 0, 6)

	// 1. Workspace isolation clause (PR 5 §8): always-on unless
	//    scope.IsSystem marks this as a cross-workspace admin call.
	if !scope.IsSystem {
		must = append(must, map[string]any{
			"key":   "workspace_id",
			"match": map[string]any{"value": scope.WorkspaceID},
		})
	}

	// 2. Caller-side equality filters (empty → drop, no zero-value
	//    must-clauses).
	if filter.Source != "" {
		must = append(must, matchClause("source", filter.Source))
	}
	if filter.Category != "" {
		must = append(must, matchClause("category", filter.Category))
	}
	if filter.MediaType != "" {
		must = append(must, matchClause("media_type", filter.MediaType))
	}
	if filter.Language != "" {
		must = append(must, matchClause("language", filter.Language))
	}

	// 2a. PR-FOLDER-FILTER (July 2026) folder restrict. Emits the
	//     canonical Qdrant `normalized_group` must-clause — NOT
	//     `folder`, `macro_topic`, or `blueprint`. The key name
	//     matches the writer side (clip_metadata_writer_payload.go)
	//     + the IndexDocument airlock (payload_mapper.go).
	//
	//     Empty FolderNormalizedGroup means "no folder filter";
	//     callers MUST resolve the alias upstream via
	//     clipfolder.FolderAliasResolver.Resolve() (godlike/07
	//     NO-FAKE-AVAILABILITY: this filter never invents a default).
	//
	//     The lowercase normalised key (e.g. "boxe", "hiphop") is
	//     stored on media_assets.metadata_json.normalized_group
	//     AND on Qdrant payload; the lowercased form is the only
	//     wire-compatible representation — uppercase variants
	//     ("Boxe") do not match because Qdrant's `match.value`
	//     is byte-equal.
	if filter.FolderNormalizedGroup != "" {
		must = append(must, matchClause("normalized_group", filter.FolderNormalizedGroup))
	}

	// 3. Lifecycle allow-list — states are alternatives, not cumulative
	//    requirements. Keep equality predicates in `must`, and encode
	//    searchable lifecycle states using Qdrant's structured
	//    `min_should` object. The REST API rejects a numeric
	//    `min_should`; it requires `{conditions, min_count}`.
	//    The default remains ACTIVE for direct callers.
	lifecycle := filter.LifecycleState
	if len(lifecycle) == 0 {
		lifecycle = []string{"ACTIVE"}
	}

	return map[string]any{
		"must": must,
		"min_should": map[string]any{
			"conditions": lifecycleClauses(lifecycle),
			"min_count":  1,
		},
	}, nil
}

// matchClause is the canonical wire shape for an equality predicate.
// Centralised so the key naming stays consistent across every filter
// built by this file — if a future PR renames a payload key, the
// migration lives here once.
func matchClause(key, value string) map[string]any {
	return map[string]any{
		"key":   key,
		"match": map[string]any{"value": value},
	}
}

// lifecycleClauses expands an allow-list of states into one condition
// per state for Qdrant's structured min_should object. Qdrant requires
// at least min_count of these alternatives to match, so ACTIVE/PUBLISHED
// behaves as lifecycle IN (ACTIVE, PUBLISHED) without an AND-only must
// clause. Future "include soft-deleted" admin paths can pass
// {"ACTIVE", "DELETE_PENDING"} without code changes here.
func lifecycleClauses(states []string) []map[string]any {
	out := make([]map[string]any, 0, len(states))
	for _, s := range states {
		out = append(out, matchClause("lifecycle_state", s))
	}
	return out
}
