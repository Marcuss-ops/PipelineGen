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
// ErrBindingNotFound) via %w. Origin="manual" with status !=
// ApprovalApproved refuses to be persisted with origin!=manual
// (godlike/06 SSOT: only humans can approve).
//
// Anti-anti-pattern (maya_binding.go): the user's spec explicitly
// REJECTS per-topic binding files. Every domain topic (Maya, boxing,
// history, ...) routes through the SAME BindingService.
package mediamemory

import "context"

// BindingService is the canonical port for media_bindings
// mutation. Concrete impl is defaultBindingService below.
type BindingService interface {
	// Create persists a binding. UNIQUE collisions surface as
	// wrapped ErrDuplicateBinding. Origin="manual" sets
	// ApprovalStatus=approval (caller-controlled).
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

// ── Default implementation (skeleton) ─────────────────────────────

// defaultBindingService is the canonical implementation of
// BindingService.
type defaultBindingService struct {
	concepts ConceptRepository
	bindings BindingRepository
	log      Logger
}

// NewDefaultBindingService constructs the service with the canonical
// (concepts, bindings) dependency set. Composition root wires the
// SQLite repos in Phase 1.2.
func NewDefaultBindingService(concepts ConceptRepository, bindings BindingRepository, log Logger) *defaultBindingService {
	if log == nil {
		log = NoopLogger()
	}
	return &defaultBindingService{
		concepts: concepts,
		bindings: bindings,
		log:      log,
	}
}

// Compile-time assertion.
var _ BindingService = (*defaultBindingService)(nil)

// Create is the canonical entrypoint. Phase 1.x: identity stub;
// subsequent phases wire the validation chain
// (IsKnownSlotKind → ConceptRepository.FindByID → BindingsRepository.Upsert).
func (s *defaultBindingService) Create(_ context.Context, _ MediaBinding) (MediaBinding, error) {
	return MediaBinding{}, errNotImplemented("mediamemory: defaultBindingService.Create not yet implemented (Phase 1.x)")
}

func (s *defaultBindingService) Update(_ context.Context, _ MediaBinding) (MediaBinding, error) {
	return MediaBinding{}, errNotImplemented("mediamemory: defaultBindingService.Update not yet implemented (Phase 1.x)")
}

func (s *defaultBindingService) Delete(_ context.Context, _ string) error {
	return errNotImplemented("mediamemory: defaultBindingService.Delete not yet implemented (Phase 1.x)")
}

func (s *defaultBindingService) Approve(_ context.Context, _ string) error {
	return errNotImplemented("mediamemory: defaultBindingService.Approve not yet implemented (Phase 1.x)")
}

func (s *defaultBindingService) Reject(_ context.Context, _ string) error {
	return errNotImplemented("mediamemory: defaultBindingService.Reject not yet implemented (Phase 1.x)")
}

func (s *defaultBindingService) ListByConcept(_ context.Context, _ string) ([]MediaBinding, error) {
	return nil, errNotImplemented("mediamemory: defaultBindingService.ListByConcept not yet implemented (Phase 1.x)")
}

func (s *defaultBindingService) ListBySlot(_ context.Context, _ string, _ SlotKind, _ int) ([]MediaBinding, error) {
	return nil, errNotImplemented("mediamemory: defaultBindingService.ListBySlot not yet implemented (Phase 1.x)")
}
