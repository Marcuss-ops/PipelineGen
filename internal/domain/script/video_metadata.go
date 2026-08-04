package script

import "strings"

// VideoMetadata holds YouTube-style metadata for a script result.
//
// TranslationStatus (PR 0.6 close-out, June 2026) is the explicit marker
// for whether the Title / Description / Tags fields are realised
// translations. Values:
//
//	"translated"    — Title/Description/Tags are populated from a
//	                   successful translator call (or directly from the
//	                   English source for Language=="en"). This is the
//	                   "happy path" — fields reflect their canonical
//	                   translation.
//	"untranslated"  — Translator returned an error or produced an empty
//	                   string. Title/Description/Tags are explicitly
//	                   cleared (empty/empty/nil) so callers cannot
//	                   mistakenly surface the original `Title` or
//	                   `enDesc` text as a "successful translation".
//	                 Per godlike/07 (no-fake-availability), the
//	                   silent-success fallback was removed; this status
//	                   is the only legal alternative to "translated".
//	""              — Backward-compat: legacy callers that pre-date the
//	                   field. Treated as "translated" for reading
//	                   purposes (the field pre-existed as a populated
//	                   payload).
//
// P0.18 (successive wave) will replace the per-item string status with
// a richer TranslationError field; until then this string sentinel is
// the contract every reader consumes.
type VideoMetadata struct {
	Language          string   `json:"language"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Tags              []string `json:"tags"`
	TranslationStatus string   `json:"translation_status,omitempty"`
}

func (m *VideoMetadata) HasContent() bool {
	if m == nil {
		return false
	}

	if strings.TrimSpace(m.Title) != "" {
		return true
	}

	if strings.TrimSpace(m.Description) != "" {
		return true
	}

	for _, tag := range m.Tags {
		if strings.TrimSpace(tag) != "" {
			return true
		}
	}

	return false
}

func CloneVideoMetadata(input *VideoMetadata) *VideoMetadata {
	if input == nil {
		return nil
	}

	clone := *input
	clone.Title = strings.TrimSpace(clone.Title)
	clone.Description = strings.TrimSpace(clone.Description)

	clone.Tags = make([]string, 0, len(input.Tags))
	for _, raw := range input.Tags {
		tag := strings.TrimSpace(raw)
		if tag != "" {
			clone.Tags = append(clone.Tags, tag)
		}
	}

	return &clone
}
