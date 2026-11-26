// overlay_plan_v2_envelope.go owns the run-level envelope projection of the
// v2 request: identity, canvas, clips, seal/background, budgets, media,
// entity timeline and the extractive timing/category filters. The split from
// overlay_plan_v2_request.go is by DOMAIN: the request file builds the request,
// this file projects the run-level envelope the compiler receives.
package overlayplan

import (
	"encoding/json"
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/color"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func overlayPlanIdentity(planID, videoID, projectID, motionSeed string, canvas OverlayCanvasSpec) overlayplan.PlanIdentity {
	return overlayplan.PlanIdentity{
		PlanID:     planID,
		VideoID:    videoID,
		ProjectID:  projectID,
		MotionSeed: motionSeed,
		Width:      canvas.Width,
		Height:     canvas.Height,
		FPSNum:     canvas.FPSNum,
		FPSDen:     canvas.FPSDen,
	}
}

func OverlayPlanCanvas(canvas OverlayCanvasSpec) (overlayplan.Canvas, error) {
	entityBadgeColor := strings.TrimSpace(canvas.EntityBadgeColor)
	if entityBadgeColor != "" {
		if !color.IsHexRGB(entityBadgeColor) {
			return overlayplan.Canvas{}, fmt.Errorf("entity_badge_color must be #RRGGBB")
		}
	}
	directives := make([]json.RawMessage, 0, len(canvas.AnimationDirectives))
	for _, directive := range canvas.AnimationDirectives {
		encoded, err := json.Marshal(directive)
		if err != nil {
			return overlayplan.Canvas{}, fmt.Errorf("overlay plan v2: encode animation directive: %w", err)
		}
		directives = append(directives, encoded)
	}
	presentation := overlayplan.MapPresentation{
		MapType: canvas.MapType, CameraAnimation: canvas.MapCameraAnimation,
		LabelAnimation: canvas.MapLabelAnimation, Animation: canvas.MapAnimation, Level: canvas.MapLevel,
	}
	if canvas.MapPresentation != nil {
		presentation.GroupNearbyPlaces = canvas.MapPresentation.GroupNearbyPlaces
		presentation.MaxPinsPerGroup = canvas.MapPresentation.MaxPinsPerGroup
		presentation.AllowRoute = canvas.MapPresentation.AllowRoute
		presentation.AnimationPool = append([]string(nil), canvas.MapPresentation.AnimationPool...)
		presentation.StylePool = append([]string(nil), canvas.MapPresentation.StylePool...)
	}
	// The pin is validated before it is encoded: a font the runtime does not
	// bundle or a motion the registry does not certify must fail here, with the
	// field named, rather than be dropped by the compiler.
	bulletStyles, err := editorialBulletStylesToWire(canvas.EditorialBulletStyles)
	if err != nil {
		return overlayplan.Canvas{}, fmt.Errorf("overlay plan v2: %w", err)
	}
	// The short-phrase buckets are the registry's, not a second list: the run
	// inherits them unless it carries its own resolved pools. Without them the
	// planner cannot rotate the lane and every phrase falls back to the preset's
	// single motion.
	shortPhraseMotions := cloneStringSliceMap(canvas.ShortPhraseMotionsByWord)
	if len(shortPhraseMotions) == 0 {
		shortPhraseMotions = capabilityoverlay.ShortPhraseMotionsByWord()
	}
	longPhraseMotions := append([]string(nil), canvas.LongPhraseMotions...)
	if len(longPhraseMotions) == 0 {
		longPhraseMotions = capabilityoverlay.LongPhraseMotions()
	}
	// Same rule for standalone images: the request may narrow the pool, but an
	// omitted pool means "rotate the certified catalog", not "use one fallback
	// entrance for every still". Without this the planner saw an empty pool and
	// stamped image_focus_reveal on 12/12 images (ITCLOSE-06).
	imageMotions := append([]string(nil), canvas.ImageMotions...)
	if len(imageMotions) == 0 {
		imageMotions = capabilityoverlay.CertifiedSingleImageMotions()
	}
	// The entity lanes follow the same rule. A verified portrait admitted by
	// the planner requires a non-empty pool; an omitted pool must inherit the
	// certified catalog instead of failing the whole plan.
	entityImageMotions := append([]string(nil), ResolveEntityImageMotionPool(canvas.EntityImageMotions)...)
	entityCaptionMotions := append([]string(nil), ResolveEntityCaptionMotionPool(canvas.EntityCaptionMotions)...)
	return overlayplan.Canvas{
		OverlayCategories:        overlayCategoriesToWire(canvas.OverlayCategories),
		GroupLimits:              cloneIntMap(canvas.OverlayGroupLimits),
		MaxOverlaysPerScene:      canvas.MaxOverlaysPerScene,
		MaxPhraseOverlays:        canvas.MaxPhraseOverlays,
		MaxImageOverlays:         canvas.MaxImageOverlays,
		MaxMapOverlays:           canvas.MaxMapOverlays,
		MapPresentation:          presentation,
		AnimationCounts:          cloneIntMap(canvas.AnimationCounts),
		ShortPhraseMotion:        canvas.ShortPhraseMotion,
		ShortPhraseMotionsByWord: shortPhraseMotions,
		LongPhraseMotions:        longPhraseMotions,
		PhraseMotionFamily:       strings.TrimSpace(canvas.PhraseMotionFamily),
		ImageMotions:             imageMotions,
		ImageWithTextMotions:     append([]string(nil), canvas.ImageWithTextMotions...),
		ImagePresets:             capabilityoverlay.ImagePresetCandidates(),
		EntityImageMotions:       entityImageMotions,
		EntityCaptionMotions:     entityCaptionMotions,
		EntityStyleID:            canvas.EntityStyleID,
		EntityBadgeColor:         entityBadgeColor,
		AnimationDirectives:      directives,
		AnimationContractVersion: canvas.AnimationContractVersion,
		AnimationCatalogDigest:   canvas.AnimationCatalogDigest,
		DisableEditorialOverlays: canvas.DisableEditorialOverlays,
		DisableRecapOverlays:     canvas.DisableRecapOverlays,
		DateMotionFamily:         canvas.DateMotionFamily,
		EnableCharts:             canvas.EnableCharts,
		DisableChartOverlays:     canvas.DisableChartOverlays,
		DisableNumberOverlays:    canvas.DisableNumberOverlays,
		MapsOnly:                 canvas.MapsOnly,
		LightLeakOnClipStart:     canvas.RandomLightLeakOnClipStart,
		LightLeakOnOverlayStart:  canvas.RandomLightLeakOnOverlayStart,
		EditorialBulletStyles:    bulletStyles,
	}, nil
}

func cloneIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, value := range in {
		out[key] = append([]string(nil), value...)
	}
	return out
}

// OverlayPlanClips projects the canonical real video spans of the scenes this
// request actually carries. Freeze tails stay explicit so Rust can skip them; no
// clip is invented or retimed here. A timeline segment whose scene the request
// did not include (fixed-media intro/outro) is not part of the editorial plan
// and must not be projected, or the request contradicts itself.
func OverlayPlanClips(result *contract.GenerateResult, planScenes map[string]struct{}) []overlayplan.Clip {
	if result == nil || result.CanonicalTimeline == nil || len(planScenes) == 0 {
		return nil
	}
	var out []overlayplan.Clip
	for _, scene := range result.CanonicalTimeline.Segments {
		sceneID := strings.TrimSpace(scene.ID)
		if _, inPlan := planScenes[sceneID]; !inPlan {
			continue
		}
		for index, clip := range scene.EffectiveVideoSegments() {
			if sceneID == "" || clip.AssetID == "" {
				continue
			}
			out = append(out, overlayplan.Clip{
				SceneID:    sceneID,
				AssetID:    clip.AssetID,
				Index:      index,
				StartUS:    scene.TimelineStartUS + clip.TimelineOffsetUS,
				DurationUS: clip.TimelineDurationUS,
				Freeze:     clip.Freeze,
			})
		}
	}
	return out
}

func overlayPlanSeal(result *contract.GenerateResult, canvas OverlayCanvasSpec, bounds map[string][2]int64) *overlayplan.Seal {
	durationUS := timelineDurationUS(bounds)
	if result.CanonicalTimeline != nil && result.CanonicalTimeline.DurationUS > durationUS {
		durationUS = result.CanonicalTimeline.DurationUS
	}
	return &overlayplan.Seal{
		AudioDurationUS: durationUS,
		OutputProfileID: canvas.OutputProfileID,
		Background:      overlayPlanBackground(canvas.Background),
	}
}

// overlayPlanBackground projects the sealed background onto the typed wire
// contract. Nil stays nil so the envelope omits an unset background.
func overlayPlanBackground(src *capmodel.OverlayBackground) *overlayplan.Background {
	if src == nil {
		return nil
	}
	out := &overlayplan.Background{
		Kind:    src.Kind,
		Color:   append([]float64(nil), src.Color...),
		Fit:     src.Fit,
		Opacity: src.Opacity,
		Loop:    src.Loop,
		Style:   src.Style,
	}
	for _, ref := range src.AssetRefs {
		out.AssetRefs = append(out.AssetRefs, overlayplan.AssetRef{
			AssetID: ref.AssetID, URL: ref.URL, SHA256: ref.SHA256, MediaType: ref.MediaType,
		})
	}
	return out
}

func timelineDurationUS(bounds map[string][2]int64) int64 {
	var duration int64
	for _, window := range bounds {
		if window[1] > duration {
			duration = window[1]
		}
	}
	return duration
}

func overlayPlanBudgets(canvas OverlayCanvasSpec) *overlayplan.Budgets {
	return &overlayplan.Budgets{
		PhraseOverlays: canvas.MaxPhraseOverlays, ImageOverlays: canvas.MaxImageOverlays,
		MapOverlays: canvas.MaxMapOverlays, MaxPerScene: canvas.MaxOverlaysPerScene,
		// Group limits are keyed by the documented plural names; reading the
		// singular spelling here silently dropped every short/long cap.
		ShortPhrase: canvas.OverlayGroupLimits[overlayGroupShortPhrases], LongPhrase: canvas.OverlayGroupLimits[overlayGroupLongPhrases],
		Bullet: canvas.OverlayGroupLimits["bullets"],
	}
}

// OverlayPlanCanonicalEntityIDs projects the canonical identity of every
// annotation occurrence and of every verified portrait onto the stable
// occurrence id the plan joins by. The annotation surface wins: it is the
// authored identity, while the media entry only names the same identity through
// a resolved portrait. An occurrence nobody resolved keeps an empty canonical
// id instead of claiming its stable id is canonical.
func OverlayPlanCanonicalEntityIDs(result *contract.GenerateResult, media *overlayplan.Media) map[string]string {
	out := map[string]string{}
	record := func(stable, canonical string) {
		stable = strings.TrimSpace(stable)
		canonical = strings.TrimSpace(canonical)
		if separator := strings.Index(canonical, "::"); separator >= 0 {
			canonical = canonical[separator+2:]
		}
		if stable == "" || canonical == "" || out[stable] != "" {
			return
		}
		out[stable] = canonical
	}
	if result != nil {
		collect := func(annotations *scriptpkg.SceneAnnotations) {
			if annotations == nil {
				return
			}
			for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotations.PrimaryEntities...), annotations.SecondaryEntities...) {
				record(annotationStableEntityID(entity), AnnotationCanonicalEntityID(entity))
			}
		}
		for i := range result.Scenes {
			collect(result.Scenes[i].Annotations)
			for _, localized := range result.Scenes[i].LocalizedAnnotations {
				collect(localized)
			}
		}
	}
	if media != nil {
		for _, entry := range media.VerifiedEntityAssets {
			record(entry.EntityID, entry.CanonicalEntityID)
		}
	}
	return out
}

func OverlayPlanMedia(result *contract.GenerateResult, language contract.Language, sceneInputs []capabilityoverlay.SceneInput) *overlayplan.Media {
	media := &overlayplan.Media{}
	if result != nil {
		seen := map[string]struct{}{}
		for i := range result.Scenes {
			annotations := annotationsForLanguage(result.Scenes[i], language, result.SourceLanguage)
			if annotations == nil {
				continue
			}
			entities := append(append([]scriptpkg.AnnotatedEntity(nil), annotations.PrimaryEntities...), annotations.SecondaryEntities...)
			for _, entity := range entities {
				if entity.Image == nil || strings.TrimSpace(entity.Image.AssetID) == "" {
					continue
				}
				stable := annotationStableEntityID(entity)
				canonical := AnnotationCanonicalEntityID(entity)
				key := stable + "\x00" + canonical
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				media.VerifiedEntityAssets = append(media.VerifiedEntityAssets, overlayplan.VerifiedEntityAsset{
					EntityID: stable, CanonicalEntityID: canonical,
					Assets: []overlayplan.AssetRef{{
						AssetID: entity.Image.AssetID, URL: EntityImageURL(entity.Image),
						SHA256: entity.Image.SHA256, MediaType: entity.Image.MediaType,
					}},
				})
			}
		}
		// The resolver also sees bundle portraits and localized aliases that the
		// annotation pass above cannot name. They belong to the same verified
		// surface: one entity-id list and one planner decision. There is no
		// second "promotable" list and no post-plan promotion pass.
		media.VerifiedEntityAssets = append(media.VerifiedEntityAssets, ResolvedEntityAssets(result, media.VerifiedEntityAssets)...)
	}
	for _, input := range sceneInputs {
		if len(input.Images) == 0 {
			continue
		}
		media.SceneImageCandidates = append(media.SceneImageCandidates, overlayplan.SceneImageCandidateSet{
			SceneID: input.ID, Candidates: imageCandidatesToWire(input.Images),
		})
	}
	return media
}

// ResolvedEntityAssets projects the portraits the media resolver can render for
// a plan occurrence onto the ONE verified surface. The planner, not this
// boundary, takes the card→image decision; shipping the resolved identity +
// bytes is what lets it do so without the PostgreSQL catalog or the Go sampler.
//
// Only renderable, content-addressed bytes cross: a semantic placeholder URL or
// a record without a 64-char digest is skipped here instead of failing the whole
// request in the Rust validator. Entries already named by the annotation pass
// are not duplicated: the first entry for an identity wins.
func ResolvedEntityAssets(result *contract.GenerateResult, existing []overlayplan.VerifiedEntityAsset) []overlayplan.VerifiedEntityAsset {
	if result == nil || result.EntityTimeline == nil || len(result.EntityTimeline.Scenes) == 0 {
		return nil
	}
	resolver, canonicalByStable := EntityCardMediaIndex(result)
	if resolver == nil {
		return nil
	}
	// Dedupe by occurrence identity only. Two occurrences can legitimately
	// resolve to the same canonical portrait (a localized card that keeps the
	// surname); each one needs its own entity_id entry, because that is the id
	// the planner's card actually carries. Rust takes the first matching entry.
	covered := make(map[string]struct{}, len(existing))
	for _, entry := range existing {
		if id := strings.TrimSpace(entry.EntityID); id != "" {
			covered[id] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(result.EntityTimeline.Scenes))
	var out []overlayplan.VerifiedEntityAsset
	for _, scene := range result.EntityTimeline.Scenes {
		for _, occurrence := range scene.Entities {
			entityID := strings.TrimSpace(occurrence.EntityID)
			if entityID == "" {
				continue
			}
			if _, exists := seen[entityID]; exists {
				continue
			}
			if _, exists := covered[entityID]; exists {
				continue
			}
			for _, lookupID := range EntityAssetLookupIDs(occurrence, canonicalByStable) {
				ref, err := resolver.ResolveBest(lookupID)
				if err != nil {
					continue
				}
				assetID := strings.TrimSpace(ref.AssetID)
				url := strings.TrimSpace(ref.URL)
				sha := strings.ToLower(strings.TrimSpace(ref.SHA256))
				if assetID == "" || url == "" || len(sha) != 64 {
					continue
				}
				if strings.HasPrefix(url, "assets/semantic/") || strings.HasPrefix(url, "semantic/") {
					continue
				}
				canonical := lookupID
				if separator := strings.Index(canonical, "::"); separator >= 0 {
					canonical = canonical[separator+2:]
				}
				seen[entityID] = struct{}{}
				covered[entityID] = struct{}{}
				out = append(out, overlayplan.VerifiedEntityAsset{
					EntityID:          entityID,
					CanonicalEntityID: canonical,
					Assets: []overlayplan.AssetRef{{
						AssetID: assetID, URL: url, SHA256: sha, MediaType: ref.MediaType,
					}},
				})
				break
			}
		}
	}
	return out
}

// EntityAssetLookupIDs tries the identity projections the resolver card can
// carry for one occurrence: the plan occurrence id, the plan item id the Rust
// planner stamps, their canonical projections, and the verified display-name
// alias a translated card may keep.
func EntityAssetLookupIDs(occurrence capabilityentities.EntityOccurrence, canonicalByStable map[string]string) []string {
	keys := []string{strings.TrimSpace(occurrence.EntityID)}
	sceneID := strings.TrimSpace(occurrence.SceneID)
	name := strings.TrimSpace(occurrence.Name)
	if sceneID != "" && name != "" {
		// The legacy spelling is a lookup alias (see OverlayDisplayAlias), and
		// this is the only place it enters the join: as an input key whose
		// value is the canonical id, never as an identity of its own.
		if alias := OverlayDisplayAlias(sceneID, name, ""); alias != "" {
			keys = append(keys, alias)
		}
	}
	out := make([]string, 0, len(keys)*2+1)
	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		for _, existing := range out {
			if existing == id {
				return
			}
		}
		out = append(out, id)
	}
	for _, key := range keys {
		appendID(canonicalByStable[key])
	}
	for _, key := range keys {
		appendID(key)
	}
	// The display-name projection is typed: a PERSON surname alias must never
	// resolve the GPE of the same surface. See BundleNameKey.
	if key := BundleNameKey(occurrence.Type, name); key != "" {
		appendID(canonicalByStable[key])
	}
	return out
}

// EntityAssetLocalPaths returns the already-materialized cache location of every
// entity portrait, keyed by content address. The v2 wire carries no local path
// (it is producer-only), so the seal reattaches it exactly like it does for
// scene images. It covers both the verified bundle and the annotation bindings:
// the unified verified surface can resolve either, and the renderer must get
// the same bytes without a second download.
func EntityAssetLocalPaths(result *contract.GenerateResult) map[string]capmodel.OverlayAssetRef {
	out := map[string]capmodel.OverlayAssetRef{}
	if result == nil {
		return out
	}
	catalog := newEntityBundleCatalog(result.SemanticRenderBundle)
	for _, entry := range catalog.entries {
		sha := strings.ToLower(strings.TrimSpace(entry.asset.ContentHash))
		localPath := strings.TrimSpace(entry.asset.LocalPath)
		if sha == "" || localPath == "" {
			continue
		}
		out[sha] = capmodel.OverlayAssetRef{
			AssetID: entry.asset.AssetID, URL: bundleAssetURL(entry.asset),
			LocalPath: localPath, SHA256: sha,
		}
	}
	localPathByAsset := map[string]string{}
	for _, segment := range result.Segments {
		candidates := append(append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.Candidates...), segment.Assets.SecondaryImages...)
		for _, candidate := range candidates {
			if id := strings.TrimSpace(candidate.AssetID); id != "" && usableLocalAssetPath(candidate.LocalPath) {
				localPathByAsset[id] = candidate.LocalPath
			}
		}
	}
	collect := func(ann *scriptpkg.SceneAnnotations) {
		if ann == nil {
			return
		}
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
			binding := entity.Image
			if binding == nil {
				continue
			}
			sha := strings.ToLower(strings.TrimSpace(binding.SHA256))
			localPath := strings.TrimSpace(binding.LocalPath)
			if !usableLocalAssetPath(localPath) {
				localPath = localPathByAsset[binding.AssetID]
			}
			if sha == "" || localPath == "" {
				continue
			}
			if _, exists := out[sha]; exists {
				continue
			}
			out[sha] = capmodel.OverlayAssetRef{
				AssetID: binding.AssetID, URL: EntityImageURL(binding),
				LocalPath: localPath, SHA256: sha, MediaType: binding.MediaType,
			}
		}
	}
	for i := range result.Scenes {
		collect(result.Scenes[i].Annotations)
		for _, localized := range result.Scenes[i].LocalizedAnnotations {
			collect(localized)
		}
	}
	return out
}

func OverlayPlanEntityTimeline(result *contract.GenerateResult, language contract.Language, projectID string, bounds map[string][2]int64, canonicalByEntity map[string]string) *overlayplan.EntityTimeline {
	durationUS := timelineDurationUS(bounds)
	out := &overlayplan.EntityTimeline{Version: 1, ProjectID: projectID, Language: string(language), DurationUS: durationUS}
	if result.EntityTimeline == nil {
		return out
	}
	out.ProjectID = result.EntityTimeline.ProjectID
	out.Language = result.EntityTimeline.Language
	if result.EntityTimeline.DurationUS > 0 {
		out.DurationUS = result.EntityTimeline.DurationUS
	}
	for _, scene := range result.EntityTimeline.Scenes {
		wireScene := overlayplan.SceneEntityTimeline{
			SceneID: scene.SceneID, SceneIndex: scene.SceneIndex,
			TimelineStartUS: scene.TimelineStartUS, VoiceoverAssetID: scene.VoiceoverAssetID,
		}
		for _, entity := range scene.Entities {
			// The stable occurrence id is the wire identity; the canonical id
			// is metadata that only exists when the annotation or media lane
			// actually resolved one. Stamping the stable id here made every
			// consumer compare two ids that were declared different and were
			// the same value (OVL-03).
			canonicalID := strings.TrimSpace(canonicalByEntity[strings.TrimSpace(entity.EntityID)])
			wireScene.Entities = append(wireScene.Entities, overlayplan.EntityOccurrence{
				EntityID: entity.EntityID, Name: entity.Name, Type: entity.Type,
				SceneID: entity.SceneID, SceneIndex: entity.SceneIndex,
				TextStart: entity.TextStart, TextEnd: entity.TextEnd,
				WordStart: entity.WordStart, WordEnd: entity.WordEnd,
				LocalStartUS: entity.LocalStartUS, LocalEndUS: entity.LocalEndUS,
				TimelineStartUS: entity.TimelineStartUS, AudioStartUS: entity.AudioStartUS, AudioEndUS: entity.AudioEndUS,
				Confidence: entity.Confidence, CanonicalEntityID: canonicalID,
			})
		}
		out.Scenes = append(out.Scenes, wireScene)
	}
	return out
}

// isLocalizedSourceEditorial reports whether a plan targets a language other
// than the language the editorial manifest was extracted from. Equal or empty
// source languages keep the historical behaviour and are not "localized".
func isLocalizedSourceEditorial(manifest *scriptpkg.EditorialManifest, language contract.Language) bool {
	if manifest == nil {
		return false
	}
	source := strings.TrimSpace(manifest.SourceLanguage)
	target := strings.TrimSpace(string(language))
	return source != "" && target != "" && !strings.EqualFold(source, target)
}

func withoutSourceEditorialCategories(canvas OverlayCanvasSpec) OverlayCanvasSpec {
	if canvas.OverlayCategories == nil {
		canvas.OverlayCategories = make(map[scriptpkg.OverlayCategory]scriptpkg.Toggle, 2)
	}
	canvas.OverlayCategories[scriptpkg.OverlayCategoryTitles] = scriptpkg.ToggleDisabled
	canvas.OverlayCategories[scriptpkg.OverlayCategoryBullets] = scriptpkg.ToggleDisabled
	return canvas
}

func withoutSourceEditorialWireCategories(in map[string]string) map[string]string {
	if in == nil {
		in = make(map[string]string, 2)
	}
	in[string(scriptpkg.OverlayCategoryTitles)] = string(scriptpkg.ToggleDisabled)
	in[string(scriptpkg.OverlayCategoryBullets)] = string(scriptpkg.ToggleDisabled)
	return in
}

func overlayCategoriesToWire(in map[scriptpkg.OverlayCategory]scriptpkg.Toggle) map[string]string {
	out := make(map[string]string, len(in))
	for category, toggle := range in {
		out[string(category)] = string(toggle)
	}
	return out
}

func speechTimingToWire(timing *capabilityaudio.SpeechTimingArtifact) *overlayplan.Timing {
	if timing == nil {
		return nil
	}
	out := &overlayplan.Timing{
		DurationUS: timing.DurationUS,
		Words:      make([]overlayplan.WordTiming, 0, len(timing.Words)),
	}
	for _, word := range timing.Words {
		out.Words = append(out.Words, overlayplan.WordTiming{
			Index:   word.Index,
			Text:    word.Text,
			StartUS: word.StartUS,
			EndUS:   word.EndUS,
		})
	}
	return out
}
