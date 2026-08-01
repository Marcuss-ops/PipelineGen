package script

import "strings"

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

// validateGenerationSourceClips validates a clips-source item.
func validateGenerationSourceClips(item GenerationItemV2, ref string) error {
	if len(item.Source.ClipIDs) == 0 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": clips source requires at least one clip_id",
			},
		}
	}
	if empty := firstEmpty(item.Source.ClipIDs); empty != -1 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": clip_ids cannot be empty or whitespace-only",
			},
		}
	}
	if dup := firstDuplicate(item.Source.ClipIDs); dup != "" {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": duplicate clip_id " + dup,
			},
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
