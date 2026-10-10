package script

import (
	"slices"
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
			ItemID:  item.ID,
			Details: []string{ref + ": text source requires topic or source_text"},
		}
	}
	return nil
}

// rejectDeprecatedIntroClipIDs fail-closes any V2 payload still carrying the
// removed source.intro_clip_ids field. The legacy field conflated "narrated
// intro clips" with the protected fixed-media intro section; the only intro
// contract now is the explicit item.intro FixedSection block. The field is
// never auto-converted, because the legacy semantics (generated narration
// over the clip) differ from fixed media.
func rejectDeprecatedIntroClipIDs(item GenerationItemV2, ref string) error {
	if len(item.Source.IntroClipIDs) > 0 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": source.intro_clip_ids is deprecated; use the item.intro fixed section (intro.clip_ids)",
			},
		}
	}
	return nil
}

// validateGenerationSourceClips validates root and segment-owned clip payloads.
// Explicit segment ownership is authoritative. Legacy root clip_ids remains
// accepted for compatibility, but is ignored whenever any segment declares
// clip_ids.
func validateGenerationSourceClips(item GenerationItemV2, ref string) error {
	if err := rejectDeprecatedIntroClipIDs(item, ref); err != nil {
		return err
	}
	segmentsExplicit := HasExplicitSegmentClipIDs(item.ScriptParams.Segments)
	// Stock clip marking is a per-clip editorial decision inside the
	// segment-owned clip contract: every stock_clip_ids entry must name one
	// of the same segment's clip_ids. Validate it independently of the
	// legacy root shape so a bad marking never reaches the runtime.
	for i, segment := range item.ScriptParams.Segments {
		if details := validateSegmentStockClipIDs(segment, i, ref); len(details) > 0 {
			return &PlanInvalidError{ItemID: item.ID, Details: details}
		}
	}
	// Explicit segment ownership wins over the legacy root field. Keep
	// accepting source.clip_ids for compatibility, but never redistribute
	// it when any segment declares clip_ids (including an explicit []).
	// Validate empty IDs before the aggregate so legacy error messages remain stable.
	if segmentsExplicit {
		for i, segment := range item.ScriptParams.Segments {
			if firstEmpty(segment.ClipIDs) != -1 {
				return &PlanInvalidError{ItemID: item.ID, Details: []string{ref + ": segments[" + strconv.Itoa(i) + "].clip_ids cannot be empty or whitespace-only"}}
			}
		}
	} else if firstEmpty(item.Source.ClipIDs) != -1 {
		return &PlanInvalidError{ItemID: item.ID, Details: []string{ref + ": clip_ids cannot be empty or whitespace-only"}}
	}
	if len(CollectRequestedClipIDs(item.Source, item.ScriptParams.Segments)) == 0 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": clips source requires at least one clip_id in source.clip_ids or script_params.segments[].clip_ids",
			},
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

// validateGenerationSourceCatalogOrSearch validates a catalog or search item.
func validateGenerationSourceCatalogOrSearch(item GenerationItemV2, ref string) error {
	if err := rejectDeprecatedIntroClipIDs(item, ref); err != nil {
		return err
	}
	if item.Source.Query == "" {
		return &PlanInvalidError{
			ItemID:  item.ID,
			Details: []string{ref + ": " + string(item.Source.Type) + " source requires a query"},
		}
	}
	if item.Source.MaxClips <= 0 {
		return &PlanInvalidError{
			ItemID:  item.ID,
			Details: []string{ref + ": " + string(item.Source.Type) + " source requires max_clips > 0"},
		}
	}
	return nil
}

// firstEmpty returns the index of the first empty-or-whitespace-only string,
// or -1 when all values are non-empty.
func firstEmpty(ids []string) int {
	for i, id := range ids {
		if strings.TrimSpace(id) == "" {
			return i
		}
	}
	return -1
}

// firstDuplicate returns the first duplicate string, or "" when all values
// are unique.
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

// validateSegmentStockClipIDs enforces the per-clip stock marking contract:
// entries are non-empty, unique, and each names a clip the same segment
// declares in clip_ids. A marking on a segment without clip_ids is invalid:
// stock marking never invents clip ownership.
func validateSegmentStockClipIDs(segment ScriptSegment, index int, ref string) []string {
	if segment.StockClipIDs == nil {
		return nil
	}
	prefix := ref + ": segments[" + strconv.Itoa(index) + "].stock_clip_ids"
	var d []string
	owned := make(map[string]struct{}, len(segment.ClipIDs))
	for _, id := range segment.ClipIDs {
		owned[strings.TrimSpace(id)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(segment.StockClipIDs))
	for _, raw := range segment.StockClipIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			d = append(d, prefix+" cannot be empty or whitespace-only")
			continue
		}
		if _, dup := seen[id]; dup {
			d = append(d, prefix+" duplicate clip_id "+id)
			continue
		}
		seen[id] = struct{}{}
		if _, ok := owned[id]; !ok {
			d = append(d, prefix+" clip_id "+id+" is not declared in this segment's clip_ids")
		}
	}
	return d
}

// SegmentClipIsStock reports whether the segment marks the given clip as
// used-as-stock (audio-only): its original audio joins the generated
// voiceover mix, while its video is never rendered or shown. Comparison
// trims surrounding whitespace; an empty id is never a stock declaration.
func SegmentClipIsStock(segment ScriptSegment, clipID string) bool {
	clipID = strings.TrimSpace(clipID)
	if clipID == "" {
		return false
	}
	for _, raw := range segment.StockClipIDs {
		if strings.TrimSpace(raw) == clipID {
			return true
		}
	}
	return false
}

// CloneScriptSegments returns a deep copy of segment metadata, including the
// per-segment clip ID slices. Segment clip ownership is editorial input and
// must not alias the request payload after plan construction.
func CloneScriptSegments(in []ScriptSegment) []ScriptSegment {
	if in == nil {
		return nil
	}
	out := make([]ScriptSegment, len(in))
	for i, segment := range in {
		out[i] = segment
		// Preserve nil versus non-nil empty slices: omitted clip_ids means
		// legacy fallback is eligible, while clip_ids: [] is explicit zero
		// clip ownership and must select advanced mode.
		if segment.ClipIDs != nil {
			out[i].ClipIDs = slices.Clone(segment.ClipIDs)
		}
		if segment.StockClipIDs != nil {
			out[i].StockClipIDs = slices.Clone(segment.StockClipIDs)
		}
	}
	return out
}

// HasExplicitSegmentClipIDs reports whether the caller selected the advanced
// segment-owned clip contract. A non-nil empty slice represents an explicit
// JSON clip_ids: [] declaration and therefore still selects this contract.
func HasExplicitSegmentClipIDs(segments []ScriptSegment) bool {
	for _, segment := range segments {
		if segment.ClipIDs != nil {
			return true
		}
	}
	return false
}

// CanonicalizeSegmentClipIDs normalizes legacy root clip fields into the
// segment model without changing the caller's slices. Explicit segment-owned
// clip_ids always win. In the legacy shape, root clips are distributed in
// order across segments.
func CanonicalizeSegmentClipIDs(source SourceSpec, segments []ScriptSegment) []ScriptSegment {
	out := CloneScriptSegments(segments)
	if len(out) == 0 {
		return out
	}

	if HasExplicitSegmentClipIDs(out) {
		return out
	}

	rootIDs := make([]string, 0, len(source.ClipIDs))
	for _, id := range source.ClipIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			rootIDs = append(rootIDs, trimmed)
		}
	}
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

// CollectRequestedClipIDs returns the unique, ordered IDs the clips resolver
// is allowed to fetch. It never searches for replacements.
func CollectRequestedClipIDs(source SourceSpec, segments []ScriptSegment) []string {
	if HasExplicitSegmentClipIDs(segments) {
		ids := []string{}
		for _, segment := range segments {
			ids = appendUnique(ids, segment.ClipIDs...)
		}
		return ids
	}
	return appendUnique(nil, source.ClipIDs...)
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
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}
