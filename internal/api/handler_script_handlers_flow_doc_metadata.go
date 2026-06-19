package api

import (
	"encoding/json"
	"strings"
)

// isAliasOf checks if shortName is a substring/alias of longName.
func isAliasOf(shortName, longName string) bool {
	if strings.EqualFold(shortName, longName) {
		return false
	}
	lowerShort := strings.ToLower(strings.TrimSpace(shortName))
	lowerLong := strings.ToLower(strings.TrimSpace(longName))
	return strings.Contains(lowerLong, lowerShort) || strings.Contains(lowerShort, lowerLong)
}

// entityDriveLink returns the full Drive URL for an entity from its EntityImage.
func entityDriveLink(img ScriptEntityImage) string {
	link := strings.TrimSpace(img.DriveLink)
	if link == "" || img.Error != "" {
		return ""
	}
	return link
}

// buildCompactMetadata builds a compact JSON representation of the script metadata.
// Format:
//
//	{
//	  "v": 2,
//	  "title": "...",
//	  "ent": {
//	    "EntityName": ["https://drive.google.com/file/d/...", ["alias"]],
//	    ...
//	  }
//	}
//
// Each entity entry is [driveLink, aliases?]. No type tag (PERSON/GPE).
// Only entities that have a corresponding image in EntityImages are added.
// SpecialNames that are substrings/aliases of an imaged entity are merged
// as aliases rather than appearing as separate entries.
func buildCompactMetadata(title string, insights ScriptInsights) string {
	type compactEntity []any // [driveLink, aliases?]

	out := struct {
		Version int                      `json:"v"`
		Title   string                   `json:"title"`
		Ent     map[string]compactEntity `json:"ent,omitempty"`
	}{
		Version: 2,
		Title:   title,
		Ent:     make(map[string]compactEntity),
	}

	// Collect entity images: full Drive link per entity name
	entityLinks := make(map[string]string)
	for _, img := range insights.EntityImages {
		if img.Error != "" || img.DriveLink == "" {
			continue
		}
		clean := strings.TrimSpace(img.EntityName)
		if clean == "" {
			continue
		}
		if link := entityDriveLink(img); link != "" {
			entityLinks[strings.ToLower(clean)] = link
		}
	}

	// STEP 1: Process entities that HAVE images first (primary entries)
	seen := make(map[string]bool)
	for _, name := range insights.SpecialNames {
		clean := strings.TrimSpace(name)
		if clean == "" {
			continue
		}
		lower := strings.ToLower(clean)
		link, hasImage := entityLinks[lower]
		if !hasImage {
			continue
		}
		if seen[lower] {
			continue
		}
		seen[lower] = true

		var aliases []string
		for _, other := range insights.SpecialNames {
			otherClean := strings.TrimSpace(other)
			if otherClean == "" || strings.EqualFold(otherClean, clean) {
				continue
			}
			if isAliasOf(otherClean, clean) {
				aliases = append(aliases, otherClean)
			}
		}
		if len(aliases) == 0 {
			aliases = nil
		}

		entry := compactEntity{link}
		if len(aliases) > 0 {
			entry = append(entry, aliases)
		}
		out.Ent[clean] = entry
	}

	// STEP 2: Add remaining SpecialNames that are NOT aliases
	for _, name := range insights.SpecialNames {
		clean := strings.TrimSpace(name)
		if clean == "" {
			continue
		}
		lower := strings.ToLower(clean)
		if seen[lower] {
			continue
		}

		isAlias := false
		for existingName := range out.Ent {
			if isAliasOf(clean, existingName) {
				isAlias = true
				break
			}
		}
		if isAlias {
			seen[lower] = true
			continue
		}

		seen[lower] = true
		out.Ent[clean] = compactEntity{""}
	}

	if len(out.Ent) == 0 {
		out.Ent = nil
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

// buildInsightsJSONBlock returns the compact metadata JSON for the Google Doc.
func buildInsightsJSONBlock(title string, insights ScriptInsights) string {
	return buildCompactMetadata(title, insights)
}
