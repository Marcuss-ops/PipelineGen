package scriptdocs

import (
	"fmt"
	"strings"
)

// Validate checks ScriptDocRequest validity.
func (r *ScriptDocRequest) Validate() error {
	if strings.TrimSpace(r.Topic) == "" {
		return fmt.Errorf("topic is required")
	}

	if r.Duration == 0 {
		r.Duration = DefaultDuration
	}
	if r.Duration < MinDuration || r.Duration > 600 {
		return fmt.Errorf("duration must be between %d and 600 seconds", MinDuration)
	}

	if len(r.Languages) == 0 {
		r.Languages = []string{"it"}
	}
	if len(r.Languages) > MaxLanguages {
		return fmt.Errorf("maximum %d languages allowed", MaxLanguages)
	}

	for _, lang := range r.Languages {
		if _, ok := LanguageInfo[lang]; !ok {
			return fmt.Errorf("unsupported language: %s (supported: it, en, es, fr, de, pt, ro)", lang)
		}
	}

	if r.Template == "" {
		r.Template = TemplateDocumentary
	}
	validTemplates := map[string]bool{
		TemplateDocumentary:  true,
		TemplateStorytelling: true,
		TemplateTop10:        true,
		TemplateBiography:    true,
	}
	if !validTemplates[r.Template] {
		return fmt.Errorf("invalid template: %s (valid: documentary, storytelling, top10, biography)", r.Template)
	}

	r.AssociationMode = normalizeAssociationMode(r.AssociationMode)
	validModes := map[string]bool{
		AssociationModeDefault:     true,
		AssociationModeFullArtlist: true,
	}
	if !validModes[r.AssociationMode] {
		return fmt.Errorf("invalid association_mode: %s (valid: default, fullartlist)", r.AssociationMode)
	}

	return nil
}
