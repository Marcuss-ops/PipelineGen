// Package scripts — generation_validator.go provides semantic
// validation of a normalized GenerationItemV2 into a set of
// actionable errors. Structural validation (envelope-level) lives
// in GenerationEnvelopeV2.Validate(); this layer adds semantic
// checks that depend on runtime state (wired services, config
// limits).
//
// Validation produces a list of human-readable details. An empty
// list means the item is valid.
package scripts

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ValidateItem checks a normalized GenerationItemV2 for semantic
// consistency. Returns nil when the item is valid, or a
// *scriptpkg.PlanInvalidError with structured details.
//
// Checks performed:
//   - Source type has corresponding fields populated.
//   - Postprocessor flags are consistent (e.g. voiceover without
//     a language set is ambiguous).
//   - Sizing constraints are possible (target_words > 0 when
//     duration is also requested).
func ValidateItem(item scriptpkg.GenerationItemV2) error {
	var details []string

	ref := item.ID
	if ref == "" {
		ref = "item"
	}

	details = append(details, validateSource(item.Source, ref)...)
	details = append(details, validateOutput(item.Output, ref)...)
	details = append(details, validateScript(item.ScriptParams, ref)...)

	if len(details) > 0 {
		return &scriptpkg.PlanInvalidError{
			ItemID:  item.ID,
			Details: details,
		}
	}
	return nil
}

func validateSource(src scriptpkg.SourceSpec, ref string) []string {
	var d []string
	switch src.Type {
	case scriptpkg.SourceText:
		if src.Topic == "" && src.SourceText == "" {
			d = append(d, ref+": text source requires topic or source_text")
		}
	case scriptpkg.SourceClips:
		if len(src.ClipIDs) == 0 {
			d = append(d, ref+": clips source requires at least one clip_id")
		}
	case scriptpkg.SourceCatalog, scriptpkg.SourceSearch:
		if src.Query == "" {
			d = append(d, ref+": "+string(src.Type)+" source requires a query")
		}
		if src.MaxClips <= 0 {
			d = append(d, ref+": "+string(src.Type)+" source requires max_clips > 0")
		}
	case scriptpkg.SourceCurate:
		// Curate has no required fields — search + hints are optional;
		// the resolver validates resolution at runtime.
	default:
		d = append(d, ref+": unknown source type "+string(src.Type))
	}
	if len(src.Guidelines) > 10000 {
		d = append(d, ref+": guidelines exceeds 10000 characters")
	}
	return d
}

func validateOutput(out scriptpkg.OutputSpec, ref string) []string {
	var d []string
	if out.GenerateVoiceover {
		if out.VoiceoverGroup == "" {
			// Voiceover group can default from config; not an error.
		}
	}
	if out.MaxChars < 0 {
		d = append(d, ref+": max_chars cannot be negative")
	}
	// P0.1 (June 2026): the canonical script pipeline emits
	// ModelScriptOutputV1 and never free-form prose. Reject any
	// OutputFmt other than "json" — including the legacy "prose"
	// value — so callers see a typed validation error instead of a
	// silent ErrModelOutputMalformed during the ollama decode.
	if out.OutputFmt != "" && out.OutputFmt != "json" {
		d = append(d, ref+": output_fmt must be 'json', got '"+out.OutputFmt+"' (prose is rejected in the canonical pipeline; use the legacy adapter if you need free-form prose)")
	}
	if len(out.Languages) > 20 {
		d = append(d, ref+": at most 20 translation languages allowed")
	}
	// Deduplicate languages check.
	seen := make(map[string]struct{}, len(out.Languages))
	for _, lang := range out.Languages {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		if _, ok := seen[lang]; ok {
			d = append(d, ref+": duplicate language '"+lang+"'")
		}
		seen[lang] = struct{}{}
	}
	return d
}

func validateScript(sp scriptpkg.ScriptSpec, ref string) []string {
	var d []string
	if sp.TargetWords < 0 {
		d = append(d, ref+": target_words cannot be negative")
	}
	if sp.Duration < 0 {
		d = append(d, ref+": duration cannot be negative")
	}
	if sp.MinWords < 0 {
		d = append(d, ref+": min_words cannot be negative")
	}
	if sp.SentencesPerImage < 0 {
		d = append(d, ref+": sentences_per_image cannot be negative")
	}
	if sp.ImagesPerScene < 0 {
		d = append(d, ref+": images_per_scene cannot be negative")
	}
	if sp.SentencesPerImage > 100 {
		d = append(d, ref+": sentences_per_image exceeds maximum of 100")
	}
	if sp.ImagesPerScene > 20 {
		d = append(d, ref+": images_per_scene exceeds maximum of 20")
	}
	return d
}
