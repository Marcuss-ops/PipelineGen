// overlay_localized_identity_images.go — cross-language identity media reuse.
//
// A translated plan is compiled after the source media stage, but the localized
// annotations are produced by the translated-NLP pass, which can run BEFORE the
// media stage writes its bindings. The localized surfaces then keep Image nil,
// the per-language semantic render bundle is built from those annotations and
// carries no entity asset, and every translated entity card plans as text-only
// even though the portrait was acquired, verified and published for the source
// language. The final-job image preflight correctly refuses that plan.
//
// This pass is the missing join: an image of a person does not depend on the
// language the card is displayed in. It copies only VERIFIED, content-addressed
// bindings (64-hex SHA plus a fetchable location) from the source annotations
// onto the localized annotations that name the same identity. Identity is
// joined by canonical id first, then stable id, then a unique family name —
// the shape a translated surface often keeps ("Tyson" for "Mike Tyson"). An
// ambiguous surname is never guessed, and a localized binding that already
// carries verified bytes is never replaced.
package scriptgeneration

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func canHydrateEntityImage(binding *scriptpkg.EntityImageBinding) bool {
	return binding != nil && overlayLooksLikeSHA256(binding.SHA256) && entityImageURL(binding) != ""
}

// hydrateLocalizedEntityImages returns the number of localized entities that
// received a verified source binding.
func hydrateLocalizedEntityImages(result *GenerateResult, language Language) int {
	if result == nil || strings.TrimSpace(string(language)) == "" ||
		strings.EqualFold(strings.TrimSpace(string(result.SourceLanguage)), strings.TrimSpace(string(language))) {
		return 0
	}
	byCanonical := make(map[string]scriptpkg.EntityImageBinding)
	byStable := make(map[string]scriptpkg.EntityImageBinding)
	surnameOwners := make(map[string]map[string]struct{})
	surnameBinding := make(map[string]scriptpkg.EntityImageBinding)
	for i := range result.Scenes {
		annotations := result.Scenes[i].Annotations
		if annotations == nil {
			continue
		}
		entities := append(append([]scriptpkg.AnnotatedEntity(nil), annotations.PrimaryEntities...), annotations.SecondaryEntities...)
		for _, entity := range entities {
			if !canHydrateEntityImage(entity.Image) {
				continue
			}
			canonical := strings.TrimSpace(annotationCanonicalEntityID(entity))
			stable := strings.TrimSpace(annotationStableEntityID(entity))
			if canonical != "" {
				byCanonical[canonical] = *entity.Image
			}
			if stable != "" {
				byStable[stable] = *entity.Image
			}
			name := strings.Fields(firstNonEmpty(entity.CanonicalName, entity.Text))
			if len(name) < 2 {
				continue
			}
			owner := canonical
			if owner == "" {
				owner = stable
			}
			if owner == "" {
				continue
			}
			surname := strings.ToLower(name[len(name)-1])
			if surnameOwners[surname] == nil {
				surnameOwners[surname] = make(map[string]struct{}, 1)
			}
			surnameOwners[surname][owner] = struct{}{}
			surnameBinding[surname] = *entity.Image
		}
	}
	for surname, owners := range surnameOwners {
		if len(owners) != 1 {
			delete(surnameBinding, surname)
		}
	}

	hydrated := 0
	for i := range result.Scenes {
		localized := result.Scenes[i].LocalizedAnnotations[language]
		if localized == nil {
			continue
		}
		for _, list := range []*[]scriptpkg.AnnotatedEntity{&localized.PrimaryEntities, &localized.SecondaryEntities} {
			for index := range *list {
				entity := &(*list)[index]
				if canHydrateEntityImage(entity.Image) {
					continue
				}
				binding, ok := sourceImageBindingByAlias(*entity, byCanonical, byStable, surnameBinding)
				if !ok {
					continue
				}
				entity.Image = &binding
				hydrated++
			}
		}
	}
	return hydrated
}

func sourceImageBindingByAlias(
	entity scriptpkg.AnnotatedEntity,
	byCanonical, byStable map[string]scriptpkg.EntityImageBinding,
	surnameBinding map[string]scriptpkg.EntityImageBinding,
) (scriptpkg.EntityImageBinding, bool) {
	if canonical := strings.TrimSpace(annotationCanonicalEntityID(entity)); canonical != "" {
		if binding, ok := byCanonical[canonical]; ok {
			return binding, true
		}
	}
	if stable := strings.TrimSpace(annotationStableEntityID(entity)); stable != "" {
		if binding, ok := byStable[stable]; ok {
			return binding, true
		}
	}
	name := strings.Fields(firstNonEmpty(entity.CanonicalName, entity.Text))
	if len(name) == 1 {
		if binding, ok := surnameBinding[strings.ToLower(name[0])]; ok {
			return binding, true
		}
	}
	return scriptpkg.EntityImageBinding{}, false
}
