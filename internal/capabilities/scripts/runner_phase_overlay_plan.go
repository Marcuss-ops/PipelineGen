package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// compileAudioOverlayPlan builds the timing-frozen overlay plan from the
// canonical audio timeline. Keeping this before the independent audio render
// lets the overlay worker begin as soon as both plans exist.
func (r *Runner) compileAudioOverlayPlan(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, step ExecutionStep, result *GenerateResult) bool {
	reportTimingSkip := func(skip timingProjectionSkip) {
		observability.ScriptTimingProjectionSkipTotal.WithLabelValues(skip.SceneID, skip.Surface).Inc()
		r.log.Warn("voiceover timing projection skipped an unanchored surface",
			zap.String("scene_id", skip.SceneID), zap.String("surface", skip.Surface), zap.Error(skip.Cause))
	}
	if err := compileResultPhraseTimings(result, req.SourceLanguage, reportTimingSkip); err != nil {
		cause := fmt.Errorf("phrase timing compilation failed: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	if err := compileResultEntityTimeline(result, req.SourceLanguage, reportTimingSkip); err != nil {
		cause := fmt.Errorf("entity timeline compilation failed: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	background, bgErr := r.resolveOverlayBackground(ctx, req.OverlayBackground)
	if bgErr != nil {
		cause := fmt.Errorf("resolve overlay background failed: %w", bgErr)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	canvas := r.overlayCanvas
	canvas.ForegroundScalePercent = req.Render.ForegroundScalePercent
	canvas.Background = overlayBackgroundFromPayload(background)
	canvas.Style = req.OverlayStyle
	canvas.PhraseMotions = req.PhraseMotions
	canvas.PhraseMotionFamily = req.PhraseMotionFamily
	canvas.MotionSeed = strings.TrimSpace(req.MotionSeed)
	canvas.CoherenceSeed = strings.TrimSpace(req.CoherenceSeed)
	// Goal E2: the phrases the semantic profile grounded as most relevant take
	// the prominent entrance, the rest keep the calm rotation. The canvas may
	// already carry an explicit override (zero = unset).
	if canvas.HeavyPhrasePriority <= 0 {
		canvas.HeavyPhrasePriority = heavyPhrasePriorityDefault
	}
	// An explicit operator threshold wins over the default: catalog runs pin
	// it unreachable so heavy-boosted phrases stay inside the requested
	// caller-pool rotation instead of leaving it.
	if req.HeavyPhrasePriority > 0 {
		canvas.HeavyPhrasePriority = req.HeavyPhrasePriority
	}
	canvas.ImageMotions = req.ImageMotions
	canvas.EntityImageMotions = req.EntityImageMotions
	canvas.EntityCaptionMotions = req.EntityCaptionMotions
	canvas.AnimationPolicies = req.AnimationPolicies
	canvas.RandomLightLeakOnClipStart = req.RandomLightLeakOnClipStart
	canvas.RandomLightLeakOnOverlayStart = req.RandomLightLeakOnOverlayStart
	canvas.RandomImageEffects = req.RandomImageEffects
	canvas.MapType = req.MapType
	canvas.MapCameraAnimation = req.MapCameraAnimation
	canvas.MapLabelAnimation = req.MapLabelAnimation
	canvas.MapAnimation = req.MapAnimation
	canvas.MapPresentation = req.MapPresentation
	canvas.MaxPhraseOverlays = req.MaxPhraseOverlays
	canvas.MaxOverlaysPerScene = req.MaxOverlaysPerScene
	canvas.MaxMapOverlays = req.MaxMapOverlays
	canvas.MapsOnly = req.MapsOnly
	canvas.MaxImageOverlays = req.MaxImageOverlays
	canvas.AnimationCounts = cloneAnimationCounts(req.AnimationCounts)
	canvas.OverlayGroupLimits = cloneOverlayGroupLimits(req.OverlayGroupLimits)
	canvas.OverlayCategories = scriptpkg.CloneOverlayCategories(req.OverlayCategories)
	canvas.OverlayCategorySegments = scriptpkg.CloneScriptSegments(req.ScriptParams.Segments)
	// The current chain wire carries visual segments only; it cannot preserve
	// per-image sound_effect clips. Keep image overlays separate when per-image
	// SFX are requested so the established asset resolver can attach one cue to
	// each image before the plan is queued, rather than silently dropping cues.
	if req.RandomSFXOnImageOverlay {
		canvas.AnimationCounts["chain_disabled"] = 1
	}
	canvas.EntityStyleID = req.EntityStyleID
	canvas.DisableNumberOverlays = req.DisableNumberOverlays
	canvas.DisableRecapOverlays = req.DisableRecapOverlays
	canvas.DisableChartOverlays = req.DisableChartOverlays
	canvas.DisableEditorialOverlays = req.DisableEditorialOverlays
	canvas.EnableCharts = req.EnableCharts
	if canvas.Style == nil && background != nil {
		canvas.Style = background.Style
	}
	driveFolderID := strings.TrimSpace(req.Render.DriveFolderID)
	plates := r.mapPlates
	if !r.shouldGeocodeScriptLocations(req) {
		plates = nil
	}
	imageProviderEnabled := req.MediaPlan.ProviderPolicy.InternetImages.AsBool() || req.MediaPlan.ProviderPolicy.ImageGeneration.AsBool()
	// Context stills and identity portraits are separate overlay lanes. The
	// shared run image budget and content-hash dedupe prevent duplicate bytes.
	genericSceneImagesEnabled := req.MaxImageOverlays > 0 && imageProviderEnabled
	r.log.Info("runner: overlay canvas motion inputs",
		zap.String("run_id", runID),
		zap.String("phrase_motion_family", canvas.PhraseMotionFamily),
		zap.Int("phrase_motions", len(canvas.PhraseMotions)),
		zap.Float64("heavy_phrase_priority", canvas.HeavyPhrasePriority),
		zap.Any("animation_counts", canvas.AnimationCounts),
	)
	if err := compileResultOverlayPlan(result, req.SourceLanguage, runID, req.Project, driveFolderID, canvas, plates, req.MediaPlan.Extraction.EntityImages.PerScene(), genericSceneImagesEnabled); err != nil {
		cause := fmt.Errorf("overlay plan compilation failed: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	if req.RandomSFXOnImageOverlay {
		if err := attachRandomOverlaySFXToPlan(result.OverlayPlan); err != nil {
			cause := fmt.Errorf("attach random image-overlay SFX: %w", err)
			r.failExecutionStepBestEffort(ctx, exec, step, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		}
		for _, plan := range result.LocalizedOverlayPlans {
			if err := attachRandomOverlaySFXToPlan(plan); err != nil {
				cause := fmt.Errorf("attach random localized image-overlay SFX: %w", err)
				r.failExecutionStepBestEffort(ctx, exec, step, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
		}
	}
	if result.OverlayPlan != nil {
		fields := []zap.Field{
			zap.String("run_id", runID),
			zap.Int("scenes", len(result.Scenes)),
			zap.Int("timed_scenes", result.OverlayPlan.TimedScenes),
			zap.Int("items", len(result.OverlayPlan.Items)),
			zap.Int("recap_placements", result.OverlayPlan.RecapPlacements),
			zap.Int("recap_cards", result.OverlayPlan.RecapCards),
			zap.Int("chart_placements", result.OverlayPlan.ChartPlacements),
			zap.Int("chart_cards", result.OverlayPlan.ChartCards),
		}
		if intake := result.OverlayPlanIntake; intake != nil {
			fields = append(fields,
				zap.Int("candidate_items", intake.Candidates),
				zap.Int("planned_items", intake.Planned),
				zap.Any("planned_by_kind", intake.PlannedByKind),
				zap.Any("dropped_by_reason", intake.DroppedByReason),
			)
		}
		r.log.Info("runner: overlay plan compiled", fields...)
	}
	r.logPhraseMotionSelections(runID, result.OverlayPlan)
	r.logPhraseAnchoringDiagnostics(runID, result, req.SourceLanguage)
	setOverlayDriveJobID(result, exec.JobID)
	if err := validatePlannedEditingSpans(result); err != nil {
		return r.failAudioCompileStep(ctx, runID, exec, step, fmt.Errorf("editing timeline preflight failed: %w", err))
	}
	if _, err := BuildEditingTimeline(result); err != nil {
		cause := fmt.Errorf("editing timeline preflight failed: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	return true
}

type deferredAudioFailureKey struct{}

type deferredAudioFailure struct {
	cause error
	step  ExecutionStep
}

type audioOverlayOutcome struct {
	outcomes []overlayRenderOutcome
	err      error
}

// audioCompile overlaps snapshot-only overlays with encoding. All exits join
// both siblings; one caller owns result writes and exactly-once failures.
func (e *executionRun) audioCompile() bool {
	var state audioCompileState
	failure := &deferredAudioFailure{}
	ctx, cancel := context.WithCancel(context.WithValue(e.ctx, deferredAudioFailureKey{}, failure))
	defer cancel()
	failJoined := func(cause error) bool {
		failCtx, finish := context.WithTimeout(context.WithoutCancel(e.ctx), 5*time.Second)
		defer finish()
		step := failure.step
		if step.StepID == "" {
			step = state.Step
		}
		e.r.failExecutionStepBestEffort(failCtx, e.exec, step, cause)
		e.r.failRunWithRetry(failCtx, e.runID, StageCompilingAudio, cause)
		return false
	}
	var done chan audioOverlayOutcome
	state.OnPlanReady = func(result *GenerateResult) bool {
		snapshot, err := snapshotGenerateResult(result)
		if err != nil {
			failure.cause = fmt.Errorf("snapshot overlay plan: %w", err)
			return false
		}
		// The overlay branch runs from a detached snapshot. Reassert the
		// broker job identity at this hand-off so publication cannot fall back
		// to the internal run/plan ID after JSON checkpointing.
		setOverlayDriveJobID(snapshot, e.exec.JobID)
		done = make(chan audioOverlayOutcome, 1)
		go func() {
			var outcome audioOverlayOutcome
			// Explicit sibling ownership survives temporal containment.
			_, outcome.err = kernobs.MeasureIndependentStageReport(ctx, StageOverlayRender, func(c context.Context) error {
				var err error
				outcome.outcomes, err = e.r.renderOverlayPlans(c, e.req, e.resumeIdx, snapshot)
				if err == nil && e.req.FinalJob && e.r.overlayPublicationDrainer != nil {
					// All child renders are now submitted; join their bounded Drive
					// publications before closing the measured overlay stage. The
					// final master consumes these certified Drive IDs.
					err = e.r.overlayPublicationDrainer.Wait(c)
				}
				return err
			})
			if outcome.err != nil {
				cancel()
			}
			done <- outcome
		}()
		return true
	}
	compiled := e.r.measurePhase(ctx, kernobs.StageName(audioCompileStage), func(c context.Context) bool {
		return e.r.runAudioCompilePhase(c, e.runID, e.req, e.exec, e.resumeIdx, e.result, &state)
	})
	if !compiled {
		cancel()
	}
	// The certified master is ready when audio_compile returns. Publish it
	// while the already-started overlay branch is still rendering; the editing
	// timeline itself remains in audio_finalize below, after both siblings join.
	audioPublished := false
	if compiled {
		if !publishFinalAudioNeeded(e.req, e.r) {
			// Overlay-only runs consume no published master audio: the documents
			// catalog and the final_job payload are the only consumers of the
			// Drive asset, and neither is active for this run. Skip the Drive
			// upload entirely (golden measured it at ~15 s of wall) instead of
			// paying it for an artifact nobody reads.
			if e.r.log != nil {
				e.r.log.Info("final audio Drive publish skipped: no documents and no final job consume it",
					zap.String("run_id", e.runID))
			}
			audioPublished = true
		} else {
			audioPublished = e.measure(StageAudioPublish, func(c context.Context) bool {
				return e.r.publishFinalAudio(c, e.runID, e.req, e.routing, e.exec, e.result)
			})
			if !audioPublished {
				cancel()
			}
		}
	}
	if done != nil {
		outcome := <-done
		if outcome.err != nil && (compiled || (!errors.Is(outcome.err, context.Canceled) && !errors.Is(outcome.err, context.DeadlineExceeded))) {
			return failJoined(outcome.err)
		}
		if !compiled {
			return failJoined(failure.cause)
		}
		if !audioPublished {
			return false
		}
		e.r.applyOverlayRenderOutcomes(e.runID, e.result, outcome.outcomes)
	} else {
		if !compiled {
			return failJoined(failure.cause)
		}
		if !audioPublished {
			return false
		}
		if !e.measure(StageOverlayRender, func(c context.Context) bool {
			return e.r.runOverlayRenderPhase(c, e.runID, e.req, e.exec, e.resumeIdx, state, e.result)
		}) {
			return false
		}
	}
	if err := e.ctx.Err(); err != nil {
		return failJoined(err)
	}
	if !audioPublished {
		return false
	}
	if !e.measure(StageAudioFinalize, func(c context.Context) bool {
		return e.r.runAudioFinalizePhase(c, e.runID, e.exec, state, e.result)
	}) {
		return false
	}
	return true
}

// Map grounding helpers shared by the overlay-plan runner phase.
func mapPlaceCandidateIgnoredWords(registry *linguistics.LexiconRegistry, language string) (map[string]struct{}, error) {
	if registry == nil {
		return nil, fmt.Errorf("lexicon registry is not installed")
	}
	profile, err := registry.ResolveRequired(language)
	if err != nil {
		return nil, err
	}
	ignored := make(map[string]struct{}, len(profile.EntityBlocklist)+len(profile.StopWords)+len(profile.FunctionWords))
	for word := range profile.EntityBlocklist {
		ignored[word] = struct{}{}
	}
	for word := range profile.StopWords {
		ignored[word] = struct{}{}
	}
	for word := range profile.FunctionWords {
		ignored[word] = struct{}{}
	}
	// Sentence-initial adverbs can look like proper place names after
	// title-casing. Keep this small extension in the same exclusion set so a
	// geocoder homonym cannot turn prose such as "Infine" into a map pin.
	if strings.EqualFold(language, "it") || strings.HasPrefix(strings.ToLower(language), "it-") {
		ignored["infine"] = struct{}{}
		// These common Italian discourse words were misclassified as place
		// entities and geocoded to unrelated homonymous settlements.
		for _, word := range []string{"tale", "tali", "ciò", "cio", "egli", "ella", "esso", "essa", "essi", "esse", "lui", "lei", "loro", "questo", "questa", "questi", "queste", "quello", "quella", "quelli", "quelle", "primo", "prima", "secondo", "seconda", "solo", "usare", "dunque", "inoltre", "durante", "corte", "procura", "cassazione", "stasi", "sempio", "poggi", "alberto", "andrea", "chiara"} {
			ignored[word] = struct{}{}
		}
	}
	return ignored, nil
}

func ignoredMapCandidate(ignored map[string]struct{}, candidate, language string) bool {
	if len(ignored) == 0 {
		return false
	}
	key := strings.ToLower(normalizeGeocodePlaceQuery(candidate, language))
	_, ok := ignored[key]
	if ok {
		return true
	}
	// A multiword extraction can bypass the exact-word discourse list even
	// though one of its tokens is a known person name.
	if strings.EqualFold(language, "it") || strings.HasPrefix(strings.ToLower(language), "it-") {
		for _, word := range []string{"stasi", "sempio", "poggi", "alberto", "andrea", "chiara"} {
			if strings.Contains(" "+key+" ", " "+word+" ") {
				return true
			}
		}
	}
	return false
}

func groundedPersonName(annotation *scriptpkg.SceneAnnotations, candidate string) bool {
	if annotation == nil {
		return false
	}
	normalize := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	}
	name := normalize(candidate)
	if name == "" {
		return false
	}
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON") {
			continue
		}
		person := normalize(entity.CanonicalName)
		if person == name || strings.Contains(" "+name+" ", " "+person+" ") || strings.Contains(" "+person+" ", " "+name+" ") {
			return true
		}
	}
	return false
}

func geocodeAnnotatedEntity(ctx context.Context, geocoder capabilitygeocoding.Geocoder, resolved map[string]capabilitygeocoding.Result, entity *scriptpkg.AnnotatedEntity, sceneIndex int, language string, geoContext *capabilitygeocoding.Context) error {
	if entity == nil || !isPlaceEntityType(entity.Type) || entity.Geo != nil {
		return nil
	}
	name := normalizeGeocodePlaceQuery(entity.CanonicalName, language)
	if name == "" {
		return nil
	}
	if isGenericPlaceLabel(name) {
		return nil
	}
	lookupContext := capabilitygeocoding.Context{}
	if geoContext != nil {
		lookupContext = *geoContext
	}
	cacheKey := geocodeCacheKey(language, name, lookupContext)
	result, ok := resolved[cacheKey]
	if !ok {
		var err error
		result, err = geocoder.Geocode(ctx, capabilitygeocoding.Request{Query: name, Language: language, Context: lookupContext})
		if err != nil {
			if errors.Is(err, capabilitygeocoding.ErrNoResult) {
				// Unresolvable, ambiguous-without-context, or malformed generated
				// labels stay visible as ordinary entities, but cannot produce a
				// map pin. Provider, transport, cancellation, and validation
				// errors still fail closed.
				return nil
			}
			return fmt.Errorf("geocode grounded place %q in scene annotation %d: %w", name, sceneIndex, err)
		}
		if err := result.Validate(); err != nil {
			return fmt.Errorf("geocode grounded place %q returned invalid coordinates: %w", name, err)
		}
		resolved[cacheKey] = result
	}
	entity.Geo = &scriptpkg.GeoCoordinate{
		Latitude: result.Latitude, Longitude: result.Longitude,
		DisplayName: strings.TrimSpace(result.DisplayName),
		Scope:       result.Scope,
	}
	if name != strings.Join(strings.Fields(strings.TrimSpace(entity.CanonicalName)), " ") {
		entity.CanonicalName = name
		entity.Text = name
	}
	// A resolved country/region becomes context for the finer places the
	// scene names after it (never the reverse: context is only ever grounded).
	adoptGeoContext(geoContext, entity)
	return nil
}

// normalizeGeocodePlaceQuery removes the Portuguese sentence article in the
// observed country phrase "O Brasil". Nominatim ranked a locality in Galicia
// for that full phrase; the query should name the country itself, Brasil.
func normalizeGeocodePlaceQuery(value, language string) string {
	words := strings.Fields(strings.TrimSpace(value))
	lang := strings.ToLower(strings.TrimSpace(language))
	lang = strings.SplitN(lang, "-", 2)[0]
	if lang == "pt" && len(words) == 2 && strings.EqualFold(words[0], "o") && strings.EqualFold(words[1], "Brasil") {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

func isGenericPlaceLabel(name string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " ")) {
	case "here", "there", "local", "nearby", "home", "workplace", "city", "town", "village", "region", "area", "location",
		"paese", "paesi", "nesta", "neste", "nessa", "nesse", "esta", "este", "essa", "esse":
		return true
	default:
		return false
	}
}

func isPlaceEntityType(entityType string) bool {
	switch scriptpkg.NormalizeAnnotationType(entityType) {
	case "GPE", "LOCATION":
		return true
	default:
		return false
	}
}

// ResolveOverlayBackground preserves the scripts capability API while
// delegating background policy to the dedicated overlay package.
func ResolveOverlayBackground(ctx context.Context, source OverlayBackgroundSource, src *scriptpkg.OverlayBackgroundSpec) (*scriptpkg.OverlayBackgroundSpec, error) {
	if source == nil {
		return capabilityoverlay.ResolveBackground(ctx, nil, src)
	}
	return capabilityoverlay.ResolveBackground(ctx, func(ctx context.Context, assetID string) (string, string, string, string, string, error) {
		asset, err := source.ResolveOverlayBackground(ctx, assetID)
		return asset.AssetID, asset.LocalPath, asset.URL, asset.SHA256, asset.MediaType, err
	}, src)
}

func (r *Runner) resolveOverlayBackground(ctx context.Context, src *scriptpkg.OverlayBackgroundSpec) (*scriptpkg.OverlayBackgroundSpec, error) {
	if r == nil {
		return nil, fmt.Errorf("visual background asset cannot be resolved: runner is nil")
	}
	return ResolveOverlayBackground(ctx, r.overlayBackgroundSource, src)
}
