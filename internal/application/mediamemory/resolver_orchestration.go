// Package mediamemory — resolver_orchestration.go is the canonical
// home for the orchestrator entrypoints (Resolve, resolveScene)
// and the 9-level strict-priority cascade driver
// (candidatesForSlot + levelExactMatch + mediaTypesForSlot).
//
// godlike/06 SSOT (single canonical home per layer): every per-
// scene orchestrator entry that hits the canonical cascade MUST
// live in this file so the orchestrator's lock-step with the
// scoring/projection/lookup layers stays grep-able.
//
// godlike/06 SSOT (strict priority cascade, owner: this file):
// levels 1..9 are inspected IN ORDER; the FIRST level returning a
// non-empty candidate set wins, and the remaining levels are NOT
// consulted for that slot. Only when the winning level returns
// an empty set is the next level tried. This is canonical to the
// architecture doc's "ordine di priorità": a manual-approved
// phrase hit MUST short-circuit the cascade (so a human
// association never gets overwritten by an external search).
//
// File split ownership (godlike/06 SSOT):
//   - resolver.go                 : Resolver port + VisualResolver struct + ResolverDeps + ctors + pins + EmbeddingVersion
//   - resolver_lookup.go          : canonicalConceptForLookup + fingerprintForNormalized
//   - resolver_orchestration.go   : Resolve + resolveScene + candidatesForSlot + levelExactMatch + mediaTypesForSlot + priorSceneVideoID + defaultResolverLimit  ← this file
//   - resolver_scoring.go         : rankedCandidate + buildFilterFlags + aspectMismatchFor + buildRankingInput + durationFitScore + clamp01 + sort + layerFromFilteredCandidate + upgradeSource
//   - resolver_projection.go      : bindingsToFilteredCandidates + candidatesToFilteredCandidates
//   - resolver_brain.go           : errInvalidPhrase + Search method (brain.MediaMemoryResolutionPort impl)
package mediamemory

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

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
		if !media.IsKnownSlotKind(slot) {
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
				CandidateID:  candidate.fc.Candidate.ID,
				SourceURL:    candidate.fc.Candidate.SourceURL,
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
		Text:         scene.Text,
		Language:     scene.Language,
		Limit:        policy.MaxCandidatesPerSlot,
		MediaTypes:   mediaTypesForSlot(slot),
		SearchPolicy: policy.SearchPolicy,
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

// defaultResolverLimit is the canonical fallback for the resolver
// hot path when policy.MaxCandidatesPerSlot is zero (godlike/07
// fail-closed: explicit default beats silent 0).
const defaultResolverLimit = 10

// mediaTypesForSlot maps a SlotKind to the canonical MediaType
// strings it accepts. Used to scope the SearchFanOut query.
func mediaTypesForSlot(slot SlotKind) []string {
	switch slot {
	case media.SlotPrimaryVideo:
		return []string{"video"}
	case media.SlotSecondaryImage, media.SlotEvidenceOverlay, media.SlotMap,
		media.SlotPortrait, media.SlotDocument, media.SlotBackground:
		return []string{"image"}
	}
	return nil
}
