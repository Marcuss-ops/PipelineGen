// Package scriptgeneration — overlay_scene_images.go: every image binding the
// plan attaches to a scene. It owns the scene-image resolution (content-addressed
// digests, local paths, preflight refs) and the cross-language reuse that copies
// a verified entity portrait from the source annotations onto their localized
// counterparts. Both are media projections onto a plan that is already sealed:
// nothing here decides WHICH image, only whether the bytes the plan names are
// the bytes the render can fetch.
package overlayplan

import (
	"encoding/hex"
	"log"
	"net/url"
	"os"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	cryptodigest "github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// sceneEntityImageHashes returns content hashes already used by entity cards.
// Generic scene stills must use a different source image so the same portrait
// is not replayed immediately after its captioned entity card.
func sceneEntityImageHashes(result *contract.GenerateResult) map[string]struct{} {
	hashes := make(map[string]struct{})
	if result == nil {
		return hashes
	}
	for _, scene := range result.Scenes {
		if scene.Annotations == nil {
			continue
		}
		entities := append(append([]scriptpkg.AnnotatedEntity(nil), scene.Annotations.PrimaryEntities...), scene.Annotations.SecondaryEntities...)
		for _, entity := range entities {
			if entity.Image == nil {
				continue
			}
			if hash := strings.ToLower(strings.TrimSpace(entity.Image.SHA256)); hash != "" {
				hashes[hash] = struct{}{}
			}
		}
	}
	return hashes
}

// SceneImageCandidate projects one already-materialized still image onto its
// owning scene. Scene images are independent of entity cards: recurring people
// can keep their canonical portrait while each scene gets its own contextual
// visual. Timing is anchored at the certified scene start, never estimated
// from the narration text.
func SceneImageCandidate(result *contract.GenerateResult, sceneID string, startUS int64, excludedHashes map[string]struct{}, sceneEndUS int64) (capabilityoverlay.ImageCandidate, bool) {
	candidates := SceneImageCandidates(result, sceneID, startUS, excludedHashes, sceneEndUS, 1)
	if len(candidates) == 0 {
		return capabilityoverlay.ImageCandidate{}, false
	}
	return candidates[0], true
}

// SceneImageCandidates projects distinct contextual stills onto one scene.
// When a scene has enough certified duration, later stills run sequentially
// after the first one, preserving the replace-only image timeline contract.
func SceneImageCandidates(result *contract.GenerateResult, sceneID string, startUS int64, excludedHashes map[string]struct{}, sceneEndUS int64, limit int) []capabilityoverlay.ImageCandidate {
	if result == nil || strings.TrimSpace(sceneID) == "" || startUS < 0 {
		return nil
	}
	if limit <= 0 {
		return nil
	}
	localHashes := make(map[string]struct{}, len(excludedHashes)+limit)
	for hash := range excludedHashes {
		localHashes[hash] = struct{}{}
	}
	var out []capabilityoverlay.ImageCandidate
	// Per-scene decision counters. They explain an image-free scene with
	// numbers (R23: 482 materialized, 1 planned) instead of requiring a
	// re-run with ad-hoc instrumentation.
	segmentCandidates, readyCandidates, excludedByHash := 0, 0, 0
	logSelection := func() []capabilityoverlay.ImageCandidate {
		log.Printf("scene images: scene_id=%s segment_candidates=%d ready=%d excluded_by_hash=%d limit=%d emitted=%d",
			strings.TrimSpace(sceneID), segmentCandidates, readyCandidates, excludedByHash, limit, len(out))
		return out
	}
	nextStartUS := ((startUS / 1000) * 1000) + capabilityoverlay.MaxImageOverlayDurationMS*1000
	for _, pin := range result.OverlayImages {
		if !strings.EqualFold(strings.TrimSpace(pin.SceneID), strings.TrimSpace(sceneID)) {
			continue
		}
		digest := strings.ToLower(strings.TrimSpace(pin.SHA256))
		path := strings.TrimSpace(pin.LocalPath)
		if strings.TrimSpace(pin.AssetID) == "" || len(digest) != 64 || path == "" {
			continue
		}
		if _, err := hex.DecodeString(digest); err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if cryptodigest.SHA256Bytes(data) != digest {
			continue
		}
		if _, duplicate := localHashes[digest]; duplicate {
			continue
		}
		durationUS := int64(capabilityoverlay.MaxImageOverlayDurationMS * 1000)
		if sceneEndUS > 0 {
			if remainingUS := sceneEndUS - nextStartUS; remainingUS <= 0 {
				break
			} else if remainingUS < durationUS {
				durationUS = remainingUS
			}
		}
		out = append(out, capabilityoverlay.ImageCandidate{
			AssetID: digest, LocalPath: path, SHA256: digest, MediaType: strings.TrimSpace(pin.MIMEType),
			StartMs: nextStartUS / 1000, EndMs: (nextStartUS + durationUS + 999) / 1000,
			StartUS: nextStartUS, DurationUS: durationUS, Score: 1, Pinned: true,
		})
		localHashes[digest] = struct{}{}
		if durationUS < int64(capabilityoverlay.MaxImageOverlayDurationMS*1000) || len(out) >= limit {
			return logSelection()
		}
		nextStartUS += durationUS
	}
	for _, segment := range result.Segments {
		if segment.SceneID != sceneID && segment.SegmentID != sceneID {
			continue
		}
		candidates := append(append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.SecondaryImages...), segment.Assets.Candidates...)
		seen := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			segmentCandidates++
			assetID := strings.TrimSpace(candidate.AssetID)
			digest := strings.TrimSpace(candidate.ContentSHA256)
			if _, duplicate := seen[assetID]; duplicate {
				continue
			}
			seen[assetID] = struct{}{}
			if !SceneImageCandidateReady(candidate, digest) {
				continue
			}
			readyCandidates++
			digest = strings.ToLower(digest)
			if _, duplicate := localHashes[digest]; duplicate {
				excludedByHash++
				continue
			}
			// Prefer the durable Drive copy: the provider URL is not stable for
			// a content-addressed asset and can return different bytes later,
			// failing the renderer's prefetch with HTTP 422.
			resourceURL := ""
			if id := driveFileID(candidate.DriveLink); id != "" {
				resourceURL = "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(id)
			}
			if resourceURL == "" {
				resourceURL = strings.TrimSpace(candidate.DriveLink)
			}
			if resourceURL == "" {
				resourceURL = strings.TrimSpace(candidate.SourceURL)
			}
			if resourceURL == "" {
				resourceURL = strings.TrimSpace(candidate.PreviewURL)
			}
			// Entity cards keep the opening beat. Context images start after the
			// five-second card window, then run sequentially without overlap.
			durationUS := int64(capabilityoverlay.MaxImageOverlayDurationMS * 1000)
			// A scene image starts after the opening entity-card window. Short
			// scenes may not have a full image interval left, so end it exactly
			// at the owning scene boundary instead of failing timeline preflight.
			if sceneEndUS > 0 {
				if remainingUS := sceneEndUS - nextStartUS; remainingUS <= 0 {
					break
				} else if remainingUS < durationUS {
					durationUS = remainingUS
				}
			}
			out = append(out, capabilityoverlay.ImageCandidate{
				AssetID: digest, URL: resourceURL, LocalPath: strings.TrimSpace(candidate.LocalPath),
				SHA256: digest, MediaType: strings.TrimSpace(candidate.MIMEType),
				StartMs: nextStartUS / 1000, EndMs: (nextStartUS + durationUS + 999) / 1000,
				StartUS: nextStartUS, DurationUS: durationUS,
				Score: 1,
			})
			localHashes[digest] = struct{}{}
			if durationUS < int64(capabilityoverlay.MaxImageOverlayDurationMS*1000) {
				break
			}
			if len(out) >= limit {
				return logSelection()
			}
			nextStartUS += durationUS
		}
		return logSelection()
	}
	return logSelection()
}

// EntityImageCardReservation counts the scene's unique image-bearing entity
// cards. Rust admits base candidates (generic stills and maps) before it merges
// entity cards, so a scene full of generic stills silently loses every entity
// image to the per-scene ceiling. Reserving their slots here keeps the generic
// lane from consuming the budget the certified portraits need.
func EntityImageCardReservation(result *contract.GenerateResult, sceneID string) int {
	if result == nil || strings.TrimSpace(sceneID) == "" {
		return 0
	}
	seen := make(map[string]struct{})
	for _, scene := range result.Scenes {
		if !strings.EqualFold(strings.TrimSpace(scene.ID), strings.TrimSpace(sceneID)) || scene.Annotations == nil {
			continue
		}
		entities := append(append([]scriptpkg.AnnotatedEntity(nil), scene.Annotations.PrimaryEntities...), scene.Annotations.SecondaryEntities...)
		for _, entity := range entities {
			if entity.Image == nil {
				continue
			}
			switch capmodel.EntityTypeToKind(entity.Type) {
			case capmodel.KindEntityCard, capmodel.KindOrganization, capmodel.KindLocation, capmodel.KindConcept:
			default:
				continue
			}
			name := strings.TrimSpace(entity.CanonicalName)
			if name == "" {
				name = strings.TrimSpace(entity.Text)
			}
			if name == "" {
				continue
			}
			seen[strings.ToLower(name)] = struct{}{}
		}
	}
	return len(seen)
}

// GenericSceneImageLimit returns how many generic stills one scene may offer
// without evicting its certified entity images from the shared per-scene
// overlay budget. With no image-bearing entities the certified generic limit
// is unchanged.
func GenericSceneImageLimit(result *contract.GenerateResult, sceneID string, maxOverlaysPerScene int) int {
	limit := MaxGenericSceneImagesPerScene
	if maxOverlaysPerScene <= 0 {
		return limit
	}
	reserved := EntityImageCardReservation(result, sceneID)
	if reserved <= 0 {
		return limit
	}
	available := maxOverlaysPerScene - reserved
	if available < 0 {
		available = 0
	}
	if available < limit {
		limit = available
	}
	return limit
}

func SceneImageCandidateReady(candidate scriptpkg.SegmentAssetCandidate, digest string) bool {
	// IndexStatus is a post-persistence search-index lifecycle signal. The
	// current run already owns verified bytes, a stable hash and a durable
	// Drive/local reference, so waiting for asynchronous INDEXED here creates
	// a scene-order race and drops otherwise renderable images.
	if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) ||
		strings.TrimSpace(candidate.AssetID) == "" || candidate.AcquisitionStatus != scriptpkg.VidRushStatusAcquired ||
		candidate.VerificationStatus != scriptpkg.VidRushStatusVerified ||
		candidate.PersistenceStatus != scriptpkg.VidRushStatusPersisted ||
		len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	return strings.TrimSpace(candidate.LocalPath) != "" || strings.TrimSpace(candidate.DriveLink) != "" ||
		strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != ""
}

// ── Cross-language identity media reuse (merged from
// overlay_localized_identity_images.go, OVL-03) ──
//
// A translated plan is compiled after the source media stage, but the localized
// annotations are produced by the translated-NLP pass, which can run BEFORE the
// media stage writes its bindings. The localized surfaces then keep Image nil,
// the per-language semantic render bundle carries no entity asset, and every
// translated entity card plans as text-only even though the portrait was
// acquired, verified and published for the source language; the final-job image
// preflight correctly refuses that plan.
//
// This pass is the missing join: an image of a person does not depend on the
// language the card is displayed in. It copies only VERIFIED, content-addressed
// bindings (64-hex SHA plus a fetchable location) from the source annotations
// onto the localized annotations that name the same identity. Identity is joined
// by canonical id first, then stable id, then a unique family name — the shape a
// translated surface often keeps ("Tyson" for "Mike Tyson"). An ambiguous
// surname is never guessed, and a localized binding that already carries
// verified bytes is never replaced.

func canHydrateEntityImage(binding *scriptpkg.EntityImageBinding) bool {
	return binding != nil && overlayLooksLikeSHA256(binding.SHA256) && EntityImageURL(binding) != ""
}

// HydrateLocalizedEntityImages returns the number of localized entities that
// received a verified source binding.
func HydrateLocalizedEntityImages(result *contract.GenerateResult, language contract.Language) int {
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
			canonical := strings.TrimSpace(AnnotationCanonicalEntityID(entity))
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
	if canonical := strings.TrimSpace(AnnotationCanonicalEntityID(entity)); canonical != "" {
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
