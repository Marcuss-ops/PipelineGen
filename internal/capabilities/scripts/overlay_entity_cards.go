// Package scriptgeneration — overlay_entity_cards.go: the entity-card half
// of the overlay plan derivation (PERSON / ORGANIZATION / LOCATION /
// CONCEPT cards resolved from the certified EntityTimeline).
//
// Extracted 2026-09-12 from overlay_plan.go to keep both halves under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"os"
	"regexp"
	"strings"

	"go.uber.org/zap"

	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// entityCardTemplate reports whether a resolver template is an entity card
// (as opposed to a planner-owned overlay template).
func entityCardTemplate(templateID string) bool {
	switch templateID {
	case "person_default", "org_default", "gpe_default", "concept_default":
		return true
	}
	return false
}

// entityCardKind reports whether an overlay kind is an entity-card kind
// (PERSON / ORGANIZATION / LOCATION / CONCEPT) — the kinds that resolve from
// the EntityTimeline; a bound image is promoted to the image-only capability
// later in the plan projection.
func entityCardKind(kind capabilityoverlay.OverlayKind) bool {
	switch kind {
	case capabilityoverlay.KindEntityCard, capabilityoverlay.KindOrganization, capabilityoverlay.KindLocation, capabilityoverlay.KindConcept:
		return true
	}
	return false
}

// entityCardMediaIndex builds the run-scoped, canonical_entity_id-keyed media
// index from the result's OWN entity-image bindings: every entity-card-kind
// annotation entity with a content-addressed binding (asset id + sha256 +
// url) is indexed so the EntityMediaResolver can pick the best asset for its
// card. The join key is the resolver's CanonicalEntityID when the annotation
// was stamped with one (capabilities/imagesearch), else the deterministic
// derivation from (type, canonical name) — the two agree whenever the
// surface IS the canonical name. Both come from the ONE identity helper
// (annotationCanonicalEntityID), so this index and the overlay-intent/bundle
// joins can never disagree on the spelling. Bindings without a content address are
// deliberately NOT indexed: a card asset must be verifiable, never a bare
// reference.
//
// The second return maps each annotated entity's StableEntityID to its
// canonical id, so a resolver card item (keyed by the same StableEntityID)
// can look up the identity to resolve.
func entityCardMediaIndex(result *GenerateResult, perSceneImages ...bool) (*capabilityentities.EntityMediaResolver, map[string]string) {
	sceneScoped := len(perSceneImages) > 0 && perSceneImages[0]
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
	for i := range result.Scenes {
		indexAnnotations := func(ann *scriptpkg.SceneAnnotations) {
			if ann == nil {
				return
			}
			for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
				if !entityCardKind(capabilityoverlay.EntityTypeToKind(entity.Type)) {
					continue
				}
				binding := entity.Image
				if binding == nil || strings.TrimSpace(binding.AssetID) == "" {
					continue
				}
				canonical := annotationCanonicalEntityID(entity)
				if canonical == "" {
					continue
				}
				stable := annotationStableEntityID(entity)
				lookupID := canonical
				if sceneScoped {
					lookupID = strings.TrimSpace(result.Scenes[i].ID) + "::" + canonical
					name := strings.TrimSpace(entity.CanonicalName)
					if name == "" {
						name = strings.TrimSpace(entity.Text)
					}
					overlayEntityID := capabilityentities.SafeEntityID(name)
					if overlayEntityID == "" {
						overlayEntityID = stable
					}
					canonicalByStable["overlay-"+strings.TrimSpace(result.Scenes[i].ID)+"-"+overlayEntityID] = lookupID
					// Run-level fallback: a scene without its own binding for the
					// same entity still resolves the first verified portrait in
					// scene order instead of degrading to a text-only card.
					if canonicalByStable[stable] == "" {
						canonicalByStable[stable] = lookupID
					}
				} else {
					canonicalByStable[stable] = lookupID
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
	media.SetIndex(index)
	return media, canonicalByStable
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

// capEntityImageOverlays keeps at most max distinct identity-image overlays in
// one run. Items are already in canonical timeline order, so retaining the
// first occurrence is deterministic and preserves the earliest spoken
// identities. Later occurrences of a retained identity do not consume a
// second image slot.
func capEntityImageOverlays(items []capabilityoverlay.OverlayItem, max int, perScene ...bool) []capabilityoverlay.OverlayItem {
	if max <= 0 || len(items) == 0 {
		return items
	}
	seen := make(map[string]struct{}, max)
	seenAssetHashes := make(map[string]struct{}, max)
	perSceneImages := len(perScene) > 0 && perScene[0]
	out := make([]capabilityoverlay.OverlayItem, 0, len(items))
	for _, item := range items {
		if item.Kind != string(capabilityoverlay.KindEntityImage) {
			out = append(out, item)
			continue
		}
		assetKey := ""
		if perSceneImages {
			if len(item.AssetRefs) > 0 {
				assetKey = strings.ToLower(strings.TrimSpace(item.AssetRefs[0].SHA256))
				if assetKey == "" {
					assetKey = strings.ToLower(strings.TrimSpace(item.AssetRefs[0].AssetID))
				}
				if assetKey == "" {
					assetKey = strings.ToLower(strings.TrimSpace(item.AssetRefs[0].URL))
				}
			}
			if assetKey != "" {
				// Reusing a portrait in a later scene is valid: it may need to
				// reappear beside another entity in a co-present sequence. Only
				// collapse duplicate copies inside the same scene.
				if sceneID := strings.TrimSpace(item.SceneID); sceneID != "" {
					assetKey = sceneID + "::" + assetKey
				}
				if _, duplicate := seenAssetHashes[assetKey]; duplicate {
					continue
				}
				seenAssetHashes[assetKey] = struct{}{}
			}
		}
		// Image extraction can assign occurrence or scene scoped IDs. Prefer
		// canonical type/name so shared-entity mode shows an entity once; per-scene
		// mode keys that identity by scene so each occurrence can keep its own
		// image overlay and independently chosen motion.
		identity := ""
		if item.EntityRef != nil {
			identity = strings.TrimSpace(item.EntityRef.CanonicalEntityID)
			if separator := strings.Index(identity, "::"); separator >= 0 {
				identity = identity[separator+2:]
			}
			if identity == "" && strings.TrimSpace(item.EntityRef.Name) != "" && strings.TrimSpace(item.EntityRef.Type) != "" {
				identity = capabilityentities.StableEntityID(item.EntityRef.Type, item.EntityRef.Name)
			}
			if identity == "" {
				identity = strings.TrimSpace(item.EntityRef.EntityID)
			}
		}
		if identity == "" && len(item.AssetRefs) > 0 {
			identity = strings.TrimSpace(item.AssetRefs[0].SHA256)
		}
		if identity == "" {
			identity = strings.TrimSpace(item.EntityID)
		}
		if identity == "" {
			identity = strings.TrimSpace(item.ID)
		}
		if perSceneImages && assetKey != "" && strings.TrimSpace(item.SceneID) != "" {
			identity = strings.TrimSpace(item.SceneID) + "::" + identity
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		if len(seen) >= max {
			continue
		}
		seen[identity] = struct{}{}
		out = append(out, item)
	}
	return out
}

// imageCandidate projects an entity image binding onto the planner's
// ImageCandidate, anchored at the certified occurrence start. The display
// window is DYNAMIC: it derives from the certified spoken window of the
// mention (EntitySpokenWindowDuration: spoken audio + readability hold,
// clamped into [1s, 5s]), so a short spoken name still reads for a full
// second while a longer narration keeps the image up without a fixed reset.
// The spoken occurrence remains the timing authority for when the animation
// enters. The direct PreviewURL (when present) is preferred over the Drive
// view-page link so the compiled layer references a fetchable image. The
// binding's verified content address (SHA256) is carried through so the
// planner's asset ref and the queue manifest stay content-addressed (a
// missing hash would silently drop the asset from the render manifest).
func imageCandidate(binding *scriptpkg.EntityImageBinding, occ *capabilityentities.EntityOccurrence, score float64) capabilityoverlay.ImageCandidate {
	startUS := (occ.AudioStartUS / 1000) * 1000
	durationUS := capabilityentities.EntitySpokenWindowDuration(occ.AudioStartUS, occ.AudioEndUS)
	return capabilityoverlay.ImageCandidate{
		AssetID:    binding.SHA256,
		URL:        entityImageURL(binding),
		LocalPath:  binding.LocalPath,
		SHA256:     binding.SHA256,
		MediaType:  "image",
		StartMs:    startUS / 1000,
		EndMs:      startUS/1000 + durationUS/1000,
		StartUS:    startUS,
		DurationUS: durationUS,
		Score:      score,
	}
}

// overlaySceneInput projects grounded annotations and certified occurrences
// into planner candidates. Optional unspoken annotations never receive timing.
func overlaySceneInput(scene Scene, language, sourceLanguage Language, timing capabilityaudio.SpeechTimingArtifact, timelineStartUS int64, occurrences []capabilityentities.EntityOccurrence, plates capabilityoverlay.PlateResolver) (*capabilityoverlay.SceneInput, error) {
	ann := annotationsForLanguage(scene, language, sourceLanguage)
	if ann == nil {
		return nil, nil
	}
	out := capabilityoverlay.SceneInput{ID: scene.ID}
	locate := func(phrase string) (*capabilityaudio.PhraseTiming, error) {
		return locatePhraseTimingWithEndpointFallback(scene.Index, timelineStartUS, timing, phrase)
	}
	timed := func(p *capabilityaudio.PhraseTiming, score float64) capabilityoverlay.TimedAnnotation {
		return capabilityoverlay.TimedAnnotation{Text: p.Text, StartMs: p.GlobalStartUS / 1000, EndMs: (p.GlobalEndUS + 999) / 1000, StartUS: p.GlobalStartUS, DurationUS: p.GlobalEndUS - p.GlobalStartUS, Score: score}
	}
	for _, span := range ann.ImportantPhrases {
		phrase := strings.TrimSpace(span.Text)
		// A two-word phrase can be a complete, grounded callout; single words
		// remain too fragmentary for this overlay lane.
		if len(strings.Fields(phrase)) < 2 {
			continue
		}
		if p, err := locate(phrase); err == nil {
			out.Phrases = append(out.Phrases, timed(p, span.Score))
		}
	}
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
		kind := capabilityoverlay.EntityTypeToKind(entity.Type)
		if kind == capabilityoverlay.KindBrandText {
			if p, err := locate(entity.CanonicalName); err == nil {
				score := entity.Confidence
				if score <= 0 {
					score = 0.9
				}
				out.BrandTexts = append(out.BrandTexts, timed(p, score))
			}
			continue
		}
		occ := occurrenceFor(occurrences, entity)
		if occ == nil {
			continue
		}
		score := entity.Confidence
		if score <= 0 {
			score = 0.9
		}
		switch kind {
		case capabilityoverlay.KindNumber:
			// Entity extraction occasionally wraps the number in prose on
			// either side ("8 ottobre 2026 per", live C1 "si attesta
			// sull'18"). Clean to the biggest valid numeric/date surface,
			// then re-attach a %/currency suffix the NLP value dropped
			// ("sull'18" from "sull'18%").
			value := recoverNumericSuffix(strings.TrimSpace(scene.Text[language]),
				cleanNumericOverlayValue(entity.CanonicalName), entity.Mentions)
			if value == "" || !numericEntityGroundedForLanguage(scene, language, sourceLanguage, value) {
				continue
			}
			startUS, endUS := occ.AudioStartUS, occ.AudioEndUS
			if value != entity.CanonicalName {
				if p, err := locate(value); err == nil {
					startUS, endUS = p.GlobalStartUS, p.GlobalEndUS
				}
				// locate fails when TTS speaks digits as words ("42%" →
				// "quarantadue"): keep the certified occurrence window — it
				// provably contains the cleaned number (same word span) —
				// instead of dropping a real, grounded value.
			}
			out.Numbers = append(out.Numbers, capabilityoverlay.TimedAnnotation{Text: value, Type: entity.Type, StartMs: startUS / 1000, EndMs: (endUS + 999) / 1000, StartUS: startUS, DurationUS: endUS - startUS, Score: score})
		case capabilityoverlay.KindQuote:
			out.Quotes = append(out.Quotes, capabilityoverlay.TimedAnnotation{Text: entity.CanonicalName, Type: entity.Type, StartMs: occ.AudioStartUS / 1000, EndMs: (occ.AudioEndUS + 999) / 1000, StartUS: occ.AudioStartUS, DurationUS: occ.AudioEndUS - occ.AudioStartUS, Score: score})
		case capabilityoverlay.KindProduct:
			if entity.Image != nil {
				out.Products = append(out.Products, imageCandidate(entity.Image, occ, score))
			}
		case capabilityoverlay.KindLogo:
			if entity.Image == nil {
				if p, err := locate(entity.CanonicalName); err == nil {
					out.BrandTexts = append(out.BrandTexts, timed(p, score))
				}
				continue
			}
			out.Logos = append(out.Logos, imageCandidate(entity.Image, occ, score))
		case capabilityoverlay.KindLocation:
			if entity.Geo == nil {
				continue
			}
			candidate, ok := capabilityoverlay.NewMapCandidate(occ.EntityID, entity.CanonicalName, entity.Geo.Latitude, entity.Geo.Longitude, occ.AudioStartUS, occ.AudioEndUS-occ.AudioStartUS, score, entity.Geo.Scope)
			if ok {
				out.Maps = append(out.Maps, candidate)
			}
		default:
			// Entity cards are resolved independently through their media
			// index; adding the same image here would duplicate rendering.
			continue
		}
	}
	// Recover complete, explicitly spoken dates that the entity annotator
	// missed (notably year-only references). These are still source-grounded
	// and receive timing only when the exact surface is found in word timing.
	if scene.OverlayCategories[scriptpkg.OverlayCategoryNumbers].AsBool() {
		for _, date := range explicitDateSurfaces(scene.Text[language]) {
			duplicate := false
			for i, existing := range out.Numbers {
				if strings.EqualFold(existing.Text, date) {
					duplicate = true
					break
				}
				// Entity extraction can return only "13 agosto" while the
				// source explicitly says "13 agosto 2007". Keep one card and
				// prefer the complete, exactly timed source surface.
				if sameGroundedDate(existing.Text, date) {
					if dateHasYear(date) && !dateHasYear(existing.Text) {
						p, err := locate(date)
						if err == nil {
							full := timed(p, scoreForDate(existing.Score))
							full.Type = "DATE"
							out.Numbers[i] = full
						}
					}
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			p, err := locate(date)
			if err != nil {
				continue
			}
			number := timed(p, 0.8)
			number.Type = "DATE"
			out.Numbers = append(out.Numbers, number)
		}
	}
	if len(out.Phrases)+len(out.Keywords)+len(out.Images)+len(out.Numbers)+len(out.BrandTexts)+len(out.Quotes)+len(out.Products)+len(out.Logos)+len(out.Maps) == 0 {
		return nil, nil
	}
	return &out, nil
}

func sameGroundedDate(left, right string) bool {
	leftParts, rightParts := groundedDateIdentityPattern.FindStringSubmatch(strings.TrimSpace(left)), groundedDateIdentityPattern.FindStringSubmatch(strings.TrimSpace(right))
	if len(leftParts) == 0 || len(rightParts) == 0 {
		return false
	}
	if leftParts[1] != rightParts[1] || !strings.EqualFold(leftParts[2], rightParts[2]) {
		return false
	}
	return leftParts[3] == "" || rightParts[3] == "" || leftParts[3] == rightParts[3]
}

func dateHasYear(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	return len(fields) == 3 && len(fields[2]) == 4
}

func scoreForDate(score float64) float64 {
	if score <= 0 {
		return 0.8
	}
	return score
}

var explicitDatePattern = regexp.MustCompile(`(?i)\b(?:[0-9]{1,2}\s+(?:gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre)\s+[0-9]{4}|[0-9]{4})\b`)
var groundedDateIdentityPattern = regexp.MustCompile(`(?i)^([0-9]{1,2})\s+(gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre)(?:\s+([0-9]{4}))?$`)

func explicitDateSurfaces(text string) []string {
	matches := explicitDatePattern.FindAllString(text, -1)
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		key := strings.ToLower(strings.TrimSpace(match))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(match))
	}
	return out
}

func phrasesAtLeastThreeWords(phrases []capabilityoverlay.TimedAnnotation) []capabilityoverlay.TimedAnnotation {
	filtered := make([]capabilityoverlay.TimedAnnotation, 0, len(phrases))
	for _, phrase := range phrases {
		if len(strings.Fields(strings.TrimSpace(phrase.Text))) >= 3 {
			filtered = append(filtered, phrase)
		}
	}
	return filtered
}

func entityImageURL(binding *scriptpkg.EntityImageBinding) string {
	if binding == nil {
		return ""
	}
	if strings.TrimSpace(binding.PreviewURL) != "" {
		return binding.PreviewURL
	}
	return binding.DriveLink
}

// PhraseAnchoringDiagnostic reports one important-phrase candidate considered
// by the overlay planner together with whether it anchored to the certified
// speech timing. It exists purely for observability: a phrase that fails to
// anchor is dropped silently, which makes "the phrases are not taken"
// impossible to diagnose from logs alone.

// normalizeOverlayText lowercases and collapses whitespace so a phrase span
// and a heavy sentence match regardless of casing or spacing.
func normalizeOverlayText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// applyHeavySentenceBoost raises phrase candidate scores with the Rust
// phrase-impact worker's heavy-sentence importance (goal E2b). A phrase whose
// normalized text is contained in (or contains) a heavy sentence inherits
// max(span score, heavy importance), so the heavy summary directly pilots
// the prominent-motion lane behind PlanInput.HeavyPhrasePriority.
// Scores only rise, never fall: admission stays a restriction, never a
// widening. Matching is source-language text; translated overlays keep their
// annotation scores when nothing matches.
func applyHeavySentenceBoost(in *capabilityoverlay.SceneInput, heavies []scriptpkg.ImportantSentence) {
	if in == nil || len(in.Phrases) == 0 || len(heavies) == 0 {
		return
	}
	type normHeavy struct {
		text       string
		importance float64
	}
	norm := make([]normHeavy, 0, len(heavies))
	for _, h := range heavies {
		t := normalizeOverlayText(h.Text)
		if t == "" || h.Importance <= 0 {
			continue
		}
		norm = append(norm, normHeavy{text: t, importance: h.Importance})
	}
	if len(norm) == 0 {
		return
	}
	for i := range in.Phrases {
		pt := normalizeOverlayText(in.Phrases[i].Text)
		if pt == "" {
			continue
		}
		best := in.Phrases[i].Score
		for _, h := range norm {
			if strings.Contains(h.text, pt) || strings.Contains(pt, h.text) {
				if h.importance > best {
					best = h.importance
				}
			}
		}
		in.Phrases[i].Score = best
	}
}
