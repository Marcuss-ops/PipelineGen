package script

import "strings"

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
