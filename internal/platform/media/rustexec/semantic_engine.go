package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// SemanticEngineAdapter is the Go boundary of the unified Rust semantic
// engine (`semantic_manifest.v3`). One request
// carries every scene of a script and returns every grounded projection at
// once: bullets, chart values, temporal facts and numeric facts.
//
// This replaces the two per-category VisualNER calls (bullet and chart) for
// the same scene. The engine keeps every candidate; selection (top-N, chart
// admission, overlay budget) stays in PipelineGen.
type SemanticEngineAdapter struct {
	executor *Executor
	embedder passageBatchEmbedder
}

const semanticEngineOutputLimit = 8 << 20
const semanticEngineMaxInFlight = 8

// NewSemanticEngineExecutor reserves bounded response headroom for whole-
// script manifests while keeping ordinary media protocol caps conservative.
func NewSemanticEngineExecutor(binaryPath, ffmpegPath string, log *zap.Logger) *Executor {
	return NewMultiplexedExecutorWithEnvironment(
		binaryPath, ffmpegPath,
		semanticEngineMaxInFlight, semanticEngineOutputLimit,
		semanticCacheEnvironment(), log,
	)
}

// semanticCacheEnvironment forwards the BrainRust persistent-cache opt-in to
// the worker explicitly. Both variables are unset by default, so a deployment
// that never configures them keeps the historical memory-only behaviour.
// SEMANTIC_CACHE_MAX_BYTES is optional; the Rust default (1 GiB) applies when
// it is absent.
func semanticCacheEnvironment() []string {
	env := make([]string, 0, 2)
	if dir := strings.TrimSpace(os.Getenv("SEMANTIC_CACHE_DIR")); dir != "" {
		env = append(env, "SEMANTIC_CACHE_DIR="+dir)
	}
	if raw := strings.TrimSpace(os.Getenv("SEMANTIC_CACHE_MAX_BYTES")); raw != "" {
		env = append(env, "SEMANTIC_CACHE_MAX_BYTES="+raw)
	}
	return env
}

// NewSemanticEngineAdapter binds the adapter to a dedicated executor for
// `bin/semantic-engine`. The executor is separate from the media executor so
// a semantic batch never queues behind renders.
func NewSemanticEngineAdapter(executor *Executor) (*SemanticEngineAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("semantic-engine: executor is required")
	}
	return &SemanticEngineAdapter{executor: executor}, nil
}

const (
	semanticManifestV3 = "semantic_manifest.v3"
)

// workerFailure renders a worker failure for operators. The machine-stable
// error_code (decode, unsupported_operation, execution) becomes a bracketed
// tag so a log filter can select one class of failure without parsing prose.
// A worker that predates the additive envelope carries no code: its message is
// kept byte-for-byte, which is why the tag is conditional.
func workerFailure(response semanticEngineResponse) error {
	message := strings.TrimSpace(response.Error)
	if code := strings.TrimSpace(response.ErrorCode); code != "" {
		return fmt.Errorf("semantic-engine: worker [%s]: %s", code, message)
	}
	return fmt.Errorf("semantic-engine: worker: %s", message)
}

// SemanticBundles analyses every scene of a script in ONE worker call and
// returns the grounded projections keyed by scene id. The whole batch fails
// closed on a contract violation; a scene the extractor refused is returned
// with an empty bundle (never invented values).
func (a *SemanticEngineAdapter) SemanticBundles(ctx context.Context, language string, scenes []scriptgen.SemanticSceneInput) (map[string]*scriptgen.SemanticCandidateBundle, error) {
	bundles, _, err := a.semanticBundles(ctx, language, scenes, nil)
	return bundles, err
}

func (a *SemanticEngineAdapter) semanticBundles(ctx context.Context, language string, scenes []scriptgen.SemanticSceneInput, editorial *semanticEngineEditorial) (map[string]*scriptgen.SemanticCandidateBundle, *scriptpkg.PhraseImpactResult, error) {
	if a == nil || a.executor == nil {
		return nil, nil, fmt.Errorf("semantic-engine: executor is not configured")
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return nil, nil, fmt.Errorf("semantic-engine: language is required")
	}
	if len(scenes) == 0 {
		return nil, nil, fmt.Errorf("semantic-engine: at least one scene is required")
	}
	request := semanticEngineRequest{
		Operation: "analyze_script",
		Language:  language,
		Scenes:    make([]semanticEngineScene, 0, len(scenes)),
		Editorial: editorial,
	}
	seen := make(map[string]struct{}, len(scenes))
	for _, scene := range scenes {
		id := strings.TrimSpace(scene.SceneID)
		if id == "" {
			return nil, nil, fmt.Errorf("semantic-engine: scene with empty id")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, nil, fmt.Errorf("semantic-engine: duplicate scene id %q", id)
		}
		seen[id] = struct{}{}
		request.Scenes = append(request.Scenes, semanticEngineScene{SceneID: id, Text: scene.Text})
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: encode: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: execute: %w", err)
	}
	var response semanticEngineResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: decode: %w", err)
	}
	if !response.OK {
		return nil, nil, workerFailure(response)
	}
	if response.Cached && a.executor.log != nil {
		a.executor.log.Debug("semantic-engine: manifest served from the persistent cache",
			zap.String("language", language), zap.Int("scenes", len(request.Scenes)))
	}
	manifest := response.Manifest
	if manifest == nil {
		return nil, nil, fmt.Errorf("semantic-engine: response carries no manifest")
	}
	if manifest.SchemaVersion != semanticManifestV3 {
		return nil, nil, fmt.Errorf("semantic-engine: unsupported schema_version %q", manifest.SchemaVersion)
	}
	if manifest.Language != language || manifest.Fingerprint == "" || manifest.Analyzers.VisualNER == "" ||
		manifest.Analyzers.SemanticCore == "" || manifest.Analyzers.PhraseImpact == "" {
		return nil, nil, fmt.Errorf("semantic-engine: manifest metadata is incomplete or does not match request language")
	}
	if strings.TrimSpace(manifest.Analyzers.EntityIndex) == "" {
		return nil, nil, fmt.Errorf("semantic-engine: v3 manifest omits entity index provenance")
	}
	if manifest.Entities == nil {
		return nil, nil, fmt.Errorf("semantic-engine: v3 manifest omits its entity projection")
	}
	if len(manifest.Scenes) != len(request.Scenes) {
		return nil, nil, fmt.Errorf("semantic-engine: manifest has %d scenes, request had %d", len(manifest.Scenes), len(request.Scenes))
	}

	texts := make(map[string]string, len(request.Scenes))
	for _, scene := range request.Scenes {
		texts[scene.SceneID] = scene.Text
	}
	linkedIdentities, err := projectSemanticLinkedEntities(texts, manifest.Entities)
	if err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: linked entities: %w", err)
	}
	if err := validateLinkedEntityCoverage(manifest.Scenes, linkedIdentities); err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: linked entity coverage: %w", err)
	}
	bundles := make(map[string]*scriptgen.SemanticCandidateBundle, len(manifest.Scenes))
	for index, scene := range manifest.Scenes {
		source, ok := texts[scene.SceneID]
		if !ok {
			return nil, nil, fmt.Errorf("semantic-engine: manifest scene %q was not requested", scene.SceneID)
		}
		if scene.SceneID != request.Scenes[index].SceneID {
			return nil, nil, fmt.Errorf("semantic-engine: manifest scene order changed at %d", index)
		}
		bundle := &scriptgen.SemanticCandidateBundle{}
		bundle.SchemaVersion = manifest.SchemaVersion
		bundle.Language = manifest.Language
		bundle.Fingerprint = manifest.Fingerprint
		bundle.Analyzers = scriptgen.SemanticAnalyzerVersions{
			VisualNER: manifest.Analyzers.VisualNER, SemanticCore: manifest.Analyzers.SemanticCore,
			PhraseImpact: manifest.Analyzers.PhraseImpact, Lexicon: manifest.Analyzers.Lexicon,
			EntityIndex: manifest.Analyzers.EntityIndex, Numbers: manifest.Analyzers.Numbers,
			Dates: manifest.Analyzers.Dates, Quantities: manifest.Analyzers.Quantities,
			Places: manifest.Analyzers.Places, Routes: manifest.Analyzers.Routes,
			Roles: manifest.Analyzers.Roles, AgeFacts: manifest.Analyzers.AgeFacts,
			PersonRelations: manifest.Analyzers.PersonRelations, Events: manifest.Analyzers.Events,
			Discourse: manifest.Analyzers.Discourse,
		}
		bundle.Status = scene.Status
		bundle.RejectedItems = scene.RejectedItems
		if scene.Reason != nil {
			bundle.Reason = *scene.Reason
		}
		if scene.Entities == nil || scene.EntityFacts == nil || scene.Identities == nil || scene.Relations.CoMentions == nil ||
			scene.Relations.AssertedRelations == nil || scene.NumericFacts == nil || scene.TemporalFacts == nil || scene.SemanticGroups == nil ||
			scene.Bullets == nil || scene.ShortPhrases == nil || scene.LongPhrases == nil ||
			scene.Quotes == nil || scene.ImportantWords == nil || scene.ImportantSegments == nil ||
			scene.ComposedImageQueries == nil || scene.ContextualImageQueries == nil || scene.VisualProposals == nil || scene.MicroSummaries == nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q omits required manifest collections", scene.SceneID)
		}
		if scene.Status != "valid" {
			bundles[scene.SceneID] = bundle
			continue
		}
		if bundle.Places, err = projectSemanticPlaces(source, scene.Places); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q places: %w", scene.SceneID, err)
		}
		if bundle.Routes, err = projectSemanticRoutes(source, bundle.Places, scene.Routes); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q routes: %w", scene.SceneID, err)
		}
		if bundle.PersonRoles, err = projectSemanticPersonRoles(source, scene.Identities, scene.PersonRoles); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q person roles: %w", scene.SceneID, err)
		}
		if bundle.AgeFacts, err = projectSemanticPersonAges(source, scene.Identities, scene.AgeFacts); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q age facts: %w", scene.SceneID, err)
		}
		if bundle.PersonRelations, err = projectSemanticPersonRelations(source, scene.Identities, scene.PersonRelations); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q person relations: %w", scene.SceneID, err)
		}
		if bundle.Entities, err = projectSemanticEntities(source, scene.Entities); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q entities: %w", scene.SceneID, err)
		}
		if bundle.EntityFacts, err = projectSemanticEntityFacts(source, scene.EntityFacts); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q entity facts: %w", scene.SceneID, err)
		}
		if bundle.Identities, err = projectSemanticIdentities(texts, scene.Identities); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q identities: %w", scene.SceneID, err)
		}
		if bundle.Relations, err = projectSemanticRelations(source, scene.Relations); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q relations: %w", scene.SceneID, err)
		}
		bundle.Bullets = projectSemanticBullets(source, scene.Bullets)
		if bundle.ShortPhrases, err = projectSemanticText(source, scene.ShortPhrases); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q short phrases: %w", scene.SceneID, err)
		}
		if bundle.LongPhrases, err = projectSemanticText(source, scene.LongPhrases); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q long phrases: %w", scene.SceneID, err)
		}
		if bundle.Quotes, err = projectSemanticText(source, scene.Quotes); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q quotes: %w", scene.SceneID, err)
		}
		if bundle.ImportantWords, err = projectSemanticText(source, scene.ImportantWords); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q important words: %w", scene.SceneID, err)
		}
		if bundle.ImportantSegments, err = projectSemanticImportantSegments(source, scene.ImportantSegments); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q important segments: %w", scene.SceneID, err)
		}
		if bundle.ComposedImageQueries, err = projectSemanticComposedImageQueries(source, scene.ComposedImageQueries); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q composed image queries: %w", scene.SceneID, err)
		}
		if bundle.ContextualImageQueries, err = projectSemanticContextualImageQueries(source, scene.ContextualImageQueries); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q contextual image queries: %w", scene.SceneID, err)
		}
		if bundle.Proposals, err = projectSemanticProposals(source, scene.VisualProposals); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q proposals: %w", scene.SceneID, err)
		}
		if bundle.Groups, err = projectSemanticGroups(scene.SceneID, texts, scene.SemanticGroups); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q groups: %w", scene.SceneID, err)
		}
		if bundle.MicroSummaries, err = projectSemanticMicroSummaries(source, scene.MicroSummaries); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q micro summaries: %w", scene.SceneID, err)
		}
		if bundle.Charts, err = projectSemanticCharts(source, scene.VisualProposals); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q: %w", scene.SceneID, err)
		}
		if bundle.Temporal, err = projectSemanticTemporal(source, scene.TemporalFacts); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q: %w", scene.SceneID, err)
		}
		if bundle.Numeric, err = projectSemanticNumeric(source, scene.NumericFacts); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q: %w", scene.SceneID, err)
		}
		if scene.Quantities != nil || scene.QuantityComparisons != nil {
			if bundle.Quantities, bundle.QuantityComparisons, err = projectSemanticQuantities(source, scene.Quantities, scene.QuantityComparisons); err != nil {
				return nil, nil, fmt.Errorf("semantic-engine: scene %q quantities: %w", scene.SceneID, err)
			}
		}
		bundles[scene.SceneID] = bundle
	}
	// The linker sees the complete script after all scene identities have
	// registered. Publish that single authoritative projection to every
	// bundle, including bundles for earlier scenes.
	for _, bundle := range bundles {
		bundle.Identities = append([]scriptgen.SemanticIdentity(nil), linkedIdentities...)
	}
	// Reference validation runs after the document-level identity projection:
	// a scene may legally mention an entity whose linked identity is registered
	// from another scene, and the per-scene identity slice cannot see it yet.
	for sceneID, bundle := range bundles {
		if err := validateSemanticReferences(bundle); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q references: %w", sceneID, err)
		}
	}
	crossSceneRoutes, err := projectSemanticCrossSceneRoutes(texts, manifest.Scenes, manifest.CrossSceneRoutes)
	if err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: cross-scene routes: %w", err)
	}
	for _, bundle := range bundles {
		bundle.CrossSceneRoutes = crossSceneRoutes
	}
	events, err := projectSemanticEvents(texts, manifest.Scenes, manifest.Entities, manifest.Events)
	if err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: events: %w", err)
	}
	for _, bundle := range bundles {
		bundle.Events = events
	}
	discourse, err := projectSemanticDiscourse(texts, manifest.Entities, manifest.Discourse)
	if err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: discourse: %w", err)
	}
	for _, bundle := range bundles {
		bundle.Discourse = discourse
	}
	if err := validateSemanticScriptViews(texts, manifest.StoryGraph, manifest.VisualCoverage, bundles); err != nil {
		return nil, nil, fmt.Errorf("semantic-engine: %w", err)
	}
	consolidation, err := projectSemanticConsolidation(texts, manifest.VisualConsolidation, bundles)
	if err != nil {
		return nil, nil, err
	}
	feedback, err := projectSemanticSearchFeedback(texts, manifest.SearchFeedback, bundles)
	if err != nil {
		return nil, nil, err
	}
	for _, bundle := range bundles {
		bundle.StoryGraph = manifest.StoryGraph
		bundle.VisualCoverage = manifest.VisualCoverage
		bundle.Consolidation = consolidation
		bundle.SearchFeedback = feedback
	}
	var editorialResult *scriptpkg.PhraseImpactResult
	if editorial != nil {
		if manifest.Editorial == nil {
			return nil, nil, fmt.Errorf("semantic-engine: requested editorial products are missing from manifest")
		}
		transcript, ok := editorial.Request["transcript"].(string)
		if !ok || strings.TrimSpace(transcript) == "" {
			return nil, nil, fmt.Errorf("semantic-engine: editorial request has no transcript")
		}
		result := scriptpkg.PhraseImpactResult{Summary: manifest.Editorial.Summary, ChapterManifest: manifest.Editorial.ChapterManifest}
		result.BulletDetails = append([]scriptpkg.EditorialBulletPoint(nil), manifest.Editorial.BulletPoints...)
		for _, bullet := range manifest.Editorial.BulletPoints {
			result.BulletPoints = append(result.BulletPoints, bullet.Text)
		}
		result.HeavySentences = manifest.Editorial.HeavySentences
		result.SceneHighlights, result.SceneHighlightsCertified = validateSceneHighlights(transcript, manifest.Editorial.SceneHighlights)
		result.Timings = manifest.Editorial.Timings
		editorialResult = &result
	}
	return bundles, editorialResult, nil
}

// Extract adapts the unified semantic worker to the existing SceneIR/asset
// search port. It intentionally analyses the scene in its own document
// context: a scene-text-only cache can leak coreference and entity-linking
// decisions between scripts that happen to contain the same sentence.
func (a *SemanticEngineAdapter) Extract(
	ctx context.Context,
	language string,
	sourceText string,
	_ int,
) ([]scriptgen.VisualEntity, error) {
	bundle, err := a.SemanticCandidates(ctx, language, sourceText, 0)
	if err != nil {
		return nil, err
	}
	out := make([]scriptgen.VisualEntity, 0, len(bundle.Entities))
	identitiesByID := make(map[string]scriptgen.SemanticIdentity, len(bundle.Identities))
	for _, identity := range bundle.Identities {
		identitiesByID[identity.ID] = identity
	}
	for _, entity := range bundle.Entities {
		canonicalID := ""
		role := ""
		var searchIntents []string
		editorialDecision := false
		editorialSurface, editorialType := "", ""
		promptEcho, visualAdmission := false, false
		if entity.IdentityID != "" {
			canonicalID = "local:" + bundle.Fingerprint + ":" + entity.IdentityID
			identity, ok := identitiesByID[entity.IdentityID]
			if !ok {
				return nil, fmt.Errorf("semantic-engine: entity %q references missing linked identity %q", entity.Text, entity.IdentityID)
			}
			role = strings.TrimSpace(identity.Role)
			if role != "named_entity" && role != "visual_concept" {
				return nil, fmt.Errorf("semantic-engine: identity %q has unsupported role %q", entity.IdentityID, role)
			}
			editorialDecision = identity.EditorialDecision
			editorialSurface, editorialType = identity.EditorialSurface, identity.EditorialType
			promptEcho, visualAdmission = identity.PromptEcho, identity.VisualAdmission
			if identity.CanonicalID != "" {
				canonicalID = identity.CanonicalID
			}
			searchIntents = make([]string, 0, len(identity.SearchIntents))
			for _, intent := range identity.SearchIntents {
				searchIntents = append(searchIntents, intent.Query)
			}
		}
		out = append(out, scriptgen.VisualEntity{
			Text: entity.Text, Type: scriptpkg.EntityType(entity.Kind), Score: float32(entity.Score),
			Start: entity.Start, End: entity.End, Evidence: entity.Evidence, IdentityID: entity.IdentityID, CanonicalID: canonicalID,
			Role:              role,
			SearchIntents:     append([]string(nil), searchIntents...),
			EntityIndex:       bundle.Analyzers.EntityIndex,
			EditorialDecision: editorialDecision,
			EditorialSurface:  editorialSurface,
			EditorialType:     editorialType,
			PromptEcho:        promptEcho,
			VisualAdmission:   visualAdmission,
		})
	}
	return out, nil
}

func validateSemanticReferences(bundle *scriptgen.SemanticCandidateBundle) error {
	ids := make(map[string]struct{}, len(bundle.Identities))
	for _, identity := range bundle.Identities {
		ids[identity.ID] = struct{}{}
	}
	known := func(id string) bool { _, ok := ids[id]; return ok }
	for _, entity := range bundle.Entities {
		if !known(entity.IdentityID) {
			return fmt.Errorf("entity %q references unknown identity %q", entity.Text, entity.IdentityID)
		}
	}
	for _, fact := range bundle.EntityFacts {
		if !known(fact.SubjectEntityID) {
			return fmt.Errorf("entity fact %q references unknown subject %q", fact.ID, fact.SubjectEntityID)
		}
		if fact.TargetEntityID != "" && !known(fact.TargetEntityID) {
			return fmt.Errorf("entity fact %q references unknown target %q", fact.ID, fact.TargetEntityID)
		}
	}
	for _, relation := range bundle.Relations.CoMentions {
		if !known(relation.LeftID) || !known(relation.RightID) {
			return fmt.Errorf("co-mention references an unknown identity")
		}
	}
	for _, relation := range bundle.Relations.AssertedRelations {
		if !known(relation.SourceID) || !known(relation.TargetID) {
			return fmt.Errorf("asserted relation references an unknown identity")
		}
	}
	for _, proposal := range bundle.Proposals {
		for _, id := range proposal.EntityIDs {
			if !known(id) {
				return fmt.Errorf("visual proposal references unknown identity %q", id)
			}
		}
	}
	facts := make(map[string]struct{}, len(bundle.EntityFacts))
	for _, fact := range bundle.EntityFacts {
		facts[fact.ID] = struct{}{}
	}
	for _, fact := range bundle.EntityFacts {
		for _, id := range fact.VisualConceptIDs {
			if !known(id) {
				return fmt.Errorf("entity fact %q references unknown visual concept %q", fact.ID, id)
			}
		}
	}
	for _, summary := range bundle.MicroSummaries {
		if summary.SubjectEntityID != "" && !known(summary.SubjectEntityID) {
			return fmt.Errorf("micro summary %q references unknown subject %q", summary.ID, summary.SubjectEntityID)
		}
		for _, id := range summary.EvidenceFactIDs {
			if _, ok := facts[id]; !ok {
				return fmt.Errorf("micro summary %q references unknown fact %q", summary.ID, id)
			}
		}
	}
	return nil
}

// SemanticCandidates keeps the per-scene contract for callers that have no
// batch. It is one engine call for one scene, never a second analysis path.
func (a *SemanticEngineAdapter) SemanticCandidates(ctx context.Context, language, sourceText string, _ int) (*scriptgen.SemanticCandidateBundle, error) {
	bundles, err := a.SemanticBundles(ctx, language, []scriptgen.SemanticSceneInput{{SceneID: "scene", Text: sourceText}})
	if err != nil {
		return nil, err
	}
	bundle, ok := bundles["scene"]
	if !ok {
		return nil, fmt.Errorf("semantic-engine: manifest omitted the requested scene")
	}
	return bundle, nil
}

var _ scriptgen.SceneSemanticAnalyzer = (*SemanticEngineAdapter)(nil)
var _ scriptgen.SceneSemanticBatchAnalyzer = (*SemanticEngineAdapter)(nil)
