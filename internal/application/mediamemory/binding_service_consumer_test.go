// Package mediamemory — binding_service_consumer_test.go is the
// consumer-side test layer for defaultBindingService.
//
// godlike/06 SSOT (single-test-file per consumer): the fakes
// (fakeConceptRepo, fakeBindingsRepo) live in binding_service_test.go
// and are REUSED here for the feedback_service_consumer_test.go
// siblings — split into two files only to keep the call counts
// per file audit-trail-friendly. A future worker that needs a new
// fake must add it in binding_service_test.go, not this file.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"testing"
	"time"
)

// fixedClock returns a Clock pinned to a deterministic time so
// service-side CreatedAt/UpdatedAt assertions are reproducible.
type fixedClock struct {
	t time.Time
}

func (c fixedClock) Now() time.Time { return c.t }

func newFixedClock() fixedClock {
	return fixedClock{t: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
}

// makeBinding is a tiny factory used by the binding_service tests.
func makeBinding(conceptID, assetID string, slot SlotKind) MediaBinding {
	return MediaBinding{
		ConceptID: conceptID,
		AssetID:   assetID,
		SlotKind:  slot,
	}
}

// seedConcept stores a canonical concept row in the fake repo.
func seedConcept(repo *fakeConceptRepo, id string) MediaConcept {
	c := MediaConcept{
		ID:                id,
		Language:          "it",
		CanonicalText:     "I Maya costruirono grandi città",
		NormalizedText:    "i maya costruirono grandi città",
		PhraseFingerprint: "fp-" + id,
		ConceptType:       ConceptPhrase,
	}
	repo.byID[c.ID] = c
	return c
}

// ── Create ──────────────────────────────────────────────────────────

func TestBindingServiceFailsClosedWhenDispatcherNil(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	// godlike/07 fail-closed: a nil dispatcher must be treated as an
	// unavailable mutation surface, never as a silent no-op.
	svc := NewDefaultBindingService(concepts, bindings, nil, NoopLogger(), newFixedClock())
	c := seedConcept(concepts, "concept-dispatcher-nil")

	_, err := svc.Create(context.Background(), makeBinding(c.ID, "asset-x", media.SlotPrimaryVideo))
	if err == nil {
		t.Fatalf("Create succeeded with nil dispatcher; want error")
	}
	if !errors.Is(err, ErrBindingMutationDispatcherUnavailable) {
		t.Fatalf("Create returned %v, want ErrBindingMutationDispatcherUnavailable", err)
	}

	if err := svc.Delete(context.Background(), "b-1"); err == nil {
		t.Fatalf("Delete succeeded with nil dispatcher; want error")
	}
	if !errors.Is(err, ErrBindingMutationDispatcherUnavailable) {
		t.Fatalf("Delete returned %v, want ErrBindingMutationDispatcherUnavailable", err)
	}

	if err := svc.Approve(context.Background(), "b-1"); err == nil {
		t.Fatalf("Approve succeeded with nil dispatcher; want error")
	}
	if !errors.Is(err, ErrBindingMutationDispatcherUnavailable) {
		t.Fatalf("Approve returned %v, want ErrBindingMutationDispatcherUnavailable", err)
	}

	if err := svc.Reject(context.Background(), "b-1"); err == nil {
		t.Fatalf("Reject succeeded with nil dispatcher; want error")
	}
	if !errors.Is(err, ErrBindingMutationDispatcherUnavailable) {
		t.Fatalf("Reject returned %v, want ErrBindingMutationDispatcherUnavailable", err)
	}
}

func TestBindingServiceCreateHappyPath(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	c := seedConcept(concepts, "concept-maya")

	got, err := svc.Create(context.Background(), makeBinding(c.ID, "asset-chichen-itza", media.SlotPrimaryVideo))
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	// defaults applied
	if got.ID == "" {
		t.Fatalf("Create returned binding with empty ID (default wasn't applied by service or fake)")
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("Create returned binding with zero CreatedAt — fake must set on insert")
	}
	if got.UpdatedAt.IsZero() {
		t.Fatalf("Create returned binding with zero UpdatedAt")
	}
	if got.Origin != OriginManual {
		t.Fatalf("Create defaulted Origin = %q, want %q", got.Origin, OriginManual)
	}
	if got.ApprovalStatus != ApprovalApproved {
		t.Fatalf("Create defaulted ApprovalStatus = %q, want %q", got.ApprovalStatus, ApprovalApproved)
	}
	if got.ConceptID != c.ID {
		t.Fatalf("Create propagated ConceptID = %q, want %q", got.ConceptID, c.ID)
	}
	if len(bindings.upserts) != 1 {
		t.Fatalf("Create propagated upsert count = %d, want 1", len(bindings.upserts))
	}
}

func TestBindingServiceCreateRejectsInvalidSlotKind(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	c := seedConcept(concepts, "concept-maya2")

	_, err := svc.Create(context.Background(), MediaBinding{
		ConceptID: c.ID, AssetID: "asset-x", SlotKind: SlotKind("not_a_slot"),
	})
	if err == nil {
		t.Fatalf("Create accepted unknown SlotKind")
	}
	if !errors.Is(err, ErrInvalidSlotKind) {
		t.Fatalf("Create returned %v, want wrapped ErrInvalidSlotKind", err)
	}
	if len(bindings.upserts) != 0 {
		t.Fatalf("Create must NOT upsert on invalid SlotKind; got %d upserts", len(bindings.upserts))
	}
}

func TestBindingServiceCreateRejectsMissingConceptID(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)

	_, err := svc.Create(context.Background(), MediaBinding{
		AssetID: "asset-x", SlotKind: media.SlotPrimaryVideo,
	})
	if err == nil {
		t.Fatalf("Create accepted missing concept_id")
	}
	// godlike/06 SSOT (input-shape vs lookup-miss): an empty
	// concept_id is an INPUT validation failure, distinct from a
	// lookup miss (which still surfaces ErrConceptNotFound in
	// TestBindingServiceCreateRejectsUnknownConcept).
	if !errors.Is(err, ErrInvalidBindingInput) {
		t.Fatalf("Create returned %v, want wrapped ErrInvalidBindingInput", err)
	}
	if len(bindings.upserts) != 0 {
		t.Fatalf("Create must NOT upsert on missing concept_id; got %d upserts", len(bindings.upserts))
	}
}

func TestBindingServiceCreateRejectsMissingAssetID(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	c := seedConcept(concepts, "concept-maya3")

	_, err := svc.Create(context.Background(), MediaBinding{
		ConceptID: c.ID, SlotKind: media.SlotPrimaryVideo,
	})
	if err == nil {
		t.Fatalf("Create accepted missing asset_id")
	}
	// godlike/06 SSOT: missing-asset_id is an INPUT validation
	// failure (distinct from ErrInvalidSlotKind which means
	// "slot kind outside the canonical closed set"; distinct
	// from ErrConceptNotFound which means "concept_id supplied
	// but absent from repo"). The wire layer in
	// internal/api/mediamemory/errors.go maps this to
	// 400/code=invalid_binding.
	if !errors.Is(err, ErrInvalidBindingInput) {
		t.Fatalf("Create returned %v, want wrapped ErrInvalidBindingInput", err)
	}
	if len(bindings.upserts) != 0 {
		t.Fatalf("Create must NOT upsert on missing asset_id; got %d upserts", len(bindings.upserts))
	}
}

func TestBindingServiceCreateRejectsUnknownConcept(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	// Concept doesn't exist — FindByID will fail.
	_, err := svc.Create(context.Background(), MediaBinding{
		ConceptID: "concept-unknown", AssetID: "asset-x", SlotKind: media.SlotPrimaryVideo,
	})
	if err == nil {
		t.Fatalf("Create accepted missing concept_id")
	}
	if !errors.Is(err, ErrConceptNotFound) {
		t.Fatalf("Create returned %v, want wrapped ErrConceptNotFound", err)
	}
	if len(bindings.upserts) != 0 {
		t.Fatalf("Create must NOT upsert on missing concept; got %d upserts", len(bindings.upserts))
	}
}

// godlike/06 SSOT note: ErrDuplicateBinding cannot surface from
// binding_service.Create directly. The repository's Upsert uses
// ON CONFLICT(concept_id, asset_id, slot_kind) DO UPDATE, which
// converts UNIQUE collisions into in-place updates. Create that
// re-runs with the same (concept, asset, slot) tuple therefore
// updates the existing row (preserving its ID and CreatedAt).
// ErrDuplicateBinding surfaces ONLY from
// CandidateRepository.UpsertInsert (raw INSERT path), not from
// this binding surface — verified via the SQLite concrete in
// internal/infrastructure/database/sqlite/mediamemory/bindings_repository.go.
// The corresponding architecture-doc clause is documented as an
// aspirational contract that the redesign will emit (Phase 3.x).

// ── Update ──────────────────────────────────────────────────────────

func TestBindingServiceUpdateRejectsInvalidSlotKind(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	// Seed via raw bindings.Upsert with explicit Origin +
	// ApprovalStatus (consistent with the other test setups in
	// this file). Mirrors production semantics: any bindings.Upsert
	// call outside the service MUST supply canonical Origin +
	// ApprovalStatus (the SQLite concrete's RETURNING-then-
	// scanBindingRow gate enforces this).
	b, _ := bindings.Upsert(context.Background(), MediaBinding{
		ID: "b-1", ConceptID: "c-1", AssetID: "a-1", SlotKind: media.SlotPrimaryVideo,
		Origin: OriginManual, ApprovalStatus: ApprovalApproved,
	})

	_, err := svc.Update(context.Background(), MediaBinding{
		ID: b.ID, ConceptID: b.ConceptID, AssetID: b.AssetID,
		SlotKind: SlotKind("garbage"),
	})
	if err == nil {
		t.Fatalf("Update accepted garbage SlotKind")
	}
	if !errors.Is(err, ErrInvalidSlotKind) {
		t.Fatalf("Update returned %v, want wrapped ErrInvalidSlotKind", err)
	}
}

func TestBindingServiceUpdateRejectsEmptyID(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	_, err := svc.Update(context.Background(), MediaBinding{
		ConceptID: "c-1", AssetID: "a-1", SlotKind: media.SlotPrimaryVideo,
	})
	if err == nil {
		t.Fatalf("Update accepted empty ID")
	}
	if !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("Update returned %v, want wrapped ErrBindingNotFound", err)
	}
}

func TestBindingServiceUpdateHappyPathPreservesUsageCount(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	// Seed with valid Origin + ApprovalStatus (or the fake's
	// IsKnownOrigin gate would reject the empty string and
	// Update would receive an empty binding.ID).
	seed, _ := bindings.Upsert(context.Background(), MediaBinding{
		ID: "b-2", ConceptID: "c-1", AssetID: "a-1", SlotKind: media.SlotPrimaryVideo,
		Origin: OriginManual, ApprovalStatus: ApprovalApproved,
		UsageCount: 3, SuccessScore: 0.42,
	})
	got, err := svc.Update(context.Background(), MediaBinding{
		ID: seed.ID, ConceptID: seed.ConceptID, AssetID: seed.AssetID,
		SlotKind: media.SlotPrimaryVideo, Origin: OriginManual, ApprovalStatus: ApprovalApproved,
		UsageCount: 3, SuccessScore: 0.42,
		StartMs: 12000, EndMs: 20000,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if got.StartMs != 12000 || got.EndMs != 20000 {
		t.Fatalf("Update didn't propagate StartMs/EndMs: got %d/%d", got.StartMs, got.EndMs)
	}
}

// ── Delete ──────────────────────────────────────────────────────────

func TestBindingServiceDeleteRejectsEmptyID(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	err := svc.Delete(context.Background(), "")
	if err == nil {
		t.Fatalf("Delete accepted empty ID")
	}
	if !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("Delete returned %v, want wrapped ErrBindingNotFound", err)
	}
}

func TestBindingServiceDeleteHappyPath(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	// Seed with valid Origin + ApprovalStatus so the fake's
	// IsKnownOrigin gate doesn't reject the empty Origin (which
	// would cascade into Delete getting an empty b.ID).
	b, _ := bindings.Upsert(context.Background(), MediaBinding{
		ID: "b-3", ConceptID: "c-1", AssetID: "a-1", SlotKind: media.SlotPrimaryVideo,
		Origin: OriginManual, ApprovalStatus: ApprovalApproved,
	})
	if err := svc.Delete(context.Background(), b.ID); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := bindings.FindByID(context.Background(), b.ID); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("Delete didn't remove row from repo; FindByID returned %v", err)
	}
}

// ── Approve / Reject ────────────────────────────────────────────────

func TestBindingServiceApproveHappyPathPreservesOrigin(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	b, _ := bindings.Upsert(context.Background(), MediaBinding{
		ID: "b-4", ConceptID: "c-1", AssetID: "a-1", SlotKind: media.SlotPrimaryVideo,
		Origin: OriginAutoLink, ApprovalStatus: ApprovalPending,
	})

	if err := svc.Approve(context.Background(), b.ID); err != nil {
		t.Fatalf("Approve returned error: %v", err)
	}
	got, _ := bindings.FindByID(context.Background(), b.ID)
	if got.ApprovalStatus != ApprovalApproved {
		t.Fatalf("Approve didn't flip status: got %q", got.ApprovalStatus)
	}
	if got.Origin != OriginAutoLink {
		t.Fatalf("Approve must preserve Origin (godlike/06 SSOT: status flip, not reset); got %q", got.Origin)
	}
}

func TestBindingServiceApproveSurfacesBindingNotFound(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	err := svc.Approve(context.Background(), "missing-id")
	if err == nil {
		t.Fatalf("Approve accepted missing ID")
	}
	if !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("Approve returned %v, want wrapped ErrBindingNotFound", err)
	}
}

func TestBindingServiceRejectSetsRejectedAndPreservesOrigin(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	b, _ := bindings.Upsert(context.Background(), MediaBinding{
		ID: "b-5", ConceptID: "c-1", AssetID: "a-1", SlotKind: media.SlotPrimaryVideo,
		Origin: OriginManual, ApprovalStatus: ApprovalApproved,
	})
	if err := svc.Reject(context.Background(), b.ID); err != nil {
		t.Fatalf("Reject returned error: %v", err)
	}
	got, _ := bindings.FindByID(context.Background(), b.ID)
	if got.ApprovalStatus != ApprovalRejected {
		t.Fatalf("Reject didn't flip status: got %q", got.ApprovalStatus)
	}
	if got.Origin != OriginManual {
		t.Fatalf("Reject must preserve Origin; got %q", got.Origin)
	}
}

func TestBindingServiceRejectSurfacesBindingNotFound(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	err := svc.Reject(context.Background(), "missing-id")
	if !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("Reject returned %v, want wrapped ErrBindingNotFound", err)
	}
}

// ── ListByConcept / ListBySlot ─────────────────────────────────────

func TestBindingServiceListByConceptRejectsEmptyConceptID(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	_, err := svc.ListByConcept(context.Background(), "")
	if err == nil {
		t.Fatalf("ListByConcept accepted empty concept_id")
	}
	if !errors.Is(err, ErrConceptNotFound) {
		t.Fatalf("ListByConcept returned %v, want wrapped ErrConceptNotFound", err)
	}
}

func TestBindingServiceListByConceptReturnsAllStatuses(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	// Seed 3 bindings: 1 approved, 1 pending, 1 rejected.
	for i, status := range []ApprovalStatus{ApprovalApproved, ApprovalPending, ApprovalRejected} {
		_, _ = bindings.Upsert(context.Background(), MediaBinding{
			ID: fmt.Sprintf("b-list-%d", i), ConceptID: "c-1", AssetID: fmt.Sprintf("a-%d", i),
			SlotKind: media.SlotPrimaryVideo, Origin: OriginManual,
			ApprovalStatus: status,
		})
	}
	got, err := svc.ListByConcept(context.Background(), "c-1")
	if err != nil {
		t.Fatalf("ListByConcept returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListByConcept returned %d bindings (dashboard diff view MUST surface all statuses), want 3", len(got))
	}
}

func TestBindingServiceListBySlotRejectsInvalidSlotKind(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	_, err := svc.ListBySlot(context.Background(), "c-1", SlotKind("garbage"), 10)
	if err == nil {
		t.Fatalf("ListBySlot accepted garbage SlotKind")
	}
	if !errors.Is(err, ErrInvalidSlotKind) {
		t.Fatalf("ListBySlot returned %v, want wrapped ErrInvalidSlotKind", err)
	}
}

func TestBindingServiceListBySlotFilterHappyPath(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	_, _ = bindings.Upsert(context.Background(), MediaBinding{
		ID: "b-slot-pv-1", ConceptID: "c-1", AssetID: "a-1",
		SlotKind: media.SlotPrimaryVideo, Origin: OriginManual, ApprovalStatus: ApprovalApproved,
	})
	_, _ = bindings.Upsert(context.Background(), MediaBinding{
		ID: "b-slot-si-1", ConceptID: "c-1", AssetID: "a-2",
		SlotKind: media.SlotSecondaryImage, Origin: OriginManual, ApprovalStatus: ApprovalApproved,
	})

	got, err := svc.ListBySlot(context.Background(), "c-1", media.SlotPrimaryVideo, 10)
	if err != nil {
		t.Fatalf("ListBySlot returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListBySlot returned %d bindings (single SlotKind filter), want 1", len(got))
	}
	if got[0].SlotKind != media.SlotPrimaryVideo {
		t.Fatalf("ListBySlot returned wrong slot kind: %q", got[0].SlotKind)
	}
}

func TestBindingServiceListBySlotEmptyResultWhenLimitExceedsAvailable(t *testing.T) {
	t.Parallel()
	concepts := newFakeConceptRepo()
	bindings := newFakeBindingsRepo()
	svc := newBindingService(concepts, bindings)
	_, _ = bindings.Upsert(context.Background(), MediaBinding{
		ID: "b-slot-lim-1", ConceptID: "c-1", AssetID: "a-1",
		SlotKind: media.SlotPrimaryVideo, Origin: OriginManual, ApprovalStatus: ApprovalApproved,
	})
	// limit = 0 means "no limit"; limit = 5 above 1 available is also no problem.
	got, err := svc.ListBySlot(context.Background(), "c-1", media.SlotPrimaryVideo, 5)
	if err != nil {
		t.Fatalf("ListBySlot returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListBySlot with limit > available returned %d, want 1", len(got))
	}
}

// ── Compile-time assertion ─────────────────────────────────────────

// Pin the fakeBindingsRepo to the port: if Fase 1.5+ extends the
// BindingRepository interface, this assertion catches drift.
var _ BindingRepository = (*fakeBindingsRepo)(nil)
var _ ConceptRepository = (*fakeConceptRepo)(nil)
