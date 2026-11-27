package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	semantic "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
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
	// semanticManifestV4 normalizes the identity registry: one document table
	// plus per-scene `identity_ids`. It is the opt-in cutover version; v3 stays
	// the default until every consumer reads the table.
	semanticManifestV4 = "semantic_manifest.v4"
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
func (a *SemanticEngineAdapter) SemanticBundles(ctx context.Context, language string, scenes []semantic.SemanticSceneInput) (map[string]*semantic.SemanticCandidateBundle, error) {
	result, _, err := a.semanticBundles(ctx, language, scenes, nil)
	if err != nil {
		return nil, err
	}
	return result.Scenes, nil
}

// SemanticScript is the whole-script projection: one identity registry plus the
// per-scene bundles keyed by scene id. Callers that plan production need the
// script-level identities, so they must not re-merge them from the scenes.
func (a *SemanticEngineAdapter) SemanticScript(ctx context.Context, language string, scenes []semantic.SemanticSceneInput) (*semantic.ScriptSemanticResult, error) {
	result, _, err := a.semanticBundles(ctx, language, scenes, nil)
	return result, err
}

func (a *SemanticEngineAdapter) semanticBundles(ctx context.Context, language string, scenes []semantic.SemanticSceneInput, editorial *semanticEngineEditorial) (*semantic.ScriptSemanticResult, *scriptpkg.PhraseImpactResult, error) {
	if a == nil || a.executor == nil {
		return nil, nil, fmt.Errorf("semantic-engine: executor is not configured")
	}
	request, texts, err := buildSemanticEngineRequest(language, scenes, editorial)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := a.executeSemanticEngineRequest(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	result, err := buildSemanticCandidateBundles(manifest, request, texts)
	if err != nil {
		return nil, nil, err
	}
	if err := applySemanticDocumentProjections(manifest, texts, result); err != nil {
		return nil, nil, err
	}
	editorialResult, err := buildSemanticEditorialResult(editorial, manifest)
	if err != nil {
		return nil, nil, err
	}
	return result, editorialResult, nil
}

func buildSemanticEngineRequest(language string, scenes []semantic.SemanticSceneInput, editorial *semanticEngineEditorial) (semanticEngineRequest, map[string]string, error) {
	language = strings.TrimSpace(language)
	if language == "" {
		return semanticEngineRequest{}, nil, fmt.Errorf("semantic-engine: language is required")
	}
	if len(scenes) == 0 {
		return semanticEngineRequest{}, nil, fmt.Errorf("semantic-engine: at least one scene is required")
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
			return semanticEngineRequest{}, nil, fmt.Errorf("semantic-engine: scene with empty id")
		}
		if _, duplicate := seen[id]; duplicate {
			return semanticEngineRequest{}, nil, fmt.Errorf("semantic-engine: duplicate scene id %q", id)
		}
		seen[id] = struct{}{}
		request.Scenes = append(request.Scenes, semanticEngineScene{SceneID: id, Text: scene.Text})
	}
	texts := make(map[string]string, len(request.Scenes))
	for _, scene := range request.Scenes {
		texts[scene.SceneID] = scene.Text
	}
	return request, texts, nil
}

func (a *SemanticEngineAdapter) executeSemanticEngineRequest(ctx context.Context, request semanticEngineRequest) (*semanticEngineManifest, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: encode: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: execute: %w", err)
	}
	var response semanticEngineResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine: decode: %w", err)
	}
	if !response.OK {
		return nil, workerFailure(response)
	}
	if response.Cached && a.executor.log != nil {
		a.executor.log.Debug("semantic-engine: manifest served from the persistent cache",
			zap.String("language", request.Language), zap.Int("scenes", len(request.Scenes)))
	}
	manifest := response.Manifest
	if manifest == nil {
		return nil, fmt.Errorf("semantic-engine: response carries no manifest")
	}
	switch manifest.SchemaVersion {
	case semanticManifestV3:
		// Historical shape: every scene carries the cumulative registry.
	case semanticManifestV4:
		// Normalized shape: expand the document table into the per-scene view
		// the existing projection consumes. This is a bounded cutover shim:
		// each scene receives only the ids it references, never the cumulative
		// registry. Remove it when projectSemanticScene* reads the table directly.
		if err := hydrateNormalizedIdentities(manifest); err != nil {
			return nil, err
		}
	default:
		// Version routing is explicit: an unknown version is never decoded as
		// v3 just because the fields happen to be present.
		return nil, fmt.Errorf("semantic-engine: unsupported schema_version %q", manifest.SchemaVersion)
	}
	if manifest.Language != request.Language || manifest.Fingerprint == "" || manifest.Analyzers.VisualNER == "" ||
		manifest.Analyzers.SemanticCore == "" || manifest.Analyzers.PhraseImpact == "" {
		return nil, fmt.Errorf("semantic-engine: manifest metadata is incomplete or does not match request language")
	}
	if strings.TrimSpace(manifest.Analyzers.EntityIndex) == "" {
		return nil, fmt.Errorf("semantic-engine: v3 manifest omits entity index provenance")
	}
	if manifest.Entities == nil {
		return nil, fmt.Errorf("semantic-engine: v3 manifest omits its entity projection")
	}
	if len(manifest.Scenes) != len(request.Scenes) {
		return nil, fmt.Errorf("semantic-engine: manifest has %d scenes, request had %d", len(manifest.Scenes), len(request.Scenes))
	}
	return manifest, nil
}

// hydrateNormalizedIdentities validates a v4 document table and expands it
// into the per-scene identity list the projection consumes. The checks are
// fail-closed: a v4 manifest with a dangling id, a duplicate identity, a
// per-scene copy or an unreferenced identity is a contract violation, not a
// value to guess.
func hydrateNormalizedIdentities(manifest *semanticEngineManifest) error {
	if manifest.Identities == nil {
		return fmt.Errorf("semantic-engine: v4 manifest omits the global identities table")
	}
	byID := make(map[string]semanticEngineIdentity, len(manifest.Identities))
	for _, identity := range manifest.Identities {
		if strings.TrimSpace(identity.ID) == "" {
			return fmt.Errorf("semantic-engine: v4 manifest carries an identity with an empty id")
		}
		if _, duplicate := byID[identity.ID]; duplicate {
			return fmt.Errorf("semantic-engine: v4 manifest duplicates identity %q", identity.ID)
		}
		byID[identity.ID] = identity
	}
	referenced := make(map[string]struct{}, len(byID))
	for index := range manifest.Scenes {
		scene := &manifest.Scenes[index]
		if scene.Identities != nil {
			return fmt.Errorf("semantic-engine: v4 scene %q carries a per-scene identity copy", scene.SceneID)
		}
		if scene.IdentityIDs == nil {
			return fmt.Errorf("semantic-engine: v4 scene %q omits identity_ids", scene.SceneID)
		}
		seen := make(map[string]struct{}, len(scene.IdentityIDs))
		scene.Identities = make([]semanticEngineIdentity, 0, len(scene.IdentityIDs))
		for _, id := range scene.IdentityIDs {
			identity, ok := byID[id]
			if !ok {
				return fmt.Errorf("semantic-engine: v4 scene %q references unknown identity %q", scene.SceneID, id)
			}
			if _, duplicate := seen[id]; duplicate {
				return fmt.Errorf("semantic-engine: v4 scene %q repeats identity %q", scene.SceneID, id)
			}
			seen[id] = struct{}{}
			referenced[id] = struct{}{}
			scene.Identities = append(scene.Identities, identity)
		}
	}
	for id := range byID {
		if _, ok := referenced[id]; !ok {
			return fmt.Errorf("semantic-engine: v4 identity %q is not referenced by any scene", id)
		}
	}
	return nil
}

func buildSemanticCandidateBundles(manifest *semanticEngineManifest, request semanticEngineRequest, texts map[string]string) (*semantic.ScriptSemanticResult, error) {
	linkedIdentities, err := projectSemanticLinkedEntities(texts, manifest.Entities)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: linked entities: %w", err)
	}
	if err := validateLinkedEntityCoverage(manifest.Scenes, linkedIdentities); err != nil {
		return nil, fmt.Errorf("semantic-engine: linked entity coverage: %w", err)
	}
	knownIdentities := make(map[string]struct{}, len(linkedIdentities))
	for _, identity := range linkedIdentities {
		knownIdentities[identity.ID] = struct{}{}
	}
	result := &semantic.ScriptSemanticResult{
		Identities: linkedIdentities,
		Scenes:     make(map[string]*semantic.SemanticCandidateBundle, len(manifest.Scenes)),
	}
	for index, scene := range manifest.Scenes {
		source, ok := texts[scene.SceneID]
		if !ok {
			return nil, fmt.Errorf("semantic-engine: manifest scene %q was not requested", scene.SceneID)
		}
		if scene.SceneID != request.Scenes[index].SceneID {
			return nil, fmt.Errorf("semantic-engine: manifest scene order changed at %d", index)
		}
		bundle := newSemanticCandidateBundle(manifest, scene)
		if semanticSceneCollectionsMissing(scene) {
			return nil, fmt.Errorf("semantic-engine: scene %q omits required manifest collections", scene.SceneID)
		}
		if scene.Status != "valid" {
			result.Scenes[scene.SceneID] = bundle
			continue
		}
		if err := projectSemanticSceneBundle(bundle, scene, source, texts); err != nil {
			return nil, err
		}
		bundle.IdentityIDs = referencedIdentityIDs(bundle, knownIdentities)
		result.Scenes[scene.SceneID] = bundle
	}
	// Reference validation runs against the script registry: a scene may legally
	// mention an entity whose linked identity is registered from another scene.
	for sceneID, bundle := range result.Scenes {
		if err := validateSemanticReferences(bundle, knownIdentities); err != nil {
			return nil, fmt.Errorf("semantic-engine: scene %q references: %w", sceneID, err)
		}
	}
	return result, nil
}

// referencedIdentityIDs returns the canonical ids one scene actually uses. It
// keeps the scene self-describing without copying the script registry.
func referencedIdentityIDs(bundle *semantic.SemanticCandidateBundle, known map[string]struct{}) []string {
	seen := make(map[string]struct{})
	add := func(id string) {
		if _, ok := known[id]; !ok {
			return
		}
		seen[id] = struct{}{}
	}
	for _, entity := range bundle.Entities {
		add(entity.IdentityID)
	}
	for _, fact := range bundle.EntityFacts {
		add(fact.SubjectEntityID)
		add(fact.TargetEntityID)
		for _, id := range fact.VisualConceptIDs {
			add(id)
		}
	}
	for _, relation := range bundle.Relations.CoMentions {
		add(relation.LeftID)
		add(relation.RightID)
	}
	for _, relation := range bundle.Relations.AssertedRelations {
		add(relation.SourceID)
		add(relation.TargetID)
	}
	for _, proposal := range bundle.Proposals {
		for _, id := range proposal.EntityIDs {
			add(id)
		}
	}
	for _, summary := range bundle.MicroSummaries {
		add(summary.SubjectEntityID)
	}
	for _, role := range bundle.PersonRoles {
		add(role.PersonID)
		add(role.OrganizationID)
		add(role.PlaceID)
	}
	for _, age := range bundle.AgeFacts {
		add(age.PersonID)
	}
	for _, relation := range bundle.PersonRelations {
		add(relation.FromID)
		add(relation.ToID)
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func newSemanticCandidateBundle(manifest *semanticEngineManifest, scene semanticEngineSceneManifest) *semantic.SemanticCandidateBundle {
	bundle := &semantic.SemanticCandidateBundle{}
	bundle.SchemaVersion = manifest.SchemaVersion
	bundle.Language = manifest.Language
	bundle.Fingerprint = manifest.Fingerprint
	bundle.Analyzers = semantic.SemanticAnalyzerVersions{
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
	return bundle
}

func semanticSceneCollectionsMissing(scene semanticEngineSceneManifest) bool {
	return scene.Entities == nil || scene.EntityFacts == nil || scene.Identities == nil || scene.Relations.CoMentions == nil ||
		scene.Relations.AssertedRelations == nil || scene.NumericFacts == nil || scene.TemporalFacts == nil || scene.SemanticGroups == nil ||
		scene.Bullets == nil || scene.ShortPhrases == nil || scene.LongPhrases == nil ||
		scene.Quotes == nil || scene.ImportantWords == nil || scene.ImportantSegments == nil ||
		scene.ComposedImageQueries == nil || scene.ContextualImageQueries == nil || scene.VisualProposals == nil || scene.MicroSummaries == nil
}

func projectSemanticSceneBundle(bundle *semantic.SemanticCandidateBundle, scene semanticEngineSceneManifest, source string, texts map[string]string) error {
	if err := projectSemanticSceneLanguage(bundle, scene, source, texts); err != nil {
		return err
	}
	if err := projectSemanticSceneText(bundle, scene, source); err != nil {
		return err
	}
	if err := projectSemanticSceneVisuals(bundle, scene, source, texts); err != nil {
		return err
	}
	return projectSemanticSceneAnalysis(bundle, scene, source)
}

func projectSemanticSceneLanguage(bundle *semantic.SemanticCandidateBundle, scene semanticEngineSceneManifest, source string, texts map[string]string) error {
	var err error
	if bundle.Places, err = projectSemanticPlaces(source, scene.Places); err != nil {
		return fmt.Errorf("semantic-engine: scene %q places: %w", scene.SceneID, err)
	}
	if bundle.Routes, err = projectSemanticRoutes(source, bundle.Places, scene.Routes); err != nil {
		return fmt.Errorf("semantic-engine: scene %q routes: %w", scene.SceneID, err)
	}
	if bundle.PersonRoles, err = projectSemanticPersonRoles(source, scene.Identities, scene.PersonRoles); err != nil {
		return fmt.Errorf("semantic-engine: scene %q person roles: %w", scene.SceneID, err)
	}
	if bundle.AgeFacts, err = projectSemanticPersonAges(source, scene.Identities, scene.AgeFacts); err != nil {
		return fmt.Errorf("semantic-engine: scene %q age facts: %w", scene.SceneID, err)
	}
	if bundle.PersonRelations, err = projectSemanticPersonRelations(source, scene.Identities, scene.PersonRelations); err != nil {
		return fmt.Errorf("semantic-engine: scene %q person relations: %w", scene.SceneID, err)
	}
	if bundle.Entities, err = projectSemanticEntities(source, scene.Entities); err != nil {
		return fmt.Errorf("semantic-engine: scene %q entities: %w", scene.SceneID, err)
	}
	if bundle.EntityFacts, err = projectSemanticEntityFacts(source, scene.EntityFacts); err != nil {
		return fmt.Errorf("semantic-engine: scene %q entity facts: %w", scene.SceneID, err)
	}
	// The identity registry is projected once at script level from
	// manifest.Entities. The per-scene wire snapshot is deliberately not
	// copied into the bundle; only referenced ids are recorded later.
	if bundle.Relations, err = projectSemanticRelations(source, scene.Relations); err != nil {
		return fmt.Errorf("semantic-engine: scene %q relations: %w", scene.SceneID, err)
	}
	return nil
}

func projectSemanticSceneText(bundle *semantic.SemanticCandidateBundle, scene semanticEngineSceneManifest, source string) error {
	var err error
	bundle.Bullets = projectSemanticBullets(source, scene.Bullets)
	if bundle.ShortPhrases, err = projectSemanticText(source, scene.ShortPhrases); err != nil {
		return fmt.Errorf("semantic-engine: scene %q short phrases: %w", scene.SceneID, err)
	}
	if bundle.LongPhrases, err = projectSemanticText(source, scene.LongPhrases); err != nil {
		return fmt.Errorf("semantic-engine: scene %q long phrases: %w", scene.SceneID, err)
	}
	if bundle.Quotes, err = projectSemanticText(source, scene.Quotes); err != nil {
		return fmt.Errorf("semantic-engine: scene %q quotes: %w", scene.SceneID, err)
	}
	if bundle.ImportantWords, err = projectSemanticText(source, scene.ImportantWords); err != nil {
		return fmt.Errorf("semantic-engine: scene %q important words: %w", scene.SceneID, err)
	}
	if bundle.ImportantSegments, err = projectSemanticImportantSegments(source, scene.ImportantSegments); err != nil {
		return fmt.Errorf("semantic-engine: scene %q important segments: %w", scene.SceneID, err)
	}
	return nil
}

func projectSemanticSceneVisuals(bundle *semantic.SemanticCandidateBundle, scene semanticEngineSceneManifest, source string, texts map[string]string) error {
	var err error
	if bundle.ComposedImageQueries, err = projectSemanticComposedImageQueries(source, scene.ComposedImageQueries); err != nil {
		return fmt.Errorf("semantic-engine: scene %q composed image queries: %w", scene.SceneID, err)
	}
	if bundle.ContextualImageQueries, err = projectSemanticContextualImageQueries(source, scene.ContextualImageQueries); err != nil {
		return fmt.Errorf("semantic-engine: scene %q contextual image queries: %w", scene.SceneID, err)
	}
	if bundle.Proposals, err = projectSemanticProposals(source, scene.VisualProposals); err != nil {
		return fmt.Errorf("semantic-engine: scene %q proposals: %w", scene.SceneID, err)
	}
	if bundle.Groups, err = projectSemanticGroups(scene.SceneID, texts, scene.SemanticGroups); err != nil {
		return fmt.Errorf("semantic-engine: scene %q groups: %w", scene.SceneID, err)
	}
	if bundle.MicroSummaries, err = projectSemanticMicroSummaries(source, scene.MicroSummaries); err != nil {
		return fmt.Errorf("semantic-engine: scene %q micro summaries: %w", scene.SceneID, err)
	}
	return nil
}

func projectSemanticSceneAnalysis(bundle *semantic.SemanticCandidateBundle, scene semanticEngineSceneManifest, source string) error {
	var err error
	if bundle.Charts, err = projectSemanticCharts(source, scene.VisualProposals); err != nil {
		return fmt.Errorf("semantic-engine: scene %q: %w", scene.SceneID, err)
	}
	if bundle.Temporal, err = projectSemanticTemporal(source, scene.TemporalFacts); err != nil {
		return fmt.Errorf("semantic-engine: scene %q: %w", scene.SceneID, err)
	}
	if bundle.Numeric, err = projectSemanticNumeric(source, scene.NumericFacts); err != nil {
		return fmt.Errorf("semantic-engine: scene %q: %w", scene.SceneID, err)
	}
	if scene.Quantities != nil || scene.QuantityComparisons != nil {
		if bundle.Quantities, bundle.QuantityComparisons, err = projectSemanticQuantities(source, scene.Quantities, scene.QuantityComparisons); err != nil {
			return fmt.Errorf("semantic-engine: scene %q quantities: %w", scene.SceneID, err)
		}
	}
	return nil
}

func applySemanticDocumentProjections(manifest *semanticEngineManifest, texts map[string]string, result *semantic.ScriptSemanticResult) error {
	bundles := result.Scenes
	crossSceneRoutes, err := projectSemanticCrossSceneRoutes(texts, manifest.Scenes, manifest.CrossSceneRoutes)
	if err != nil {
		return fmt.Errorf("semantic-engine: cross-scene routes: %w", err)
	}
	for _, bundle := range bundles {
		bundle.CrossSceneRoutes = crossSceneRoutes
	}
	events, err := projectSemanticEvents(texts, manifest.Scenes, manifest.Entities, manifest.Events)
	if err != nil {
		return fmt.Errorf("semantic-engine: events: %w", err)
	}
	for _, bundle := range bundles {
		bundle.Events = events
	}
	discourse, err := projectSemanticDiscourse(texts, manifest.Entities, manifest.Discourse)
	if err != nil {
		// Discourse links are an optional enrichment over the grounded entity
		// and scene projections above. A malformed reference must not discard
		// otherwise valid scene analysis (or fail the whole video job); omit the
		// entire discourse surface so no ungrounded link can escape.
		discourse = nil
	}
	for _, bundle := range bundles {
		bundle.Discourse = discourse
	}
	if err := validateSemanticScriptViews(texts, manifest.StoryGraph, manifest.VisualCoverage, result); err != nil {
		return fmt.Errorf("semantic-engine: %w", err)
	}
	consolidation, err := projectSemanticConsolidation(texts, manifest.VisualConsolidation, bundles)
	if err != nil {
		return err
	}
	feedback, err := projectSemanticSearchFeedback(texts, manifest.SearchFeedback, bundles)
	if err != nil {
		return err
	}
	result.Consolidation = consolidation
	result.SearchFeedback = feedback
	for _, bundle := range bundles {
		bundle.StoryGraph = manifest.StoryGraph
		bundle.VisualCoverage = manifest.VisualCoverage
		// Shared read-only pointers, not copies: the script result is the
		// single owner of these projections.
		bundle.Consolidation = consolidation
		bundle.SearchFeedback = feedback
	}
	return nil
}

func buildSemanticEditorialResult(editorial *semanticEngineEditorial, manifest *semanticEngineManifest) (*scriptpkg.PhraseImpactResult, error) {
	if editorial == nil {
		return nil, nil
	}
	if manifest.Editorial == nil {
		return nil, fmt.Errorf("semantic-engine: requested editorial products are missing from manifest")
	}
	transcript, ok := editorial.Request["transcript"].(string)
	if !ok || strings.TrimSpace(transcript) == "" {
		return nil, fmt.Errorf("semantic-engine: editorial request has no transcript")
	}
	result := scriptpkg.PhraseImpactResult{Summary: manifest.Editorial.Summary, ChapterManifest: manifest.Editorial.ChapterManifest}
	result.BulletDetails = append([]scriptpkg.EditorialBulletPoint(nil), manifest.Editorial.BulletPoints...)
	for _, bullet := range manifest.Editorial.BulletPoints {
		result.BulletPoints = append(result.BulletPoints, bullet.Text)
	}
	result.HeavySentences = manifest.Editorial.HeavySentences
	result.SceneHighlights, result.SceneHighlightsCertified = validateSceneHighlights(transcript, manifest.Editorial.SceneHighlights)
	if len(manifest.Editorial.IntroAgenda) > 0 {
		if len(manifest.Editorial.IntroAgenda) < 3 || len(manifest.Editorial.IntroAgenda) > 5 {
			return nil, fmt.Errorf("semantic-engine: intro_agenda has %d topics, want 3-5", len(manifest.Editorial.IntroAgenda))
		}
		result.IntroAgenda = append([]scriptpkg.IntroTopic(nil), manifest.Editorial.IntroAgenda...)
		for _, topic := range result.IntroAgenda {
			if strings.TrimSpace(topic.Text) == "" || topic.SentenceIndex < 0 ||
				!groundedSpan(transcript, topic.StartByte, topic.EndByte, topic.Text) {
				return nil, fmt.Errorf("semantic-engine: intro topic %q is not grounded in the transcript", topic.Text)
			}
		}
	}
	result.Timings = manifest.Editorial.Timings
	return &result, nil
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
) ([]semantic.VisualEntity, error) {
	result, _, err := a.semanticBundles(ctx, language, []semantic.SemanticSceneInput{{SceneID: "scene", Text: sourceText}}, nil)
	if err != nil {
		return nil, err
	}
	bundle, ok := result.Scenes["scene"]
	if !ok {
		return nil, fmt.Errorf("semantic-engine: manifest omitted the requested scene")
	}
	out := make([]semantic.VisualEntity, 0, len(bundle.Entities))
	identitiesByID := result.IdentityByID()
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
		out = append(out, semantic.VisualEntity{
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

func validateSemanticReferences(bundle *semantic.SemanticCandidateBundle, knownIdentities map[string]struct{}) error {
	known := func(id string) bool { _, ok := knownIdentities[id]; return ok }
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
func (a *SemanticEngineAdapter) SemanticCandidates(ctx context.Context, language, sourceText string, _ int) (*semantic.SemanticCandidateBundle, error) {
	bundles, err := a.SemanticBundles(ctx, language, []semantic.SemanticSceneInput{{SceneID: "scene", Text: sourceText}})
	if err != nil {
		return nil, err
	}
	bundle, ok := bundles["scene"]
	if !ok {
		return nil, fmt.Errorf("semantic-engine: manifest omitted the requested scene")
	}
	return bundle, nil
}

var _ semantic.SceneSemanticAnalyzer = (*SemanticEngineAdapter)(nil)
var _ semantic.SceneSemanticBatchAnalyzer = (*SemanticEngineAdapter)(nil)
