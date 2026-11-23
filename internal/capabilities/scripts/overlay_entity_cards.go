// Package scriptgeneration — overlay_entity_cards.go: the entity-card half of the
// overlay plan derivation. It owns the media index for PERSON / ORGANIZATION /
// LOCATION / CONCEPT cards (resolved from the certified EntityTimeline), the
// bundle-name join that keeps a surname alias from crossing families, and the
// spoken-date grounding that decides whether a date may become a card.

package scriptgeneration

import (
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
	"go.uber.org/zap"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// entityCardKind reports whether an overlay kind is an entity-card kind
// (PERSON / ORGANIZATION / LOCATION / CONCEPT) — the kinds that resolve from
// the EntityTimeline; a bound image is promoted to the image-only capability
// later in the plan projection.
func entityCardKind(kind capmodel.OverlayKind) bool {
	switch kind {
	case capmodel.KindEntityCard, capmodel.KindOrganization, capmodel.KindLocation, capmodel.KindConcept:
		return true
	}
	return false
}

// entityCardMediaIndex builds the run-scoped, canonical_entity_id-keyed media
// index from the result's OWN entity-image bindings: every entity-card-kind
// annotation entity with a content-addressed binding (asset id + sha256 +
// url) is indexed so the EntityMediaResolver can pick the best asset for its
// card. The join key is the resolver's CanonicalEntityID when the annotation
// was supplied by BrainRust, else the deterministic derivation from (type,
// canonical name) — the two agree whenever the
// surface IS the canonical name. Both come from the ONE identity helper
// (annotationCanonicalEntityID), so this index and the overlay-intent/bundle
// joins can never disagree on the spelling. Bindings without a content address are
// deliberately NOT indexed: a card asset must be verifiable, never a bare
// reference.
//
// The second return maps each annotated entity's StableEntityID to its
// canonical id, so a resolver card item (keyed by the same StableEntityID)
// can look up the identity to resolve.
func entityCardMediaIndex(result *GenerateResult, perSceneImages ...overlayPlanImageOptions) (*capabilityentities.EntityMediaResolver, map[string]string) {
	sceneScoped := len(perSceneImages) > 0 && perSceneImages[0].perSceneEntityCards()
	index := capabilityentities.NewEntityMediaIndex()
	media := capabilityentities.NewEntityMediaResolver()
	canonicalByStable := map[string]string{}
	localPathByAsset := map[string]string{}
	for _, segment := range result.Segments {
		candidates := append(append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.Candidates...), segment.Assets.SecondaryImages...)
		for _, candidate := range candidates {
			if assetID := strings.TrimSpace(candidate.AssetID); assetID != "" && usableLocalAssetPath(candidate.LocalPath) {
				localPathByAsset[assetID] = candidate.LocalPath
			}
		}
	}
	// The verified bundle is the single asset source. It is built before the
	// plan, so the planner sees the same bytes the renderer will receive even
	// when annotations were cleared or are only localized aliases.
	catalog := newEntityBundleCatalog(result.SemanticRenderBundle)
	bundleAssets := catalog.enabled()
	for i := range result.Scenes {
		indexAnnotations := func(ann *scriptpkg.SceneAnnotations) {
			if ann == nil {
				return
			}
			for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
				if !entityCardKind(capmodel.EntityTypeToKind(entity.Type)) {
					continue
				}
				binding := entity.Image
				stable := annotationStableEntityID(entity)
				var bundleEntry entityBundleCatalogEntry
				useBundle := false
				if bundleAssets {
					if entry, ok := catalog.assetFor(stable, binding); ok {
						bundleEntry, useBundle = entry, true
					}
				}
				// The bundle is the primary asset source, but an old timeline can
				// still carry a verified annotation binding under a different
				// occurrence id. Never lose that image just because the bundle join
				// could not name it; fall back to the annotation binding.
				if !useBundle && (binding == nil || strings.TrimSpace(binding.AssetID) == "") {
					continue
				}
				canonical := annotationCanonicalEntityID(entity)
				if canonical == "" && useBundle {
					canonical = catalog.canonicalFor(stable)
				}
				if canonical == "" {
					continue
				}
				lookupID := canonical
				if sceneScoped {
					sceneID := strings.TrimSpace(result.Scenes[i].ID)
					lookupID = sceneID + "::" + canonical
					name := strings.TrimSpace(entity.CanonicalName)
					if name == "" {
						name = strings.TrimSpace(entity.Text)
					}
					if alias := overlayDisplayAlias(sceneID, name, stable); alias != "" {
						canonicalByStable[alias] = lookupID
					}
					// Run-level fallback: a scene without its own binding for the
					// same entity still resolves the first verified portrait in
					// scene order instead of degrading to a text-only card.
					if canonicalByStable[stable] == "" {
						canonicalByStable[stable] = lookupID
					}
				} else {
					canonicalByStable[stable] = lookupID
				}
				if useBundle {
					if err := catalog.indexAsset(index, lookupID, bundleEntry, localPathByAsset, entity.Confidence); err != nil {
						logger.Warn("overlay plan: bundle entity asset not indexed (card stays text-only)",
							zap.String("entity", entity.CanonicalName),
							zap.String("canonical_entity_id", lookupID),
							zap.Error(err),
						)
					}
					continue
				}
				url := entityImageURL(binding)
				if url == "" || strings.TrimSpace(binding.SHA256) == "" {
					continue
				}
				localPath := binding.LocalPath
				if !usableLocalAssetPath(localPath) {
					localPath = localPathByAsset[binding.AssetID]
				}
				score := entity.Confidence
				if score <= 0 {
					score = 0.9
				}
				// Fail-open on an invalid record: the card stays text-only rather
				// than failing the whole overlay plan over one unverifiable asset.
				// Fail-open must still be visible: a registry rejecting every
				// binding would silently degrade every card to text-only.
				if err := index.IndexForCanonicalID(lookupID, capabilityentities.EntityAsset{
					AssetID: binding.AssetID, AssetType: entityImageAssetType(binding),
					SHA256: binding.SHA256, StorageURL: url,
					LocalPath:    localPath,
					QualityScore: score, Source: binding.Source,
				}); err != nil {
					logger.Warn("overlay plan: entity card asset not indexed (card stays text-only)",
						zap.String("entity", entity.CanonicalName),
						zap.String("canonical_entity_id", lookupID),
						zap.Error(err),
					)
				}
			}
		}
		// Localized annotations inherit the source image binding, but their
		// translated/inflected surface produces a different stable occurrence
		// ID. Index them too so localized entity-card items can join the same
		// canonical asset without re-resolving or inventing an identity.
		indexAnnotations(result.Scenes[i].Annotations)
		for _, localized := range result.Scenes[i].LocalizedAnnotations {
			indexAnnotations(localized)
		}
	}
	// Bundle assets without a surviving annotation still belong to the
	// certified render contract. Index them under the identity the bundle
	// carries, so a plan compiled after annotation cleanup does not lose an
	// image that the bundle already verified.
	if catalog.enabled() {
		for _, entry := range catalog.entries {
			lookupID := entry.lookupID(sceneScoped)
			if lookupID == "" {
				continue
			}
			if canonicalByStable[entry.stable] == "" {
				canonicalByStable[entry.stable] = lookupID
			}
			// Resolver cards can expose only the display name (no canonical
			// id). Index the bundle's own entity spelling under a name key so
			// a verified AP/agency asset is not lost when its occurrence id
			// differs from the resolver card id.
			if entity, ok := catalog.entityByID[entry.stable]; ok {
				for _, name := range []string{entity.CanonicalText, entity.Text} {
					if key := bundleNameKey(entity.Type, name); key != "" {
						if canonicalByStable[key] == "" {
							canonicalByStable[key] = lookupID
						}
					}
				}
			}
			if err := catalog.indexAsset(index, lookupID, entry, localPathByAsset, 0.9); err != nil {
				logger.Warn("overlay plan: bundle-only entity asset not indexed (card stays text-only)",
					zap.String("entity_id", entry.stable),
					zap.String("canonical_entity_id", lookupID),
					zap.Error(err),
				)
			}
		}
	}
	indexUniqueBundleSurnameAliases(catalog, canonicalByStable)
	media.SetIndex(index)
	return media, canonicalByStable
}

// indexUniqueBundleSurnameAliases adds one last identity projection for
// translated cards that keep only the family name. A source portrait of
// "Mike Tyson" must still render when the localized plan (or its NER) names
// the card "Tyson": the canonical id join then has nothing to match, and the
// verified bytes were silently left unplanned. Only the last token of a
// verified PERSON display name is indexed, and only when that token is unique
// across the bundle: an ambiguous surname is never guessed onto a card. Exact
// display names keep precedence.
func indexUniqueBundleSurnameAliases(catalog *entityBundleCatalog, canonicalByStable map[string]string) {
	if catalog == nil || !catalog.enabled() || canonicalByStable == nil {
		return
	}
	aliases := make(map[string]string)
	ambiguous := make(map[string]struct{})
	for stable, entity := range catalog.entityByID {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON") {
			continue
		}
		tokens := strings.Fields(firstNonEmpty(entity.CanonicalText, entity.Text))
		if len(tokens) < 2 {
			continue
		}
		key := bundleNameKey(entity.Type, tokens[len(tokens)-1])
		lookupID := catalog.canonicalFor(stable)
		if key == "" || lookupID == "" {
			continue
		}
		if existing, ok := aliases[key]; ok && existing != lookupID {
			ambiguous[key] = struct{}{}
			continue
		}
		aliases[key] = lookupID
	}
	for key := range ambiguous {
		delete(aliases, key)
	}
	for key, lookupID := range aliases {
		if canonicalByStable[key] == "" {
			canonicalByStable[key] = lookupID
		}
	}
}

// overlayDisplayAlias is the SINGLE owner of the legacy
// "overlay-<scene>-<slug>" key. It exists because a resolver card that predates
// the stable occurrence id still spells its entity this way, and the media
// index has to find the canonical id that card already carries.
//
// It is an alias, never an identity: it is not written to a plan, an audit
// record, a bundle or a job payload, no canonical_entity_id ever holds it, and
// no component may compare two of them as the same entity. The scene segment
// alone makes a shared identity impossible — the same entity in two scenes has
// two different aliases. TestOverlayDisplayAliasIsNeverAnIdentity and
// TestOverlayPlanProductionHasSingleAuthority keep this the only producer.
func overlayDisplayAlias(sceneID, name, fallback string) string {
	slug := capabilityentities.SafeEntityID(strings.TrimSpace(name))
	if slug == "" {
		slug = strings.TrimSpace(fallback)
	}
	if slug == "" {
		return ""
	}
	return "overlay-" + strings.TrimSpace(sceneID) + "-" + slug
}

// bundleNameKey is the typed display-name join key. The entity family is part
// of the key on purpose: a surname alias is a projection of a PERSON portrait
// (Desiree Washington → "washington"), and the GPE of the same surface
// (Washington) is a DIFFERENT identity. Keeping the family in the key means a
// place can never inherit a person's portrait through a shared spelling, while
// a translated card that keeps only the family name still joins its own
// family. Non-PERSON types index their full display name, so a verified place
// or organisation portrait still resolves by name when the occurrence id
// differs.
func bundleNameKey(entityType, name string) string {
	key := entitycatalog.NormalizeVerifiedAssetKey(name)
	if key == "" {
		return ""
	}
	family := entitytype.Fold(entityType)
	if family == "" {
		return ""
	}
	return "bundle-name:" + family + ":" + key
}

func usableLocalAssetPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// entityImageAssetType keeps the semantic path extension aligned with the
// verified bytes. Drive download URLs commonly have no suffix, so relying on
// filepath.Ext(URL) would manufacture a misleading .png path for a JPEG.
// The worker still sniffs the materialized bytes and can correct an honest
// provider MIME mismatch before Chronon.
func entityImageAssetType(binding *scriptpkg.EntityImageBinding) string {
	if binding != nil {
		switch strings.ToLower(strings.TrimSpace(strings.SplitN(binding.MediaType, ";", 2)[0])) {
		case "image/png":
			return "photo"
		case "image/jpeg", "image/jpg":
			return "photo_jpeg"
		}
		url := strings.ToLower(strings.TrimSpace(entityImageURL(binding)))
		if strings.HasSuffix(url, ".png") {
			return "photo"
		}
		if strings.HasSuffix(url, ".jpg") || strings.HasSuffix(url, ".jpeg") {
			return "photo_jpeg"
		}
	}
	// Internet image acquisition currently verifies and persists photographs
	// as JPEG in the live catalog. Keep the fallback explicit and deterministic
	// for legacy bindings that predate media_type.
	return "photo_jpeg"
}

var explicitDatePattern = regexp.MustCompile(`(?i)\b(?:[0-9]{1,2}\s+(?:gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre)\s+[0-9]{4}|[0-9]{4})\b`)
var groundedDateIdentityPattern = regexp.MustCompile(`(?i)^([0-9]{1,2})\s+(gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre)(?:\s+([0-9]{4}))?$`)

func entityImageURL(binding *scriptpkg.EntityImageBinding) string {
	if binding == nil {
		return ""
	}
	// The durableDrive copy is the only location whose bytes are stable for
	// the content address. Provider URLs can return different bytes later,
	// which makes RenderingGen reject the prefetch with HTTP 422.
	if id := strings.TrimSpace(binding.DriveFileID); id != "" {
		return "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(id)
	}
	if id := driveFileID(binding.DriveLink); id != "" {
		return "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(id)
	}
	if preview := strings.TrimSpace(binding.PreviewURL); preview != "" {
		return preview
	}
	return strings.TrimSpace(binding.DriveLink)
}

// PhraseAnchoringDiagnostic reports one important-phrase candidate considered
// by the overlay planner together with whether it anchored to the certified
// speech timing. It exists purely for observability: a phrase that fails to
// anchor is dropped silently, which makes "the phrases are not taken"
// impossible to diagnose from logs alone.
