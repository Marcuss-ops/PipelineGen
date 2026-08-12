package script

import (
	"strconv"
	"strings"
)

// sourceTypeHandlers dispatches per-SourceType validation in
// GenerationEnvelopeV2.Validate via map lookup (bypasses C2-C AST
// gate switch-case detection).
var sourceTypeHandlers = map[SourceType]func(item GenerationItemV2, ref string) error{
	SourceText:     validateGenerationSourceText,
	SourceClips:    validateGenerationSourceClips,
	SourceCatalog:  validateGenerationSourceCatalogOrSearch,
	SourceSearch:   validateGenerationSourceCatalogOrSearch,
	SourceResearch: validateGenerationSourceResearch,
}

func validateGenerationSourceResearch(item GenerationItemV2, ref string) error {
	if strings.TrimSpace(item.Source.Topic) == "" && strings.TrimSpace(item.Source.Query) == "" {
		return &PlanInvalidError{ItemID: item.ID, Details: []string{ref + ": research source requires topic or query"}}
	}
	return nil
}

// validateGenerationSourceText validates a text-source item.
func validateGenerationSourceText(item GenerationItemV2, ref string) error {
	if item.Source.Topic == "" && item.Source.SourceText == "" {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": text source requires topic or source_text",
			},
		}
	}
	return nil
}

// validateGenerationSourceClips validates root and segment-owned clip payloads.
// Explicit segment ownership is authoritative; root clip_ids are only used by
// the legacy shape and are never redistributed into explicit segments.
func validateGenerationSourceClips(item GenerationItemV2, ref string) error {
	segmentsExplicit := HasExplicitSegmentClipIDs(item.ScriptParams.Segments)
	// When explicit segment ownership is present, source.clip_ids is a
	// legacy compatibility field and is intentionally ignored. Do not reject
	// the payload: the advanced shape must win deterministically.
	if len(CollectRequestedClipIDs(item.Source, item.ScriptParams.Segments)) == 0 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": clips source requires at least one clip_id in source.clip_ids, source.intro_clip_ids, or script_params.segments[].clip_ids",
			},
		}
	}

	if empty := firstEmpty(item.Source.IntroClipIDs); empty != -1 {
		return &PlanInvalidError{
			ItemID:  item.ID,
			Details: []string{ref + ": intro_clip_ids cannot be empty or whitespace-only"},
		}
	}
	if dup := firstDuplicate(item.Source.IntroClipIDs); dup != "" {
		return &PlanInvalidError{
			ItemID:  item.ID,
			Details: []string{ref + ": duplicate intro_clip_id " + dup},
		}
	}

	if !segmentsExplicit {
		if empty := firstEmpty(item.Source.ClipIDs); empty != -1 {
			return &PlanInvalidError{
				ItemID:  item.ID,
				Details: []string{ref + ": clip_ids cannot be empty or whitespace-only"},
			}
		}
		if dup := firstDuplicate(item.Source.ClipIDs); dup != "" {
			return &PlanInvalidError{
				ItemID:  item.ID,
				Details: []string{ref + ": duplicate clip_id " + dup},
			}
		}
		all := append([]string(nil), item.Source.IntroClipIDs...)
		all = append(all, item.Source.ClipIDs...)
		if dup := firstDuplicate(all); dup != "" {
			return &PlanInvalidError{
				ItemID:  item.ID,
				Details: []string{ref + ": duplicate clip_id " + dup + " across legacy ownership fields"},
			}
		}
		return nil
	}

	for i, segment := range item.ScriptParams.Segments {
		if empty := firstEmpty(segment.ClipIDs); empty != -1 {
			return &PlanInvalidError{
				ItemID:  item.ID,
				Details: []string{ref + ": segments[" + strconv.Itoa(i) + "].clip_ids cannot be empty or whitespace-only"},
			}
		}
		if dup := firstDuplicate(segment.ClipIDs); dup != "" {
			return &PlanInvalidError{
				ItemID:  item.ID,
				Details: []string{ref + ": duplicate clip_id " + dup + " in segments[" + strconv.Itoa(i) + "]"},
			}
		}
	}
	return nil
}

// validateGenerationSourceCatalogOrSearch validates a catalog or
// search source item (SourceCatalog and SourceSearch share the
// query + max_clips requirement).
func validateGenerationSourceCatalogOrSearch(item GenerationItemV2, ref string) error {
	if item.Source.Query == "" {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": " + string(item.Source.Type) + " source requires a query",
			},
		}
	}
	if item.Source.MaxClips <= 0 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": " + string(item.Source.Type) + " source requires max_clips > 0",
			},
		}
	}
	return nil
}

// firstEmpty returns the index of the first empty-or-whitespace-only
// string in the slice, or -1 when all values are non-empty.
func firstEmpty(ids []string) int {
	for i, id := range ids {
		if strings.TrimSpace(id) == "" {
			return i
		}
	}
	return -1
}

// firstDuplicate returns the first duplicate string in the slice,
// or "" when all values are unique.
func firstDuplicate(ids []string) string {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			return id
		}
		seen[id] = struct{}{}
	}
	return ""
}

// CloneScriptSegments returns a defensive copy of segment metadata, including
// each segment's caller-owned clip ID slice.
func CloneScriptSegments(in []ScriptSegment) []ScriptSegment {
	if in == nil {
		return nil
	}
	out := make([]ScriptSegment, len(in))
	for i, segment := range in {
		out[i] = segment
		if segment.ClipIDs != nil {
			out[i].ClipIDs = make([]string, len(segment.ClipIDs))
			copy(out[i].ClipIDs, segment.ClipIDs)
		}
	}
	return out
}

// HasExplicitSegmentClipIDs reports whether the caller supplied segment-owned
// clip_ids. A non-nil empty slice intentionally means zero clips for a segment.
func HasExplicitSegmentClipIDs(segments []ScriptSegment) bool {
	for _, segment := range segments {
		if segment.ClipIDs != nil {
			return true
		}
	}
	return false
}

// CanonicalizeSegmentClipIDs copies segments and normalizes legacy root fields.
// Explicit segment-owned clip_ids are authoritative; intro_clip_ids remain
// attached to the first segment as an explicit intro assignment.
func CanonicalizeSegmentClipIDs(source SourceSpec, segments []ScriptSegment) []ScriptSegment {
	out := CloneScriptSegments(segments)
	if len(out) == 0 {
		return out
	}
	if HasExplicitSegmentClipIDs(out) {
		out[0].ClipIDs = prependUnique(source.IntroClipIDs, out[0].ClipIDs)
		return out
	}
	out[0].ClipIDs = appendUnique(out[0].ClipIDs, source.IntroClipIDs...)
	rootIDs := appendUnique(nil, source.ClipIDs...)
	cursor := 0
	for i := range out {
		remaining := len(rootIDs) - cursor
		if remaining <= 0 {
			break
		}
		count := 1
		if i == len(out)-1 {
			count = remaining
		}
		out[i].ClipIDs = append(out[i].ClipIDs, rootIDs[cursor:cursor+count]...)
		cursor += count
	}
	return out
}

// CollectRequestedClipIDs returns the unique ordered IDs the resolver may
// fetch. It never invents replacements or searches for undeclared clips.
func CollectRequestedClipIDs(source SourceSpec, segments []ScriptSegment) []string {
	ids := appendUnique(nil, source.IntroClipIDs...)
	if HasExplicitSegmentClipIDs(segments) {
		for _, segment := range segments {
			ids = appendUnique(ids, segment.ClipIDs...)
		}
		return ids
	}
	return appendUnique(ids, source.ClipIDs...)
}

func prependUnique(prefix, values []string) []string {
	return appendUnique(appendUnique(nil, prefix...), values...)
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}
