package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// SemanticEngineAdapter is the Go boundary of the unified Rust semantic
// engine (`semantic_manifest.v2`). ONE request carries every scene of a
// script and returns every grounded projection at once: bullets, chart
// values, temporal facts and numeric facts.
//
// This replaces the two per-category VisualNER calls (bullet and chart) for
// the same scene. The engine keeps every candidate; selection (top-N, chart
// admission, overlay budget) stays in PipelineGen.
type SemanticEngineAdapter struct {
	executor        *Executor
	embedder        passageBatchEmbedder
	entityCacheMu   sync.RWMutex
	entityCache     map[string][]scriptgen.SemanticEntityCandidate
	entityCacheFIFO []string
}

const semanticEntityCacheCapacity = 512
const semanticEngineOutputLimit = 8 << 20

// NewSemanticEngineExecutor reserves bounded response headroom for whole-
// script manifests while keeping ordinary media protocol caps conservative.
func NewSemanticEngineExecutor(binaryPath, ffmpegPath string, log *zap.Logger) *Executor {
	return NewExecutorWithOutputLimit(binaryPath, ffmpegPath, configuredExecutionSlots(), semanticEngineOutputLimit, log)
}

// NewSemanticEngineAdapter binds the adapter to a dedicated executor for
// `bin/semantic-engine`. The executor is separate from the media executor so
// a semantic batch never queues behind renders.
func NewSemanticEngineAdapter(executor *Executor) (*SemanticEngineAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("semantic-engine: executor is required")
	}
	return &SemanticEngineAdapter{executor: executor, entityCache: make(map[string][]scriptgen.SemanticEntityCandidate)}, nil
}

const semanticManifestV2 = "semantic_manifest.v2"

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
		return nil, nil, fmt.Errorf("semantic-engine: worker: %s", strings.TrimSpace(response.Error))
	}
	manifest := response.Manifest
	if manifest == nil {
		return nil, nil, fmt.Errorf("semantic-engine: response carries no manifest")
	}
	if manifest.SchemaVersion != semanticManifestV2 {
		return nil, nil, fmt.Errorf("semantic-engine: unsupported schema_version %q", manifest.SchemaVersion)
	}
	if manifest.Language != language || manifest.Fingerprint == "" || manifest.Analyzers.VisualNER == "" ||
		manifest.Analyzers.SemanticCore == "" || manifest.Analyzers.PhraseImpact == "" {
		return nil, nil, fmt.Errorf("semantic-engine: manifest metadata is incomplete or does not match request language")
	}
	if len(manifest.Scenes) != len(request.Scenes) {
		return nil, nil, fmt.Errorf("semantic-engine: manifest has %d scenes, request had %d", len(manifest.Scenes), len(request.Scenes))
	}

	texts := make(map[string]string, len(request.Scenes))
	for _, scene := range request.Scenes {
		texts[scene.SceneID] = scene.Text
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
			PhraseImpact: manifest.Analyzers.PhraseImpact,
		}
		bundle.Status = scene.Status
		bundle.RejectedItems = scene.RejectedItems
		if scene.Reason != nil {
			bundle.Reason = *scene.Reason
		}
		if scene.Entities == nil || scene.EntityFacts == nil || scene.Identities == nil || scene.Relations.CoMentions == nil ||
			scene.Relations.AssertedRelations == nil || scene.NumericFacts == nil || scene.TemporalFacts == nil || scene.SemanticGroups == nil ||
			scene.Bullets == nil || scene.ShortPhrases == nil || scene.LongPhrases == nil ||
			scene.Quotes == nil || scene.ImportantWords == nil || scene.VisualProposals == nil || scene.MicroSummaries == nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q omits required manifest collections", scene.SceneID)
		}
		if scene.Status != "valid" {
			bundles[scene.SceneID] = bundle
			a.cacheEntities(manifest.Language, source, nil)
			continue
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
		if bundle.Proposals, err = projectSemanticProposals(source, scene.VisualProposals); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q proposals: %w", scene.SceneID, err)
		}
		if bundle.Groups, err = projectSemanticGroups(scene.SceneID, texts, scene.SemanticGroups); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q groups: %w", scene.SceneID, err)
		}
		if bundle.MicroSummaries, err = projectSemanticMicroSummaries(source, scene.MicroSummaries); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q micro summaries: %w", scene.SceneID, err)
		}
		if err := validateSemanticReferences(bundle); err != nil {
			return nil, nil, fmt.Errorf("semantic-engine: scene %q references: %w", scene.SceneID, err)
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
		bundles[scene.SceneID] = bundle
		a.cacheEntities(manifest.Language, source, bundle.Entities)
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

func semanticEntityCacheKey(language, source string) string {
	return strings.ToLower(strings.TrimSpace(language)) + "\x00" + source
}

func cloneSemanticEntities(entities []scriptgen.SemanticEntityCandidate) []scriptgen.SemanticEntityCandidate {
	return append([]scriptgen.SemanticEntityCandidate(nil), entities...)
}

func (a *SemanticEngineAdapter) cacheEntities(language, source string, entities []scriptgen.SemanticEntityCandidate) {
	key := semanticEntityCacheKey(language, source)
	a.entityCacheMu.Lock()
	defer a.entityCacheMu.Unlock()
	if _, exists := a.entityCache[key]; !exists {
		a.entityCacheFIFO = append(a.entityCacheFIFO, key)
		if len(a.entityCacheFIFO) > semanticEntityCacheCapacity {
			delete(a.entityCache, a.entityCacheFIFO[0])
			a.entityCacheFIFO = a.entityCacheFIFO[1:]
		}
	}
	a.entityCache[key] = cloneSemanticEntities(entities)
}

func (a *SemanticEngineAdapter) cachedEntities(language, source string) ([]scriptgen.SemanticEntityCandidate, bool) {
	a.entityCacheMu.RLock()
	entities, ok := a.entityCache[semanticEntityCacheKey(language, source)]
	cloned := cloneSemanticEntities(entities)
	a.entityCacheMu.RUnlock()
	return cloned, ok
}

// Extract adapts the unified semantic worker to the existing SceneIR/asset
// search port. When the same source text was already included in a script
// batch, it reuses those entities without another Rust request.
func (a *SemanticEngineAdapter) Extract(
	ctx context.Context,
	language string,
	sourceText string,
	_ int,
) ([]scriptgen.VisualEntity, error) {
	entities, ok := a.cachedEntities(language, sourceText)
	if !ok {
		bundle, err := a.SemanticCandidates(ctx, language, sourceText, 0)
		if err != nil {
			return nil, err
		}
		entities = cloneSemanticEntities(bundle.Entities)
	}
	out := make([]scriptgen.VisualEntity, 0, len(entities))
	for _, entity := range entities {
		out = append(out, scriptgen.VisualEntity{
			Text: entity.Text, Type: scriptpkg.EntityType(entity.Kind), Score: float32(entity.Score),
			Start: entity.Start, End: entity.End, Evidence: entity.Evidence,
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
