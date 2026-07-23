// Package mediamemory — binding_service.go is the canonical SSOT for
// the media_bindings write surface.
//
// godlike/06 SSOT: BindingService is the SINGLE owner of mutation
// semantics on media_bindings. Every dashboard action, every auto-
// linker write, every reindex reconcile passes through this service.
// Direct repository.Upsert calls outside BindingsService are
// forward-prevention forbidden (forward-pointer to archcheck).
//
// godlike/07 NO-FAKE-AVAILABILITY: invalid bindings (unknown
// SlotKind, missing concept_id, missing asset_id) are surfaced via
// typed sentinels (ErrInvalidSlotKind / ErrDuplicateBinding /
// ErrBindingNotFound / ErrConceptNotFound) via %w. Origin=manual
// with ApprovalApproved is the canonical "creator approved"
// balance: humans MUST approve explicitly (no implicit promotion).
//
// Anti-anti-pattern (maya_binding.go): the user's spec explicitly
// REJECTS per-topic binding files. Every domain topic (Maya, boxing,
// history, ...) routes through the SAME BindingService.
package mediamemory

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// BindingService is the canonical port for media_bindings
// mutation. Concrete impl is defaultBindingService below.
type BindingService interface {
	// Create persists a binding. UNIQUE collisions surface as
	// wrapped ErrDuplicateBinding. Origin="manual" sets
	// ApprovalStatus=ApprovalApproved (caller-controlled).
	Create(ctx context.Context, b MediaBinding) (MediaBinding, error)

	// Update mutates an existing binding. Approval-only updates
	// route through Approve.
	Update(ctx context.Context, b MediaBinding) (MediaBinding, error)

	// Delete removes a binding by ID. Phase 1.x: only the dashboard
	// admin paths call this; auto linker never deletes.
	Delete(ctx context.Context, id string) error

	// Approve sets ApprovalStatus=ApprovalApproved. Origin "manual"
	// is the canonical path; auto paths use SetOrigin.
	Approve(ctx context.Context, id string) error

	// Reject sets ApprovalStatus=ApprovalRejected. The ranker treats
	// rejected bindings as if they did not exist.
	Reject(ctx context.Context, id string) error

	// ListByConcept returns every binding (any status) for the
	// concept. Used by the dashboard's "Visual Memory" page.
	ListByConcept(ctx context.Context, conceptID string) ([]MediaBinding, error)

	// ListBySlot returns all approved bindings for a concept filtered
	// by SlotKind. Used by the ranker's hot path pre-load.
	ListBySlot(ctx context.Context, conceptID string, slot SlotKind, limit int) ([]MediaBinding, error)
}

// ── Default implementation (canonical) ──────────────────────────

// defaultBindingService is the canonical implementation of
// BindingService.
type defaultBindingService struct {
	concepts   ConceptRepository
	bindings   BindingRepository
	dispatcher BindingMutationDispatcher
	log        Logger
	clock      Clock
}

// NewDefaultBindingService constructs the service with the canonical
// (concepts, bindings, dispatcher) dependency set. Composition root
// wires the SQLite repos and the canonical BindingMutationDispatcher in
// Phase 1.2.
//
// godlike/07 NO-FAKE-AVAILABILITY: dispatcher is MANDATORY. Passing nil
// makes every write method fail with
// ErrBindingMutationDispatcherUnavailable, so a mis-wired composition
// root is caught immediately instead of silently writing without the
// outbox.
func NewDefaultBindingService(concepts ConceptRepository, bindings BindingRepository, dispatcher BindingMutationDispatcher, log Logger, clock Clock) *defaultBindingService {
	if log == nil {
		log = NoopLogger()
	}
	if clock == nil {
		clock = RealClock()
	}
	return &defaultBindingService{
		concepts:   concepts,
		bindings:   bindings,
		dispatcher: dispatcher,
		log:        log,
		clock:      clock,
	}
}

// Compile-time assertion.
var _ BindingService = (*defaultBindingService)(nil)

// applyDefaults fills in non-zero defaults on a Brand-NEW binding
// (Create path). Update / Approve / Reject paths MUST preserve the
// existing values supplied by the caller.
//
// godlike/06 SSOT (ID mint location): the canonical ID mint is at
// the SQLite concrete repository (uuid.NewString in the Upsert
// INSERT statement, see
// internal/infrastructure/database/sqlite/mediamemory/bindings_repository.go).
// The service deliberately leaves b.ID empty on Create so the repo
// can mint with full context; a pre-mint in the service splits that
// responsibility and risks a drift between service-minted and
// repo-minted IDs.
func applyDefaults(b *MediaBinding, clock Clock) {
	now := clock.Now().UTC()
	if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	if b.Origin == "" {
		// godlike/06 SSOT: dashboard/edit page is the canonical
		// OriginManual path. Auto-linker writes use OriginAutoLink
		// explicitly; auto-paraphrase discoveries use OriginPhraseEq
		// / OriginSemantic.
		b.Origin = OriginManual
	}
	if b.ApprovalStatus == "" {
		b.ApprovalStatus = ApprovalApproved
	}
	if b.Provider == "" {
		// godlike/06 SSOT (Fase 4.3 binding provenance
		// contract): manual dashboard edits land with
		// ProviderLocal (the canonical "human-curated" tag).
		// Auto-linker writes set Provider explicitly to the
		// SearchFanOut-translated value (artlist, youtube,
		// pexels, mediamemory.semantic, ...).
		b.Provider = ProviderLocal
	}
}

// Create is the canonical entrypoint for the dashboard's manual
// binding editor + the Phase-3 linker worker's first-pass writes.
//
// Validation chain (godlike/06 SSOT):
//  1. SlotKind is in the canonical closed set (media.IsKnownSlotKind).
//  2. concept_id points to an existing row (ConceptRepository.FindByID
//     — fail-fast with typed ErrConceptNotFound; never silently
//     accept an orphan binding).
//  3. UNIQUE conflict (concept_id, asset_id, slot_kind) is wrapped
//     as ErrDuplicateBinding.
//
// godlike/07 NO-FAKE-AVAILABILITY: every failed step surfaces a
// typed-sentinel envelope so the API handler can map it to a
// correct HTTP status (400 for SlotKind/Concept, 409 for Duplicate).
func (s *defaultBindingService) Create(ctx context.Context, b MediaBinding) (MediaBinding, error) {
	if s.dispatcher == nil {
		return MediaBinding{}, fmt.Errorf("mediamemory: create binding: %w", ErrBindingMutationDispatcherUnavailable)
	}
	if !media.IsKnownSlotKind(b.SlotKind) {
		return MediaBinding{}, fmt.Errorf(
			"mediamemory: create binding slot_kind=%q: %w",
			string(b.SlotKind), ErrInvalidSlotKind,
		)
	}
	if b.ConceptID == "" {
		return MediaBinding{}, fmt.Errorf(
			"mediamemory: create binding missing concept_id: %w",
			ErrInvalidBindingInput,
		)
	}
	if b.AssetID == "" {
		return MediaBinding{}, fmt.Errorf(
			"mediamemory: create binding missing asset_id: %w",
			ErrInvalidBindingInput,
		)
	}
	if _, err := s.concepts.FindByID(ctx, b.ConceptID); err != nil {
		return MediaBinding{}, fmt.Errorf(
			"mediamemory: create binding concept_id=%q: %w",
			b.ConceptID, err,
		)
	}
	applyDefaults(&b, s.clock)
	return s.dispatcher.UpsertBinding(ctx, b)
}

// Update mutates an existing binding. Approval-only updates MUST
// route through Approve / Reject (godlike/06 SSOT: explicit audit
// trail; the dashboard's "approve this binding" button calls
// Approve, not Update with ApprovalStatus=approved).
func (s *defaultBindingService) Update(ctx context.Context, b MediaBinding) (MediaBinding, error) {
	if s.dispatcher == nil {
		return MediaBinding{}, fmt.Errorf("mediamemory: update binding: %w", ErrBindingMutationDispatcherUnavailable)
	}
	if !media.IsKnownSlotKind(b.SlotKind) {
		return MediaBinding{}, fmt.Errorf(
			"mediamemory: update binding slot_kind=%q: %w",
			string(b.SlotKind), ErrInvalidSlotKind,
		)
	}
	if b.ID == "" {
		return MediaBinding{}, fmt.Errorf(
			"mediamemory: update binding missing id: %w",
			ErrBindingNotFound,
		)
	}
	b.UpdatedAt = s.clock.Now().UTC()
	return s.dispatcher.UpsertBinding(ctx, b)
}

// Delete is provided for admin reindex flows (dashboard "remove
// binding"). The Phase-3 linker NEVER deletes (auto discovery is
// append-only; corrections are a re-Approve or new OriginManual).
func (s *defaultBindingService) Delete(ctx context.Context, id string) error {
	if s.dispatcher == nil {
		return fmt.Errorf("mediamemory: delete binding: %w", ErrBindingMutationDispatcherUnavailable)
	}
	if id == "" {
		return fmt.Errorf("mediamemory: delete binding missing id: %w", ErrBindingNotFound)
	}
	// Read the binding before deletion to propagate concept_id to the
	// async outbox event. The binding row may already be gone in a
	// concurrent delete; that race is surfaced as ErrBindingNotFound
	// and the caller can retry.
	b, err := s.bindings.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("mediamemory: delete binding read %q: %w", id, err)
	}
	return s.dispatcher.DeleteBinding(ctx, id, b.ConceptID)
}

// Approve sets ApprovalStatus=ApprovalApproved. Preserves the
// existing usage_count + last_used_at + origin (godlike/06 SSOT:
// the canonical "approve" flow is a status flip, not a reset).
func (s *defaultBindingService) Approve(ctx context.Context, id string) error {
	if s.dispatcher == nil {
		return fmt.Errorf("mediamemory: approve binding %q: %w", id, ErrBindingMutationDispatcherUnavailable)
	}
	b, err := s.bindings.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("mediamemory: approve binding %q: %w", id, err)
	}
	b.ApprovalStatus = ApprovalApproved
	if b.Origin == "" {
		b.Origin = OriginManual
	}
	b.UpdatedAt = s.clock.Now().UTC()
	if _, err := s.dispatcher.UpsertBinding(ctx, b); err != nil {
		return fmt.Errorf("mediamemory: approve binding %q: %w", id, err)
	}
	return nil
}

// Reject sets ApprovalStatus=ApprovalRejected. The ranker treats
// rejected bindings as if they did not exist (filtered at the
// ListApprovedByConcept hot path).
//
// godlike/06 SSOT (status-flip symmetry with Approve): both
// Approve and Reject backfill Origin if empty so the ranker never
// sees a binding-with-status-flip but missing origin. The
// status flip is NOT a reset — usage_count / last_used_at /
// success_score / manual scores are preserved.
func (s *defaultBindingService) Reject(ctx context.Context, id string) error {
	if s.dispatcher == nil {
		return fmt.Errorf("mediamemory: reject binding %q: %w", id, ErrBindingMutationDispatcherUnavailable)
	}
	b, err := s.bindings.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("mediamemory: reject binding %q: %w", id, err)
	}
	b.ApprovalStatus = ApprovalRejected
	if b.Origin == "" {
		b.Origin = OriginManual
	}
	b.UpdatedAt = s.clock.Now().UTC()
	if _, err := s.dispatcher.UpsertBinding(ctx, b); err != nil {
		return fmt.Errorf("mediamemory: reject binding %q: %w", id, err)
	}
	return nil
}

// ListByConcept routes to the repository's same-named read; the
// repository's ListByConcept returns ALL statuses (the dashboard
// wants pending + approved + rejected rows for its diff view).
func (s *defaultBindingService) ListByConcept(ctx context.Context, conceptID string) ([]MediaBinding, error) {
	if conceptID == "" {
		return nil, fmt.Errorf("mediamemory: list by concept missing concept_id: %w", ErrConceptNotFound)
	}
	return s.bindings.ListByConcept(ctx, conceptID)
}

// ListBySlot is the ranker's resolver-hot-path pre-load.
// ListApprovedByConcept is reused (single-slot filter via []SlotKind).
func (s *defaultBindingService) ListBySlot(ctx context.Context, conceptID string, slot SlotKind, limit int) ([]MediaBinding, error) {
	if !media.IsKnownSlotKind(slot) {
		return nil, fmt.Errorf(
			"mediamemory: list by slot slot_kind=%q: %w",
			string(slot), ErrInvalidSlotKind,
		)
	}
	if conceptID == "" {
		return nil, fmt.Errorf("mediamemory: list by slot missing concept_id: %w", ErrConceptNotFound)
	}
	return s.bindings.ListApprovedByConcept(ctx, conceptID, []SlotKind{slot}, limit)
}

// (no dead compile-time pins; time and fmt are both used through
//  normal source paths and goimports keeps their imports alive.)
