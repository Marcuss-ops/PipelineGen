package config

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// LanguageSpecSlice is the YAML-facing carrier for
// MultilingualConfig.Languages. It accepts BOTH the
// legacy CSV-shaped YAML
//
//	languages: [it, en, es, ...]
//
// (auto-promoted to enabled+translate+tts on every entry) AND
// the typed struct-list shape
//
//	languages:
//	  - {code: it, enabled: true, translate_clips: true, generate_tts: false}
//	  - {code: en, enabled: true, translate_clips: true, generate_tts: true}
//
// (preserved verbatim). godlike/06 SSOT: this is the SINGLE
// canonical decoder for cfg.MultilingualConfig.Languages.
//
// PR-CATALOG-MULTILINGUA step 3 (July 2026): introduced alongside
// the domain/asset.LanguageRegistry SSOT. Legacy pre-step-3
// configs that carried `materialize_languages:` have been
// retired; operators must use the typed `languages:` list
// (or the legacy []string shape, which is auto-promoted).
type LanguageSpecSlice []asset.LanguageSpec

// UnmarshalYAML handles both []string (legacy) and []LanguageSpec
// (new) shapes. The struct-list shape is attempted first; on
// success the slice is set verbatim. On any error from the
// struct list, the []string shape is attempted and each code
// is auto-promoted to a fully-enabled spec.
func (l *LanguageSpecSlice) UnmarshalYAML(node *yaml.Node) error {
	// Try the typed struct-list shape first. An empty YAML
	// list decodes to a nil slice without error, which is
	// fine (we'll fall through to the string-list pass and
	// also get an empty list there). The struct-list path
	// wins for non-empty typed YAMLs.
	var structs []asset.LanguageSpec
	if err := node.Decode(&structs); err == nil && len(structs) > 0 {
		*l = structs
		return nil
	}
	// Try the legacy []string shape. yaml.v3 will refuse a
	// map node into a []string, so a non-list YAML surfaces
	// a decode error here (caller bug).
	var codes []string
	if err := node.Decode(&codes); err != nil {
		return fmt.Errorf("multilingual.languages: must be []string (legacy) or []LanguageSpec (typed): %w", err)
	}
	for _, c := range codes {
		*l = append(*l, asset.LanguageSpec{
			Code:           c,
			Enabled:        true,
			TranslateClips: true,
			GenerateTTS:    true,
		})
	}
	return nil
}
