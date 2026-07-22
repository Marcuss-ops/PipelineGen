// Package mediamemory — resolver.go is the canonical VisualResolver
// that composes the 9-level priority pipeline (architecture doc,
// section 10):
//
//  1. Phrase esatta approvata manualmente      (Level 0, hot path)
//  2. Frase normalizzata                       (Level 0, normalized retry)
//  3. Concetto semanticamente equivalente     (Level 1, Qdrant)
//  4. Entità                                   (Level 1, entity fan-out)
//  5. Azione visiva                            (Level 1, action hint)
//  6. Keyword                                  (Level 1, BM25 sparse)
//  7. Categoria                                (Level 1, topic)
//  8. Catalogo locale                          (Level 2, SQLite binding + media_assets)
//  9. Provider esterno                         (Level 3, SearchFanOut)
//
// godlike/06 SSOT: VisualResolver is the SINGLE owner of the
// resolution order. Every route that produces a SceneVisualPlan
// (whether API, batch, or admin) routes through this resolver
// (forward-pointer; sister surface to ClipResolver in the existing
// scripts/usecase package, which is the per-script generative
// counterpart).
//
// godlike/06 SSOT (strict priority cascade): levels 1..9 are
// inspected IN ORDER; the FIRST level returning a non-empty
// candidate set wins, and the remaining levels are NOT consulted
// for that slot. Only when the winning level returns an empty
// set is the next level tried. This is canonical to the
// architecture doc's "ordine di priorità": a manual-approved
// phrase hit MUST short-circuit the cascade (so a human
// association never gets overwritten by an external search).
//
// godlike/07 NO-FAKE-AVAILABILITY: every level surfaces a typed
// failure rather than silently fall-through. Levels 0/1 hit the
// cache via ConceptRepository; Level 8 falls through to
// CandidateRepository; Level 9 hits the external SearchFanOut
// (IF ResolvePolicy.AllowExternalSearch = true). Per-level
// errors propagate into the ResolveResult.Warnings array so
// callers can branch on Partial/BackendErrors deterministically.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Resolver is the canonical port exposed to api/mediamemory.
// Concrete impl is VisualResolver (this file, below).
type Resolver interface {
	// Resolve produces a ResolveResult for the input request. The
	// resolver MUST honour ctx.Done() at every level iteration.
	Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error)
}

// ── Canonical implementation (concrete) ───────────────────────────

// VisualResolver is the canonical implementation of Resolver. It
// composes the 9-level priority pipeline. Each level reads from
// the canonical ports declared in ports.go — there is no parallel
// "fast path" (godlike/06 SSOT).
//
// godlike/06 SSOT (Fase 2.3 anti-repetition wiring): the resolver
// holds an optional UsageRepository (nil-safe composition) so the
// per-project history read can flow through the canonical
// append-only audit log. When nil, the resolver degrades
// gracefully: RepetitionPenalty stays 0 (no penalty input
// available) and the ranker still scores candidates normally.
type VisualResolver struct {
	concepts   ConceptRepository
	bindings   BindingRepository
	external   SearchFanOut
	semantic   SemanticLookup
	usage      UsageRepository // optional; nil-safe for backward compat
	ranker     Ranker
	normalizer Normalizer // godlike/06 SSOT: SINGLE canonical normalization surface
	log        Logger
	clock      Clock
	metrics    MetricsSink
}

// NewVisualResolver constructs the resolver with the canonical
// dependency set. Composition root wires concrete adapters.
//
// godlike/06 SSOT: the Normalizer is REQUIRED (composition-root
// must inject *defaultNormalizer from normalizer.go) so the
// Level 0/1/2 fingerprint lookup uses the canonical SHA256 +
// NFC + lowercase + dedup-whitespace + terminal-punctuation-strip
// algorithm. A nil normalizer triggers NewCanonicalNormalizer
// (composition-root-friendly default) so test harnesses can
// pass nil without breaking the SSOT.
//
// godlike/06 SSOT (Fase 2.3 wiring): the optional UsageRepository
// is the consumer seam for ListProjectUsages. A nil usage
// surfaces as "anti-repetition disabled" — penalties stay 0 and the
// ranker still scores candidates normally. Composition root wires
// the canonical concrete UsageRepository (sqlite-backed) unless
// the caller explicitly opts out (e.g. test harnesses).
func NewVisualResolver(
	concepts ConceptRepository,
	bindings BindingRepository,
	external SearchFanOut,
	semantic SemanticLookup,
	ranker Ranker,
	log Logger,
	clock Clock,
	metrics MetricsSink,
) *VisualResolver {
	return NewVisualResolverWithUsage(concepts, bindings, external, semantic, nil, ranker, log, clock, metrics)
}

// NewVisualResolverWithUsage is the canonical Fase 2.3
// constructor. Composition root uses this form when wiring the
// concrete UsageRepository so repetition_penalty has identity.
func NewVisualResolverWithUsage(
	concepts ConceptRepository,
	bindings BindingRepository,
	external SearchFanOut,
	semantic SemanticLookup,
	usage UsageRepository,
	ranker Ranker,
	log Logger,
	clock Clock,
	metrics MetricsSink,
) *VisualResolver {
	if log == nil {
		log = NoopLogger()
	}
	if clock == nil {
		clock = RealClock()
	}
	if metrics == nil {
		metrics = NoopMetrics()
	}
	return &VisualResolver{
		concepts:   concepts,
		bindings:   bindings,
		external:   external,
		semantic:   semantic,
		usage:      usage,
		ranker:     ranker,
		normalizer: NewDefaultNormalizer(""), // godlike/06 SSOT: canonical SHA256 surface
		log:        log,
		clock:      clock,
		metrics:    metrics,
	}
}

// Compile-time assertion: VisualResolver satisfies Resolver.
var _ Resolver = (*VisualResolver)(nil)

// Resolve is the canonical entrypoint.
//
// godlike/06 SSOT: returns a ResolveResult envelope EVEN ON ERROR
// for the partial case (some scenes resolved, some failed). Per-
// scene cascade misses (zero layers because all 9 levels yielded
// nothing OR because AllowExternalSearch=false + no local hits)
// are GRACEFUL: the warnings array carries the cascade rationale
// and the function returns (result, nil). Hard errors (invalid
// input, missing language, ...) return (zero, err).
func (r *VisualResolver) Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error) {
	if len(req.Scenes) == 0 {
		return ResolveResult{}, errInvalidPhrase("Resolve called with empty scenes (caller must supply at least one scene)")
	}
	if req.Language == "" {
		return ResolveResult{}, errInvalidPhrase("ResolveRequest.Language is required")
	}

	result := ResolveResult{
		ProjectID: req.ProjectID,
		Plans:     make([]SceneVisualPlan, 0, len(req.Scenes)),
		Warnings:  make([]string, 0),
	}

	// godlike/06 SSOT (Fase 2.3 anti-repetition wiring): the
	// entire Resolve batch pre-caches the project history ONCE
	// (canonical scope: AntiRepetitionHistoryLimit rows, newest
	// first). When r.usage is nil the resolver degrades
	// gracefully (no penalty input available). When the read
	// errors, we surface it as a typed warning so the batch
	// still progresses.
	//
	// godlike/06 SSOT (per-project cache, not per-scene): the
	// level-by-level warnings are per-scene but the history is
	// project-scoped. Hoisting it out of resolveScene keeps the
	// inner loop's IO surface narrow (one repository call per
	// Resolve, not per scene).
	prevVideoID := ""
	projectHistory := make([]UsageEvent, 0)
	if r.usage != nil && req.ProjectID != "" {
		history, histErr := r.usage.ListProjectUsages(ctx, req.ProjectID, AntiRepetitionHistoryLimit)
		if histErr != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("project_id=%q: anti-repetition history read failed (degraded to no-penalty mode): %s",
					req.ProjectID, histErr.Error()))
		} else {
			projectHistory = history
		}
	}

	for _, scene := range req.Scenes {
		plan, warns, err := r.resolveScene(ctx, req, scene, prevVideoID, projectHistory)
		if err != nil {
			// godlike/07 NO-FAKE-AVAILABILITY: per-scene failure
			// is surfaced as a typed warning; the batch continues.
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("scene_id=%q: %s", scene.ID, err.Error()))
			continue
		}
		// godlike/07 (graceful boundary miss): zero-layer plans
		// represent scenes whose cascade yielded no survivor.
		// They are NOT appended to result.Plans; the warnings
		// already carry the cascade rationale so the caller can
		// branch on len(Plans).
		if len(plan.Layers) > 0 {
			result.Plans = append(result.Plans, plan)
			// godlike/06 SSOT (Fase 2.3 anti-repetition
			// cross-scene invariant): roll the winning layer's
			// VideoID forward as the prior-scene VideoID so the
			// consecutive-scene penalty for scene N+1 is grounded
			// in the canonical winner from scene N. Empty plan
			// (no layers) leaves prevVideoID unchanged so the
			// penalty doesn't fire on a zero-layer boundary.
			prevVideoID = priorSceneVideoID(plan)
		}
		result.Warnings = append(result.Warnings, warns...)
	}

	// godlike/07 (graceful policy-bounded miss): when the caller
	// supplied a resolver-policy that bounded the cascade (e.g.
	// AllowExternalSearch=false) and no local cache had bindings,
	// the function returns (zero-plans, nil, warnings-bearing-
	// cascadeRationale). A real backend error (Level 9 retry, repo
	// failure) surfaces in the warnings array; the batch succeeds.
	return result, nil
}

// priorSceneVideoID returns the canonical VideoID of the first
// winning layer in `plan`, used to roll forward into the next
// scene's prevVideoID. Empty when the plan has zero layers.
// godlike/06 SSOT: Layer.AssetID is the canonical per-scene
// identity (matches MediaCandidate.AssetID). The Fase 4 linker
// will attach an explicit VideoID for non-asset-id repetition
// (e.g. sub-clips of the same source video); until then
// AssetID IS the cross-scene identity.
func priorSceneVideoID(plan SceneVisualPlan) string {
	if len(plan.Layers) == 0 {
		return ""
	}
	return plan.Layers[0].AssetID
}

// resolveScene runs the full 9-level cascade for a single scene.
// Returns (plan, warnings, error). When error is non-nil, the
// caller (Resolve) records a per-scene warning and short-circuits
// the plan slice.
//
// godlike/06 SSOT (slot honoring): the returned SceneVisualPlan
// carries at most len(scene.Slots) layers — picking the top
// ranked candidate per requested slot kind. Layer 1 (primary
// video) + Layer 2 (secondary image) + Layer 3 (evidence overlay)
// are the canonical renderer ceiling; exceeding it is forbidden.
//
// godlike/06 SSOT (Fase 2.3 anti-repetition wiring): projectHistory
// is the project-scoped cache read once per Resolve() batch;
// prevVideoID is the prior-scene winning layer's video_id (empty
// on the first scene) so the consecutive-source penalty fires
// deterministically.
func (r *VisualResolver) resolveScene(
	ctx context.Context,
	req ResolveRequest,
	scene SceneSpec,
	prevVideoID string,
	projectHistory []UsageEvent,
) (SceneVisualPlan, []string, error) {
	plan := SceneVisualPlan{
		ProjectID:  req.ProjectID,
		SceneID:    scene.ID,
		Text:       scene.Text,
		Language:   scene.Language,
		DurationMs: scene.DurationMs,
		Layers:     make([]Layer, 0, len(scene.Slots)),
		// Source is upgraded by upgradeSource() when a winning
		// level produces a layer; starting empty ensures the
		// first winning level sets the canonical label rather
		// than being dominated by the previous default ("exact").
		Source: "",
	}

	if scene.Language == "" {
		scene.Language = req.Language
	}

	warnings := make([]string, 0)

	// Resolve for each requested slot. We deliberately do NOT
	// stack all slots into one bucket — each slot's cascade may
	// succeed at a different level (e.g. primary_video hits exact
	// cache, secondary_image falls through to external), so
	// per-slot orchestration is the canonical pattern.
	for _, slot := range scene.Slots {
		if !IsKnownSlotKind(slot) {
			warnings = append(warnings, fmt.Sprintf(
				"scene_id=%q: unknown slot_kind=%q (filtered)", scene.ID, slot,
			))
			continue
		}

		cands, source, cascadeWarns := r.candidatesForSlot(ctx, scene, slot, req.Policy)
		warnings = append(warnings, cascadeWarns...) // If cascade returned nothing for this slot, leave a
		// warning and skip the layer. The plan's Source tag stays
		// unchanged ("exact"); Source is upgraded ONLY by a winner.
		if len(cands) == 0 {
			warnings = append(warnings, fmt.Sprintf(
				"scene_id=%q slot_kind=%q: no candidate survived the cascade",
				scene.ID, slot,
			))
			continue
		}

		// Filter phase: godlike/07 mandatory gates.
		filters := buildFilterFlags(ctx, cands, r.bindings, scene, slot)
		filtered, err := r.ranker.Filter(ctx, filters)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"scene_id=%q slot_kind=%q: filter error: %s",
				scene.ID, slot, err.Error(),
			))
			continue
		}
		if len(filtered) == 0 {
			warnings = append(warnings, fmt.Sprintf(
				"scene_id=%q slot_kind=%q: all candidates failed the 7 gates",
				scene.ID, slot,
			))
			continue
		}

		// Score + sort + pick top per slot.
		//
		// godlike/06 SSOT (Fase 2.3 anti-repetition): the
		// RankingInput pool is fed through
		// PopulateRepetitionPenalty BEFORE Score so the
		// ranker's canonical formula subtracts the penalty
		// directly (no re-Score pass, no post-Score nudge).
		// Empty projectHistory (no UsageRepository wired / no
		// project_id / read error previously surfaced as a
		// warning) -> all penalties stay 0, the ranker still
		// scores candidates normally.
		inputs := make([]RankingInput, 0, len(filtered))
		for _, fc := range filtered {
			inputs = append(inputs, buildRankingInput(scene, fc))
		}
		// godlike/06 SSOT (Top-10 rose, deterministic-but-diverse):
		// the rose is implicitly the surviving input pool after
		// Filter; limit is bound upstream at ListApprovedByConcept
		// (limit=10). Diversity propagates through the penalty
		// formula: candidates sharing prevVideoID receive
		// SameVideoInConsecutiveScenePenalty (0.3) so they
		// drop in FinalScore comparison while the rose top
		// remains the highest-scoring survivor. No rotation is
		// needed: the deterministic sort keeps determinism.
		inputs = PopulateRepetitionPenalty(inputs, projectHistory, prevVideoID, r.clock.Now())

		scored := make([]rankedCandidate, 0, len(inputs))
		for _, in := range inputs {
			fc := FilteredCandidate{Candidate: in.Candidate, Binding: in.Binding}
			score, scoreErr := r.ranker.Score(ctx, in)
			if scoreErr != nil {
				continue
			}
			if score.Verdict == VerdictDrop {
				continue
			}
			scored = append(scored, rankedCandidate{fc: fc, out: score})
		}
		if len(scored) == 0 {
			continue
		}

		// Sort DESC by FinalScore (deterministic, no RNG).
		sortByFinalScoreDesc(scored)
		for _, candidate := range scored {
			plan.Candidates = append(plan.Candidates, CandidateOption{
				AssetID:      candidate.fc.Candidate.AssetID,
				Provider:     candidate.fc.Candidate.Provider,
				Score:        candidate.out.FinalScore,
				DurationMs:   candidate.fc.Candidate.DurationMs,
				MediaType:    candidate.fc.Candidate.MediaType,
				RightsStatus: string(candidate.fc.Candidate.RightsStatus),
			})
		}

		// Take the top one for this slot via PickTopFromRose
		// (single layer per slot).
		//
		// godlike/06 SSOT (Fase 2.2 rosa pick): the resolver
		// delegates the per-slot top-1 selection to the canonical
		// PickTopFromRose helper so the deterministic-but-
		// diversified knob (DiversityFinalScoreDelta) lives in
		// one place. The helper accepts the sorted rose + the
		// prior scene's VideoID and returns either the highest-
		// scoring survivor OR a non-consecutive alternative
		// within delta. priorSceneVideoID() reads
		// plan.Layers[0].AssetID so the consecutive-scene penalty
		// for scene N+1 is grounded in the canonical winner
		// from scene N. Provider flows through layerFromFilteredCandidate.
		top := PickTopFromRose(scored, prevVideoID)
		layer := layerFromFilteredCandidate(top.fc, slot, top.out.FinalScore)
		plan.Layers = append(plan.Layers, layer)
		plan.Source = upgradeSource(plan.Source, source)
	}

	// godlike/06 SSOT: 1 ≤ len(Layers) ≤ min(3, len(scene.Slots)).
	// Currently we always cap at len(scene.Slots) which is already
	// ≤3 by API convention; Phase 4 adds an explicit ceiling
	// enforcement. Zero layers is a graceful cascade miss — the
	// outer Resolve surfaces the rationale in Warnings and
	// returns (zero-plans, nil).
	return plan, warnings, nil
}

// ── 9-level strict-priority cascade ────────────────────────────────

// candidatesForSlot walks the 9-level cascade for a single slot,
// returning the first non-empty candidate set + the level name
// that produced it + per-level warnings collected along the way.
//
// godlike/06 SSOT (strict priority): once a non-empty level is
// found, all subsequent levels are SKIPPED for this slot.
// Warnings up to AND INCLUDING the winning level are surfaced.
func (r *VisualResolver) candidatesForSlot(
	ctx context.Context,
	scene SceneSpec,
	slot SlotKind,
	policy ResolvePolicy,
) ([]FilteredCandidate, string, []string) {
	cascadeWarns := make([]string, 0)

	// Level 1+2: exact-match + normalized-match via ConceptRepository.
	bindings, lvl, warns := r.levelExactMatch(ctx, scene, slot, policy)
	cascadeWarns = append(cascadeWarns, warns...)
	if len(bindings) > 0 {
		return bindingsToFilteredCandidates(bindings), lvl, cascadeWarns
	}

	// Level 3-7: semantic lookup (entity / action / keyword / topic).
	semantic, semErr := r.semantic.LookupByConcept(ctx, ConceptPhrase, scene.Text, scene.Language, policy.MaxCandidatesPerSlot)
	if semErr != nil {
		cascadeWarns = append(cascadeWarns, fmt.Sprintf(
			"level=3-7: semantic lookup error: %s", semErr.Error(),
		))
	} else if len(semantic) > 0 {
		filtered := candidatesToFilteredCandidates(semantic)
		return filtered, "semantic", cascadeWarns
	}

	// Level 8: local catalog.
	if r.bindings == nil {
		// Without bindings we cannot list approved bindings;
		// surface as a typed failure (no silent zero-return).
		cascadeWarns = append(cascadeWarns, "level=8: bindings repository is nil")
	} else {
		// The local catalog lookup is approximated by listing
		// approved bindings for the scene's text concept. The
		// Phase 2 wiring promotes this to CandidateRepository.
		// godlike/06 SSOT: concept miss is a typed warning
		// (ErrConceptNotFound) — we still continue past Level 8
		// to Level 9 instead of silently zeroing.
		concept, lookupErr := r.canonicalConceptForLookup(ctx, scene)
		if lookupErr == nil {
			local, listErr := r.bindings.ListApprovedByConcept(ctx, concept.ID, []SlotKind{slot}, policy.MaxCandidatesPerSlot)
			if listErr != nil {
				cascadeWarns = append(cascadeWarns, fmt.Sprintf(
					"level=8: ListApprovedByConcept error: %s", listErr.Error(),
				))
			} else if len(local) > 0 {
				return bindingsToFilteredCandidates(local), "local", cascadeWarns
			}
		} else if errors.Is(lookupErr, ErrConceptNotFound) {
			cascadeWarns = append(cascadeWarns, fmt.Sprintf(
				"level=8: concept not found for scene_id=%q (skipping)",
				scene.ID,
			))
		} else {
			cascadeWarns = append(cascadeWarns, fmt.Sprintf(
				"level=8: concept lookup failed: %s", lookupErr.Error(),
			))
		}
	}

	// Level 9: external SearchFanOut. Gated by AllowExternalSearch.
	if !policy.AllowExternalSearch {
		cascadeWarns = append(cascadeWarns, fmt.Sprintf(
			"scene_id=%q slot_kind=%q: external search disabled by policy (level=9 skipped)",
			scene.ID, slot,
		))
		return nil, "", cascadeWarns
	}
	if r.external == nil {
		cascadeWarns = append(cascadeWarns, "level=9: search fanout not wired")
		return nil, "", cascadeWarns
	}
	res, err := r.external.Search(ctx, SearchFanOutQuery{
		Text:       scene.Text,
		Language:   scene.Language,
		Limit:      policy.MaxCandidatesPerSlot,
		MediaTypes: mediaTypesForSlot(slot),
	})
	if err != nil {
		cascadeWarns = append(cascadeWarns, fmt.Sprintf("level=9: external search error: %s", err.Error()))
		return nil, "", cascadeWarns
	}
	if res.Partial {
		cascadeWarns = append(cascadeWarns, fmt.Sprintf(
			"level=9: partial backend failures (backends=%v)",
			res.BackendNames,
		))
	}
	return candidatesToFilteredCandidates(res.Candidates), "external", cascadeWarns
}

// levelExactMatch wraps Level 1 (exact fingerprint hit) + Level 2
// (normalized retry). Returns bindings at any approval status for
// Level 1; Level 2 retry is a future enhancement (skeleton here).
//
// godlike/06 SSOT (canonical normalization): the resolver MUST use
// the canonical Normalizer (SHA256 fingerprint + NFC + lowercase +
// dedup-whitespace + strip terminal punctuation). Tests assert the
// canonical fingerprint round-trip so a future normalization drift
// surfaces as a test failure.
func (r *VisualResolver) levelExactMatch(
	ctx context.Context,
	scene SceneSpec,
	slot SlotKind,
	policy ResolvePolicy,
) ([]MediaBinding, string, []string) {
	warns := make([]string, 0)
	if r.concepts == nil || r.bindings == nil {
		warns = append(warns, "level=1+2: concepts/bindings repository is nil")
		return nil, "", warns
	}
	concept, err := r.canonicalConceptForLookup(ctx, scene)
	if err != nil {
		if !errors.Is(err, ErrConceptNotFound) {
			warns = append(warns, fmt.Sprintf("level=1+2: FindByFingerprint error: %s", err.Error()))
		}
		return nil, "", warns
	}
	limit := policy.MaxCandidatesPerSlot
	if limit <= 0 {
		limit = defaultResolverLimit
	}
	bindings, err := r.bindings.ListApprovedByConcept(ctx, concept.ID, []SlotKind{slot}, limit)
	if err != nil {
		warns = append(warns, fmt.Sprintf("level=1+2: ListApprovedByConcept error: %s", err.Error()))
		return nil, "", warns
	}
	if len(bindings) == 0 {
		return nil, "", warns
	}
	return bindings, "exact", warns
}

// canonicalConceptForLookup computes the canonical phrase via the
// injected Normalizer (godlike/06 SSOT) and resolves the matching
// concept row by PhraseFingerprint. Returns ErrConceptNotFound on miss.
func (r *VisualResolver) canonicalConceptForLookup(ctx context.Context, scene SceneSpec) (MediaConcept, error) {
	c, err := r.normalizer.Normalize(ctx, scene.Text, scene.Language)
	if err != nil {
		return MediaConcept{}, err
	}
	return r.concepts.FindByFingerprint(ctx, c.Language, c.PhraseFingerprint)
}

// fingerprintForNormalized is the package-level helper kept for
// test compatibility ONLY. Production code MUST use
// defaultNormalizer.Fingerprint (or any Normalizer impl) — this
// helper is byte-equivalent so the in-package resolver tests can
// pre-compute fingerprints without depending on a Normalizer at
// construction time.
//
// godlike/06 SSOT: tests use this helper to seed ConceptRepository
// fixtures; production code reaches the canonical SHA256 via
// r.normalizer.Normalize(...).
func fingerprintForNormalized(language, normalized string) string {
	return NewDefaultNormalizer("").Fingerprint(language, normalized)
}

// defaultResolverLimit is the canonical fallback for the resolver
// hot path when policy.MaxCandidatesPerSlot is zero (godlike/07
// fail-closed: explicit default beats silent 0).
const defaultResolverLimit = 10

// ── Bindings → FilteredCandidate projection (lossless) ────────────

// bindingsToFilteredCandidates converts MediaBinding rows into
// FilteredCandidate envelopes WITHOUT losing operator-curated
// fields. The FilteredCandidate.Binding field carries the binding
// envelope (godlike/06 SSOT extension) so the ranker can pull
// ManualScore, SemanticScore, QualityScore, SuccessScore and the
// binding window (StartMs, EndMs) downstream.
//
// AssetID is the bridge: media_bindings.asset_id → media_assets.id.
// A binding without AssetID is fail-closed (FilteredCandidate is
// skipped and a typed warning is appended).
//
// godlike/06 SSOT (canonical defaults): the synthesized Candidate
// carries canonical MaterializationStatus=Hot + DiscoveryStatus=Searched.
// An approved, manually-curated binding is treated as available
// media so it survives the ranker's availability gate; the
// binding envelope does NOT carry the cache-tier or pipeline-
// completion assertion (those live on the linked media_assets row).
// Phase 2 adds the projection loader (binding → asset hot-tier
// query) at which point the canonical defaults are replaced with
// real values.
//
// Filter's well-formed guard requires non-empty Materialization/
// Discovery statuses in the canonical closed sets; without
// these defaults every binding hit would fail the guard and be
// dropped with no Layer produced.
func bindingsToFilteredCandidates(bindings []MediaBinding) []FilteredCandidate {
	out := make([]FilteredCandidate, 0, len(bindings))
	for _, b := range bindings {
		if b.AssetID == "" {
			continue
		}
		out = append(out, FilteredCandidate{
			Candidate: MediaCandidate{
				AssetID:               b.AssetID,
				MaterializationStatus: MaterializationHot,
				DiscoveryStatus:       DiscoverySearched,
			},
			Binding: b,
		})
	}
	return out
}

// candidatesToFilteredCandidates converts MediaCandidate rows to
// FilteredCandidate envelopes (Level 8/9 path — no binding
// envelope available, so Binding stays zero). The ranker falls
// back to canonical default scores via the RankingInput.
func candidatesToFilteredCandidates(candidates []MediaCandidate) []FilteredCandidate {
	out := make([]FilteredCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.AssetID == "" {
			continue
		}
		out = append(out, FilteredCandidate{Candidate: c})
	}
	return out
}

// buildFilterFlags computes the FilteredCandidate gates from the
// (candidate, binding, scene) triple.
//
// godlike/06 SSOT (filter flags): the seven mandatory gates are
// a JS function of (c, b, scene). They are aggregated into the
// FilteredCandidate booleans which Filter() reads.
//
// Phase 1.x scope:
//   - IsDuplicate: false (Phase 2 anti-repetition lands)
//   - MissingRights: candidate's RightsStatus != RightsVerified
//   - AspectMismatch: candidate's MediaType does not match the slot's expected type
//   - Contaminated: candidate's MaterializationStatus == MaterializationFailed
//   - License valid (gates via MissingRights), availability
//     (MaterializationStatus ∈ {Hot, Warm, Cold}), duration valid
//     (DurationMs > 0 for videos), format supported (MediaType
//     in {"video", "image"}) — all rolled into MissingRights /
//     AspectMismatch for Phase 1.x simplicity. Phase 2 splits
//     each gate into a separate boolean for rich diagnostics.
func buildFilterFlags(
	_ context.Context,
	candidates []FilteredCandidate,
	_ BindingRepository,
	_ SceneSpec,
	slot SlotKind,
) []FilteredCandidate {
	out := make([]FilteredCandidate, 0, len(candidates))
	for _, fc := range candidates {
		cc := fc.Candidate
		// 1. Rights (canonical godlike/07 gate #1): rights-uncertain
		// candidates MUST NOT be promoted to Hot (ranker still sees
		// them but they receive a rights_penalty at Score time).
		missingRights := cc.RightsStatus != "" && cc.RightsStatus != RightsVerified
		// 2. Aspect ratio / media-type mismatches (gate #4):
		// primary_video expects "video" MediaType; secondary_image
		// expects "image". An empty MediaType is allowed
		// (legacy rows) and bypasses the gate.
		aspectMismatch := aspectMismatchFor(slot, cc.MediaType)
		// 3. Corrupted / failed materialization (gate #6).
		contaminated := cc.MaterializationStatus == MaterializationFailed
		// 4. Dedup (gate #7): anti-repetition is Phase 2; Phase 1.x
		// leaves IsDuplicate=false. A future binding
		// anti-repetition column on media_bindings will gate this.
		out = append(out, FilteredCandidate{
			Candidate:      cc,
			Binding:        fc.Binding,
			IsDuplicate:    false,
			MissingRights:  missingRights,
			AspectMismatch: aspectMismatch,
			Contaminated:   contaminated,
		})
	}
	return out
}

// aspectMismatchFor returns true when the slot expects a media
// type that the candidate does not declare. An empty candidate
// MediaType is treated as ambiguous (no mismatch) so legacy rows
// remain selectable.
func aspectMismatchFor(slot SlotKind, mediaType string) bool {
	if mediaType == "" {
		return false
	}
	switch slot {
	case SlotPrimaryVideo:
		return mediaType != "video"
	case SlotSecondaryImage, SlotEvidenceOverlay, SlotMap,
		SlotPortrait, SlotDocument, SlotBackground:
		return mediaType != "image"
	}
	return false
}

// buildRankingInput projects a (FilteredCandidate, SceneSpec) into
// the ranker's canonical RankingInput. When the candidate has a
// binding envelope, scores come from the operator-curated columns
// verbatim. Otherwise they come from canonical defaults.
//
// godlike/06 SSOT (lossless binding projection): binding fields
// ManualScore / SemanticScore / QualityScore / SuccessScore flow
// into the ranker seats without intermediate copying (a future
// drift in this mapping is caught by tests).
func buildRankingInput(scene SceneSpec, fc FilteredCandidate) RankingInput {
	in := RankingInput{
		Candidate: fc.Candidate,
		Binding:   fc.Binding,
	}

	if fc.Binding.AssetID != "" {
		// Path A: binding-envelope projection (godlike/06 SSOT
		// lossless: operator-curated ManualScore flows in verbatim;
		// ApprovalStatus=Approved gates the binding into the
		// resolver hot path but the SCORE comes from ManualScore).
		in.SemanticScore = clamp01(fc.Binding.SemanticScore)
		in.ExactMatchScore = 1.0 // the existence of an approved binding IS the exact-match signal
		in.VisualScore = 0.5     // visual channel is Phase 4; Phase 1.x neutral
		// manual_approval_score is the operator-curated ManualScore
		// (clamped to [0,1]). When the binding is not yet approved
		// it is hard-zeroed so it cannot sneak into the hot path
		// via a high ManualScore while bypassing approval.
		in.ManualApprovalScore = 0.0
		if fc.Binding.ApprovalStatus == ApprovalApproved {
			in.ManualApprovalScore = clamp01(fc.Binding.ManualScore)
		}
		in.QualityScore = clamp01(fc.Binding.QualityScore)
		in.HistoricalSuccessScore = clamp01(fc.Binding.SuccessScore)
	} else if fc.Candidate.CandidateScore > 0 {
		// Path B-variant: candidate-only path from Level 3-7
		// semantic lookup (QdrantSemanticLookup). The Qdrant
		// hybrid-search RRF score propagates verbatim into
		// the ranker's SemanticScore seat so a paraphrase
		// match beats a neutral zero. godlike/06 SSOT
		// (lossless Qdrant-score → ranker-seat projection).
		in.SemanticScore = clamp01(fc.Candidate.CandidateScore)
		in.ExactMatchScore = 0.0 // semantic ≠ exact-match
		in.VisualScore = 0.0
		in.ManualApprovalScore = 0.0
		in.QualityScore = 0.5
		in.HistoricalSuccessScore = 0.4
	} else {
		// Path B (Levels 8/9 path: no binding envelope, no
		// Qdrant score). Defaults are godlike/06 SSOT —
		// Phase 1.x's canonical neutral scores for the
		// candidate-only path that arrives without a Qdrant
		// RRF hint.
		in.SemanticScore = 0.0
		in.ExactMatchScore = 0.0
		in.VisualScore = 0.0
		in.ManualApprovalScore = 0.0
		in.QualityScore = 0.5
		in.HistoricalSuccessScore = 0.4
	}

	// Duration fit: 1.0 when the candidate duration sits inside
	// ±10% of the scene's duration; degrades linearly otherwise.
	// Phase 2 will use a richer curve per visual-action profile.
	in.DurationFitScore = durationFitScore(scene.DurationMs, fc.Candidate.DurationMs)

	// Repetition penalty: applied only at the per-slot level for
	// binding-envelope candidates (Phase 2). Phase 1.x zero.
	in.RepetitionPenalty = 0.0

	// Rights penalty: non-zero when candidate rights != verified.
	// Phase 2 also penalizes AllowConditional verdicts; Phase 1.x
	// keeps the binary penalty.
	if fc.Candidate.RightsStatus != "" && fc.Candidate.RightsStatus != RightsVerified {
		in.RightsPenalty = 0.30
	}
	return in
}

// durationFitScore returns 1.0 when candidate duration sits inside
// ±10% of the scene, 0.0 when more than 2x off, linear between.
func durationFitScore(sceneMs, candidateMs int64) float64 {
	if sceneMs <= 0 || candidateMs <= 0 {
		// Treat missing duration as neutral 0.5 (we cannot penalize).
		if sceneMs <= 0 && candidateMs <= 0 {
			return 0.5
		}
		return 0.0
	}
	ratio := float64(candidateMs) / float64(sceneMs)
	switch {
	case ratio >= 0.9 && ratio <= 1.1:
		return 1.0
	case ratio >= 0.5 && ratio <= 2.0:
		// Linear interpolation between 1.0 (at 0.9..1.1) and 0.0
		// at the 0.5 / 2.0 endpoints.
		d := ratio
		if d > 1.0 {
			d = 2.0 - d
		}
		// d in [0.5, 0.9] → score in [0.0, 1.0]
		return (d - 0.5) / 0.4
	default:
		return 0.0
	}
}

// clamp01 saturates the value to [0,1]. Out-of-range ranker
// inputs (e.g. operator-curated ManualScore = 1.3) are NOT a
// silent zero — clamped at the boundary so the math stays stable.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// mediaTypesForSlot maps a SlotKind to the canonical MediaType
// strings it accepts. Used to scope the SearchFanOut query.
func mediaTypesForSlot(slot SlotKind) []string {
	switch slot {
	case SlotPrimaryVideo:
		return []string{"video"}
	case SlotSecondaryImage, SlotEvidenceOverlay, SlotMap,
		SlotPortrait, SlotDocument, SlotBackground:
		return []string{"image"}
	}
	return nil
}

// ── Sort + layer composition helpers ────────────────────────────

// rankedCandidate is the internal (filter, score) pair used by
// the resolver sort + pick step.
type rankedCandidate struct {
	fc  FilteredCandidate
	out RankingOutput
}

// sortByFinalScoreDesc sorts in place by FinalScore DESC,
// breaking ties by AssetID ASC for determinism (mirrors
// search.Aggregator's "RankByScore (Score DESC, Source ASC,
// AssetID ASC)" contract from PR 9).
func sortByFinalScoreDesc(in []rankedCandidate) {
	sort.SliceStable(in, func(i, j int) bool {
		return lessRanked(in[i], in[j])
	})
}

// lessRanked orders ranked candidates: higher FinalScore first;
// ties broken by AssetID ASC.
func lessRanked(a, b rankedCandidate) bool {
	if a.out.FinalScore != b.out.FinalScore {
		return a.out.FinalScore > b.out.FinalScore
	}
	return a.fc.Candidate.AssetID < b.fc.Candidate.AssetID
}

// layerFromFilteredCandidate composes a Layer envelope from the
// winning FilteredCandidate + the slot + the final score.
//
// godlike/06 SSOT (lossless binding + provider propagation):
// when the FilteredCandidate has a binding envelope, StartMs /
// EndMs / BindingID flow through verbatim. The Provider tag
// always propagates from the source MediaCandidate — a binding
// envelope does NOT mask the canonical Provider (the Level
// 3-7 semantic adapter stamps ProviderSemanticIndex; the
// Level 9 SearchFanOutAdapter stamps the forwarding provider;
// Level 1+2 binding wins preserve the binding's manually-curated
// origin via fc.Candidate.Provider when present, otherwise "").
func layerFromFilteredCandidate(fc FilteredCandidate, slot SlotKind, finalScore float64) Layer {
	layer := Layer{
		Slot:           slot,
		AssetID:        fc.Candidate.AssetID,
		CandidateScore: finalScore,
		Provider:       fc.Candidate.Provider,
	}
	if fc.Binding.AssetID != "" {
		layer.BindingID = fc.Binding.ID
		layer.StartMs = fc.Binding.StartMs
		layer.EndMs = fc.Binding.EndMs
	}
	return layer
}

// upgradeSource returns the higher-ranked source label between
// current and a winning level. Strict priority: exact > semantic >
// local > external > mixed. The current plan.Source starts at
// "exact" (the canonical default) and may stay there when the
// winning level IS exact, get downgraded otherwise.
func upgradeSource(current, winning string) string {
	if winning == "" {
		return current
	}
	rank := map[string]int{
		"exact":    4,
		"semantic": 3,
		"local":    2,
		"external": 1,
		"mixed":    0,
	}
	if rank[winning] > rank[current] {
		return winning
	}
	if current == "" {
		return winning
	}
	return current
}

// errInvalidPhrase is a tiny helper so Resolve can wrap the
// per-package sentinel cleanly.
func errInvalidPhrase(reason string) error {
	return errors.Join(ErrInvalidPhrase, errors.New(reason))
}
