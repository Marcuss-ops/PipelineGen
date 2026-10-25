// Package scriptgeneration — runner_overlay_prepare.go owns the read-only
// snapshot and pure compute helpers the overlay.prepare branch uses while
// running concurrently with TTS. The branch never touches the mutable result
// (or result.Scenes) mid-flight: it works from a scene-text snapshot + the
// fenced VidRush segments, computes the grounded annotations and the
// pre-timing OverlayIntents as values, and the caller applies them to the
// durable result after the join. This keeps overlay.prepare from ever waiting
// for TTS or final audio without introducing a data race with the TTS writer.
//
// It also owns the ENQUEUE half of the same seam: which OverlayIntents carry
// asset bytes worth prefetching, and the fire-and-forget submission of the
// prepare job itself. Producer and consumer of one queue belong together — a
// change to what counts as "worth prefetching" is a change to what the prepare
// branch can compute, and vice versa. Merged 2026-10-10 from
// runner_overlay_enqueue.go.
package scriptgeneration

import (
	"context"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// sceneTextSnapshot is the read-only scene identity the prepare branch needs
// (id, index, source-language text, and any pre-existing annotations). It
// deliberately excludes Voiceover and duration fields so the branch can run
// concurrently with TTS without racing the TTS writer. Annotations is a shared
// read-only pointer: the branch never mutates it.
type sceneTextSnapshot struct {
	ID              string
	Index           int
	Text            string
	Annotations     *scriptpkg.SceneAnnotations
	ClipIDs         []string
	ExtractEntities scriptpkg.Toggle
}

// snapshotSceneText projects the source-language text (and any pre-existing
// annotations) of every scene into a lightweight read-only snapshot. It is
// taken before the concurrent TTS and prepare branches start, so both branches
// read stable text and the prepare branch never reads the mutable Scene struct.
func snapshotSceneText(scenes []Scene, language Language, requests ...GenerateRequest) []sceneTextSnapshot {
	var request GenerateRequest
	if len(requests) > 0 {
		request = requests[0]
	}
	out := make([]sceneTextSnapshot, 0, len(scenes))
	for i := range scenes {
		clipIDs := make([]string, 0, len(scenes[i].Clips)+1)
		for _, clip := range scenes[i].Clips {
			if clip != nil && clip.ID != "" {
				clipIDs = append(clipIDs, clip.ID)
			}
		}
		if scenes[i].Clip != nil && scenes[i].Clip.ID != "" {
			clipIDs = append(clipIDs, scenes[i].Clip.ID)
		}
		out = append(out, sceneTextSnapshot{
			ID: scenes[i].ID, Index: i, Text: scenes[i].Text[language],
			Annotations: scenes[i].Annotations, ClipIDs: clipIDs,
			ExtractEntities: sceneExtractionToggle(request, scenes[i]),
		})
	}
	return out
}

// vidRushPrepareResult carries the computed outputs of the prepare branch,
// applied to the durable result by the caller after the join.
type vidRushPrepareResult struct {
	segments       []scriptpkg.VidRushSegmentResult
	annotations    map[int]*scriptpkg.SceneAnnotations
	disabledScenes map[int]bool
	intents        []capabilityoverlay.OverlayIntent
}

// vidRushPrepareOutcome is the channel payload the prepare branch sends back
// to the main goroutine.
//
// err and nlpErr are kept SEPARATE because they are different facts reported
// against different stages: err is the VidRush prepare/join failure
// (StageGeneratingSceneText), while nlpErr is the translated-NLP failure
// (StageTranslatingScenes) of the work the semantic branch now also carries.
// Folding them into one field would mislabel the failed stage.
type vidRushPrepareOutcome struct {
	result     vidRushPrepareResult
	prefetched *AudioPrefetchResult
	err        error
	// localizedAnnotations is the translated-NLP output COMPUTED on the
	// semantic branch, to be applied to the durable result by the phase
	// goroutine after the join. It rides the outcome as a value precisely so no
	// worker writes onto result.Scenes while TTS is running.
	localizedAnnotations map[int]map[Language]*scriptpkg.SceneAnnotations
	nlpErr               error
}

// runVidRushJoinAndPrepare is the background branch of the SceneTextReady
// fan-out: it awaits the VidRush barrier, computes grounded annotations and
// pre-timing OverlayIntents from the read-only scene snapshot, and enqueues
// overlay.prepare. It never reads the mutable result while TTS is running.
func (r *Runner) runVidRushJoinAndPrepare(ctx context.Context, runID string, req GenerateRequest, snapshot []sceneTextSnapshot) (vidRushPrepareResult, error) {
	segments, err := r.waitForVidRush(ctx, runID)
	if err != nil {
		return vidRushPrepareResult{}, err
	}
	if req.EntityExtractionDisabled() {
		segments = retainExplicitlyEnabledSegments(req, snapshot, segments)
	}
	phraseLimit := req.MediaPlan.Extraction.MaxImportantPhrasesPerSegment
	includePhrases := req.MediaPlan.Extraction.Includes(mediadomain.ExtractionIncludeImportantPhrases)
	annotations := computeSegmentEntityAnnotations(snapshot, req.SourceLanguage, segments, phraseLimit, includePhrases, req.MediaPlan.Extraction.ImportantPhrases)
	if r.shouldGeocodeScriptLocations(req) {
		geocoded := make(map[int]*scriptpkg.SceneAnnotations, len(annotations))
		for index, annotation := range annotations {
			if annotation == nil {
				geocoded[index] = nil
				continue
			}
			clone := *annotation
			clone.PrimaryEntities = append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...)
			clone.SecondaryEntities = append([]scriptpkg.AnnotatedEntity(nil), annotation.SecondaryEntities...)
			for i := range clone.PrimaryEntities {
				clone.PrimaryEntities[i].Geo = annotation.PrimaryEntities[i].Geo
			}
			for i := range clone.SecondaryEntities {
				clone.SecondaryEntities[i].Geo = annotation.SecondaryEntities[i].Geo
			}
			geocoded[index] = &clone
		}
		sceneTexts := make(map[int]string, len(snapshot))
		for index, scene := range snapshot {
			if scene.ExtractEntities != scriptpkg.ToggleDisabled {
				sceneTexts[index] = scene.Text
			}
		}
		wantMaps := req.MapsOnly || req.AnimationCounts["maps"] > 0 || req.AnimationCounts["one_map"] > 0 || req.AnimationCounts["two_maps"] > 0
		if err := r.geocodePlaceAnnotations(ctx, geocoded, string(req.SourceLanguage), geocodeOptions{sceneTexts: sceneTexts, mapsOnly: wantMaps}); err != nil {
			return vidRushPrepareResult{}, err
		}
		annotations = geocoded
	}
	// Map candidates are compiled again later from the durable scene
	// annotations. Apply the name/homonym rejection to that final surface as
	// well, so a downstream projection cannot restore a person token as a city.
	if req.MaxMapOverlays > 0 || req.MapsOnly || req.AnimationCounts["maps"] > 0 || req.AnimationCounts["one_map"] > 0 || req.AnimationCounts["two_maps"] > 0 {
		for index, annotation := range annotations {
			if annotation == nil || index < 0 || index >= len(snapshot) {
				continue
			}
			stripPersonNameMapCandidates(annotation, snapshot[index].Text)
		}
	}
	var intents []capabilityoverlay.OverlayIntent
	if r.overlayRegistry != nil {
		intents = planOverlayIntentsForAnnotations(snapshot, annotations, r.overlayRegistry)
		if err := r.enqueueOverlayPrepare(ctx, runID, req, intents); err != nil {
			return vidRushPrepareResult{}, err
		}
	}
	disabledScenes := make(map[int]bool)
	for _, scene := range snapshot {
		if scene.ExtractEntities == scriptpkg.ToggleDisabled {
			disabledScenes[scene.Index] = true
		}
	}
	return vidRushPrepareResult{segments: segments, annotations: annotations, disabledScenes: disabledScenes, intents: intents}, nil
}

// retainExplicitlyEnabledSegments keeps only incremental results for segments
// whose explicit enabled override wins over the run-level opt-out.
func retainExplicitlyEnabledSegments(_ GenerateRequest, snapshot []sceneTextSnapshot, segments []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	if len(segments) == 0 {
		return nil
	}
	out := make([]scriptpkg.VidRushSegmentResult, 0, len(segments))
	for _, segment := range segments {
		for _, scene := range snapshot {
			if scene.ID == segment.SceneID && scene.ExtractEntities == scriptpkg.ToggleEnabled {
				out = append(out, segment)
				break
			}
		}
	}
	return out
}

func sourceTraceFromResult(result *GenerateResult) scriptpkg.SourceTrace {
	trace := scriptpkg.SourceTrace{}
	if result == nil {
		return trace
	}
	trace = result.SourceTrace
	seen := make(map[string]struct{})
	for _, scene := range result.Scenes {
		refs := scene.Clips
		if len(refs) == 0 && scene.Clip != nil {
			refs = []*ClipReference{scene.Clip}
		}
		for _, ref := range refs {
			if ref == nil || ref.ID == "" {
				continue
			}
			if _, ok := seen[ref.ID]; ok {
				continue
			}
			seen[ref.ID] = struct{}{}
			trace.AcceptedClipIDs = append(trace.AcceptedClipIDs, ref.ID)
		}
	}
	return trace
}

// applyVidRushPrepareProjections projects the prepare branch's outputs onto
// the durable result: the entity aggregate, per-scene annotations + typed
// entity results, compatibility surfaces, and the pre-timing OverlayIntents.
// It is shared by both the serial and parallel orchestration paths so the
// projection contract has exactly one owner.
func applyVidRushPrepareProjections(result *GenerateResult, prepared vidRushPrepareResult) {
	if result == nil {
		return
	}
	if len(prepared.segments) > 0 || len(prepared.disabledScenes) > 0 {
		segments := make([]scriptpkg.VidRushSegmentResult, 0, len(prepared.segments))
		for _, segment := range prepared.segments {
			disabled := prepared.disabledScenes[segment.Position]
			if segment.SceneID != "" {
				for index := range result.Scenes {
					if result.Scenes[index].ID == segment.SceneID {
						disabled = prepared.disabledScenes[index]
						break
					}
				}
			}
			if !disabled {
				segments = append(segments, segment)
			}
		}
		result.Segments = append([]scriptpkg.VidRushSegmentResult(nil), segments...)
		result.Entities = aggregateEntityResult(segments)
		if result.Entities == nil {
			result.Artifacts = nil
		} else {
			result.Artifacts = &GenerateArtifacts{Entities: result.Entities}
		}
		for idx, ann := range prepared.annotations {
			if idx >= 0 && idx < len(result.Scenes) {
				result.Scenes[idx].Annotations = ann
			}
		}
		applySegmentEntityResults(result, segments)
	}
	for index := range prepared.disabledScenes {
		if index >= 0 && index < len(result.Scenes) {
			// Entity extraction is off: drop the entity surface but keep
			// grounded important phrases. Phrases ground by verbatim
			// substring + certified timing and need no NER; nil-ing the
			// whole annotation would silently drop operator-supplied and
			// catalog phrases on entity-free runs. An annotation left with
			// no phrase content is still nil-ed to preserve the
			// disabled-means-no-entity-annotations contract.
			if ann := result.Scenes[index].Annotations; ann != nil {
				stripped := *ann
				stripped.PrimaryEntities = nil
				stripped.SecondaryEntities = nil
				if len(stripped.ImportantPhrases) == 0 {
					result.Scenes[index].Annotations = nil
				} else {
					result.Scenes[index].Annotations = &stripped
				}
			}
			result.Scenes[index].Entities = nil
			result.Scenes[index].EntityOverlayRequired = false
		}
	}
	result.OverlayIntents = prepared.intents
}

// computeSegmentEntityAnnotations builds the per-scene grounded annotations
// from the read-only snapshot + the fenced segments, keyed by scene index. It
// mirrors applySegmentEntityAnnotations matching (SceneID, then Position) but
// never mutates result, so it is safe to run concurrently with TTS. Pre-
// existing annotations on the snapshot are preserved and only overridden when
// a matching segment produces grounded annotations (the same precedence the
// sequential flow used), so a run without a VidRush pipeline still plans
// intents from the scenes' own annotations.
func computeSegmentEntityAnnotations(snapshot []sceneTextSnapshot, language Language, segments []scriptpkg.VidRushSegmentResult, phraseLimit int, includePhrases bool, requestedPhraseSets ...[]string) map[int]*scriptpkg.SceneAnnotations {
	annotations := make(map[int]*scriptpkg.SceneAnnotations)
	for _, s := range snapshot {
		if s.ExtractEntities == scriptpkg.ToggleDisabled {
			continue
		}
		if s.Annotations != nil {
			annotations[s.Index] = s.Annotations
		}
	}
	indexByID := make(map[string]int, len(snapshot))
	for _, s := range snapshot {
		indexByID[s.ID] = s.Index
	}
	for _, seg := range segments {
		idx, ok := indexByID[seg.SceneID]
		if !ok && seg.SceneID == "" {
			// Fall back to position matching when the segment carries no scene
			// identity (legacy barriers), mirroring applySegmentEntityAnnotations.
			if seg.Position >= 0 && seg.Position < len(snapshot) {
				idx, ok = seg.Position, true
			}
		}
		if !ok || snapshot[idx].ExtractEntities == scriptpkg.ToggleDisabled {
			continue
		}
		text := strings.TrimSpace(snapshot[idx].Text)
		if text == "" {
			continue
		}
		if ann := projectEntityAnnotations(text, string(language), seg); ann != nil {
			annotations[idx] = ann
		}
	}
	if includePhrases && phraseLimit > 0 {
		// VidRush may return fewer source phrase hints than the final narration
		// can ground (it often analyzes the brief before the generated prose is
		// available). Complete that surface locally from the exact final source
		// text, using the same deterministic selector used for translations.
		// This never calls a model and never invents a phrase outside the spoken
		// scene; entity spans remain blocked.
		// Phrase selection is decoupled from the entity toggle: important
		// phrases are editorial text grounded by verbatim string matching
		// plus certified word timing, never by NER. An entity-free catalog
		// run (extract_entities=disabled) must still project its phrases;
		// only the entity/segment paths above stay gated on the toggle.
		corpus := make([]string, 0, len(snapshot))
		for _, scene := range snapshot {
			if text := strings.TrimSpace(scene.Text); text != "" {
				corpus = append(corpus, text)
			}
		}
		for _, scene := range snapshot {
			text := strings.TrimSpace(scene.Text)
			if text == "" {
				continue
			}
			ann := annotations[scene.Index]
			if ann == nil {
				ann = &scriptpkg.SceneAnnotations{Version: 1, Language: string(language), Status: "completed"}
				annotations[scene.Index] = ann
			}
			supplementSourceImportantPhrases(ann, text, string(language), phraseLimit, corpus)
		}
	}
	// Operator-supplied phrase hints are external input grounded by verbatim
	// substring match (findEntitySpan): like the selector above they need no
	// NER, so they apply even when entity extraction is disabled.
	if len(requestedPhraseSets) > 0 {
		for _, scene := range snapshot {
			text := strings.TrimSpace(scene.Text)
			if text == "" {
				continue
			}
			ann := annotations[scene.Index]
			if ann == nil {
				ann = &scriptpkg.SceneAnnotations{Version: 1, Language: string(language), Status: "completed"}
				annotations[scene.Index] = ann
			}
			requestedSpans := make([]scriptpkg.AnnotationSpan, 0, len(requestedPhraseSets[0]))
			for _, requested := range requestedPhraseSets[0] {
				span, ok := findEntitySpan(text, requested)
				if !ok {
					continue
				}
				span.Text = strings.TrimSpace(requested)
				requestedSpans = append(requestedSpans, span)
				found := false
				for i := range ann.ImportantPhrases {
					if ann.ImportantPhrases[i].StartRune == span.StartRune && ann.ImportantPhrases[i].EndRune == span.EndRune {
						ann.ImportantPhrases[i].Text = strings.TrimSpace(requested)
						ann.ImportantPhrases[i].Score = 1
						ann.ImportantPhrases[i].Kind = "key_statement"
						found = true
						break
					}
				}
				if !found {
					ann.ImportantPhrases = append(ann.ImportantPhrases, scriptpkg.AnnotationSpan{
						Text: strings.TrimSpace(requested), StartRune: span.StartRune, EndRune: span.EndRune,
						Score: 1, Kind: "key_statement",
					})
				}
			}
			if len(requestedSpans) > 0 {
				kept := ann.ImportantPhrases[:0]
				for _, phrase := range ann.ImportantPhrases {
					displaced := false
					for _, requested := range requestedSpans {
						if phrase.StartRune == requested.StartRune && phrase.EndRune == requested.EndRune {
							phrase.Text, phrase.Score, phrase.Kind = requested.Text, 1, "key_statement"
							break
						}
						if phrase.StartRune < requested.EndRune && requested.StartRune < phrase.EndRune {
							displaced = true
							break
						}
					}
					if !displaced {
						kept = append(kept, phrase)
					}
				}
				ann.ImportantPhrases = kept
			}
		}
	}
	return annotations
}

// enqueueOverlayPrepare builds the overlay.prepare job for the run's
// pre-timing OverlayIntents and submits it (fire-and-forget). The plan id is
// the run id — the same idempotency key the overlay plan uses — so a retry
// never double-prepares.
func (r *Runner) enqueueOverlayPrepare(ctx context.Context, runID string, req GenerateRequest, intents []capabilityoverlay.OverlayIntent) error {
	if r.overlayPrepareEnqueuer == nil || len(intents) == 0 {
		return nil
	}
	if !overlayPrepareNeedsAssetPrefetch(intents) {
		// Text/phrase-only intents have no asset bytes for RenderingGen to
		// resolve or prefetch. The final timing-frozen compile resolves their
		// templates directly, so an overlay.prepare queue round-trip would only
		// add lease/IPC latency to the run.
		return nil
	}
	canvas := r.overlayCanvas.withDefaults()
	// overlay.prepare is a business stage boundary: the enqueue itself is
	// measured on the canonical Run clock so the run's critical path shows
	// the prepare submit wall time (which runs in parallel with TTS) instead
	// of hiding it inside the generate phase.
	if _, err := kernobs.MeasureStageReport(ctx, StageOverlayPrepare, func(stageCtx context.Context) error {
		return r.overlayPrepareEnqueuer.EnqueuePrepare(stageCtx, capabilityoverlay.PrepareRequest{
			SchemaVersion: capabilityoverlay.SchemaVersionPrepare,
			PlanID:        runID,
			VideoID:       runID,
			ProjectID:     strings.TrimSpace(req.Project),
			Width:         canvas.Width,
			Height:        canvas.Height,
			FPSNum:        canvas.FPSNum,
			FPSDen:        canvas.FPSDen,
			Intents:       intents,
		})
	}); err != nil {
		return err
	}
	return nil
}

func overlayPrepareNeedsAssetPrefetch(intents []capabilityoverlay.OverlayIntent) bool {
	for _, intent := range intents {
		if len(intent.AssetRefs) > 0 || len(intent.Payload.AssetRefs) > 0 {
			return true
		}
	}
	return false
}
