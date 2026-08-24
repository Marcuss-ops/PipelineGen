// Package mediamemory — linker_link.go owns the canonical "link"
// phase of the linker pipeline (architecture doc section 7 + Fase 3.2).
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// (phrase → MediaConcept) and (MediaConcept × media.SlotPrimaryVideo →
// MediaBinding) persistence seam. The two helpers here
// (normalizeAndUpsertConcepts, persistBindings) together form the
// "link" phase of the linker pipeline that EnrichCandidate
// (linker_worker.go) composes:
//
//  1. normalizeAndUpsertConcepts — canonical phrase→concept
//     pipeline (Normalize → ConceptRepository.Upsert, idempotent
//     via ON CONFLICT DO UPDATE).
//  2. persistBindings — canonical binding-write pipeline (one
//     MediaBinding per concept × media.SlotPrimaryVideo; default
//     ApprovalPending for Fase 3.2 — the dashboard's "Visual
//     Memory" page is the canonical approval surface for
//     promotion to ApprovalApproved).
//
// godlike/07 NO-FAKE-AVAILABILITY: the helpers here NEVER swallow
// failures silently. Each helper returns a (success, failures)
// tuple where per-step errors accumulate in the []string slice
// without short-circuiting the entire loop — partial success is
// the canonical linker behavior (at least one surviving concept
// + binding is enough to proceed).
package mediamemory

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/google/uuid"
)

// normalizeAndUpsertConcepts loops over phrases, runs the
// canonical Normalizer, upserts the resulting MediaConcept.
// Tracks but does NOT short-circuit on per-phrase normalization
// failure (godlike/06 SSOT partial-success: at least one
// surviving concept is enough to proceed). Returns
// (concepts, failures).
func (w *defaultLinker) normalizeAndUpsertConcepts(ctx context.Context, req LinkerRequest, phrases []string) ([]MediaConcept, []string) {
	concepts := make([]MediaConcept, 0, len(phrases))
	failures := make([]string, 0)
	for _, phrase := range phrases {
		if phrase == "" {
			continue
		}
		concept, err := w.deps.Normalizer.Normalize(ctx, phrase, req.Language)
		if err != nil {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q phrase=%q normalize failed: %s",
				req.Candidate.ID, phrase, err.Error(),
			))
			continue
		}
		// godlike/06 SSOT (default ConceptType): catalog_only
		// enrichment without an explicit entity classifier
		// assigns ConceptPhrase. Future Fase 4.1 visual channel
		// wires EntityDetector to override to ConceptEntity / etc.
		if concept.ConceptType == "" {
			concept.ConceptType = ConceptPhrase
		}
		if concept.ID == "" {
			concept.ID = uuid.NewString()
		}
		persisted, perr := w.deps.Concepts.Upsert(ctx, concept)
		if perr != nil {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q concept (phrase=%q lang=%q) upsert failed: %s",
				req.Candidate.ID, phrase, req.Language, perr.Error(),
			))
			continue
		}
		concepts = append(concepts, persisted)
	}
	return concepts, failures
}

// persistBindings writes one MediaBinding per (concept ×
// media.SlotPrimaryVideo). godlike/06 SSOT (canonical slot seed):
// for Fase 3.2 the linker produces a primary_video slot per
// concept. Future Fase 4.4 will lift the slot set to the
// SceneVisualPlan's preferred_slots (the linker remains the
// slot-agnostic writer; the ranker / resolver select the
// canonical slot at render time).
func (w *defaultLinker) persistBindings(ctx context.Context, req LinkerRequest, concepts []MediaConcept) ([]string, []string) {
	ids := make([]string, 0, len(concepts))
	failures := make([]string, 0)
	if w.deps.Bindings == nil {
		return ids, []string{"mediamemory: linker BindingRepository is nil (composition root must wire sqlite repo)"}
	}
	for _, c := range concepts {
		binding := MediaBinding{
			ConceptID: c.ID,
			AssetID:   req.Candidate.AssetID,
			SlotKind:  media.SlotPrimaryVideo,
			Origin:    OriginAutoLink,
			// godlike/06 SSOT (auto-link approval semantics): the
			// linker writes ApprovalPending by default; the
			// dashboard's "Visual Memory" page is the canonical
			// approval surface for promotion to ApprovalApproved.
			ApprovalStatus: ApprovalPending,
			ManualScore:    0,
			SemanticScore:  0,
			QualityScore:   1, // linker-pass n/a; baseline 1 keeps dashboards sensible
		}
		persisted, perr := w.deps.Bindings.Upsert(ctx, binding)
		if perr != nil {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q concept=%q persist binding failed: %s",
				req.Candidate.ID, c.ID, perr.Error(),
			))
			continue
		}
		ids = append(ids, persisted.ID)
	}
	return ids, failures
}
