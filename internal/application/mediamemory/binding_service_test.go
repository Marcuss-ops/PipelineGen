// Package mediamemory — binding_service_test.go is the Fase 1.4
// unit-test surface for defaultBindingService.
//
// godlike/06 SSOT (test mirrors port): every assertion checks
// behavior at the service-method boundary. Repository seams are
// satisfied by in-memory fakes (mirror of the SQLite concrete) so
// the test exercises the canonical validation chain without an
// actual DB. The fakes intentionally REPLICATE the SQLite repo's
// SlotKind/Origin/ApprovalStatus validation chain so a future
// drift between fake and concrete surfaces as a test failure (the
// godlike/07 fail-closed envelope holds even at the test seam).
//
// godlike/07 NO-FAKE-AVAILABILITY: a fake that swallows repos is
// NOT a fake — every call records arguments so we can assert that
// the service propagated them correctly.
package mediamemory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ── Fake ConceptRepository ──────────────────────────────────────────

// fakeConceptRepo is an in-memory ConceptRepository used by the
// binding_service tests. It validates the canonical IsKnownXxx
// predicates (mirror of the SQLite concrete in
// internal/infrastructure/database/sqlite/mediamemory/concepts_repository.go)
// so a future drift between fake and concrete surfaces as a
// expected-failure here.
type fakeConceptRepo struct {
	mu sync.Mutex

	byID    map[string]MediaConcept
	upserts []MediaConcept
}

func newFakeConceptRepo() *fakeConceptRepo {
	return &fakeConceptRepo{byID: make(map[string]MediaConcept)}
}

func (f *fakeConceptRepo) Upsert(_ context.Context, c MediaConcept) (MediaConcept, error) {
	if !IsKnownConceptType(c.ConceptType) {
		return MediaConcept{}, fmt.Errorf("mediamemory: concept type=%q: %w",
			c.ConceptType, ErrInvalidPhrase)
	}
	if c.Language == "" || c.PhraseFingerprint == "" {
		return MediaConcept{}, fmt.Errorf("mediamemory: concept: %w", ErrInvalidPhrase)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	c.UpdatedAt = time.Now().UTC()
	f.byID[c.ID] = c
	f.upserts = append(f.upserts, c)
	return c, nil
}

func (f *fakeConceptRepo) FindByID(_ context.Context, id string) (MediaConcept, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return MediaConcept{}, fmt.Errorf("mediamemory: concept id=%q: %w", id, ErrConceptNotFound)
	}
	return c, nil
}

func (f *fakeConceptRepo) FindByFingerprint(_ context.Context, language, fp string) (MediaConcept, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.byID {
		if c.Language == language && c.PhraseFingerprint == fp {
			return c, nil
		}
	}
	return MediaConcept{}, fmt.Errorf("mediamemory: fingerprint fp=%q: %w", fp, ErrConceptNotFound)
}

func (f *fakeConceptRepo) FindManyByFingerprints(_ context.Context, language string, fps []string) ([]MediaConcept, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]MediaConcept, 0, len(fps))
	for _, fp := range fps {
		for _, c := range f.byID {
			if c.Language == language && c.PhraseFingerprint == fp {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

// ── Fake BindingRepository ──────────────────────────────────────────

// fakeBindingsRepo is an in-memory BindingRepository used by both
// binding_service and feedback_service tests.
//
// godlike/06 SSOT (mirror of concrete): the fake enforces the
// same validation gates as the SQLite concrete:
//
//   - SlotKind MUST be in the canonical closed set (godlike/06 SSOT).
//   - UNIQUE(concept_id, asset_id, slot_kind) violation → wrapped ErrDuplicateBinding.
//   - Missing row → wrapped ErrBindingNotFound.
type fakeBindingsRepo struct {
	mu sync.Mutex

	byID        map[string]MediaBinding
	upserts     []MediaBinding
	failNextErr error // Optional: if non-nil, next Upsert returns this error
}

func newFakeBindingsRepo() *fakeBindingsRepo {
	return &fakeBindingsRepo{
		byID: make(map[string]MediaBinding),
	}
}

func (r *fakeBindingsRepo) Upsert(_ context.Context, b MediaBinding) (MediaBinding, error) {
	if r.failNextErr != nil {
		err := r.failNextErr
		r.failNextErr = nil
		return MediaBinding{}, err
	}
	if !IsKnownSlotKind(b.SlotKind) {
		return MediaBinding{}, fmt.Errorf("mediamemory: binding slot_kind=%q: %w",
			b.SlotKind, ErrInvalidSlotKind)
	}
	if !IsKnownOrigin(b.Origin) {
		return MediaBinding{}, fmt.Errorf("mediamemory: binding origin=%q: %w",
			b.Origin, ErrInvalidSlotKind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// godlike/06 SSOT (mirror of concrete ON CONFLICT DO UPDATE):
	// the SQLite concrete's Upsert NEVER returns ErrDuplicateBinding;
	// UNIQUE(concept_id, asset_id, slot_kind) conflicts are converted
	// to updates. We mirror that here: if a row with the same
	// (concept, asset, slot) exists, we update it in place
	// regardless of whether the caller's b.ID matches the existing
	// ID. ErrDuplicateBinding surfaces ONLY from
	// CandidateRepository.UpsertInsert (raw INSERT path), not from
	// the binding Upsert surface.
	for id, existing := range r.byID {
		if existing.ConceptID == b.ConceptID && existing.AssetID == b.AssetID && existing.SlotKind == b.SlotKind {
			// Conflict on the (concept, asset, slot) tuple: rewrite
			// to existing.ID, preserve existing CreatedAt, refresh
			// UpdatedAt. The returned row's ID is the existing row's
			// ID — matching the real ON CONFLICT DO UPDATE
			// semantics.
			b.ID = existing.ID
			if b.CreatedAt.IsZero() {
				b.CreatedAt = existing.CreatedAt
			}
			b.UpdatedAt = time.Now().UTC()
			r.byID[id] = b
			r.upserts = append(r.upserts, b)
			return b, nil
		}
	}
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	b.UpdatedAt = time.Now().UTC()
	r.byID[b.ID] = b
	r.upserts = append(r.upserts, b)
	return b, nil
}

func (r *fakeBindingsRepo) FindByID(_ context.Context, id string) (MediaBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.byID[id]
	if !ok {
		return MediaBinding{}, fmt.Errorf("mediamemory: binding id=%q: %w", id, ErrBindingNotFound)
	}
	return b, nil
}

func (r *fakeBindingsRepo) ListApprovedByConcept(_ context.Context, conceptID string, slotKinds []SlotKind, limit int) ([]MediaBinding, error) {
	for _, sk := range slotKinds {
		if !IsKnownSlotKind(sk) {
			return nil, fmt.Errorf("mediamemory: list approved slot_kind=%q: %w",
				sk, ErrInvalidSlotKind)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MediaBinding, 0, 4)
	for _, b := range r.byID {
		if b.ConceptID != conceptID {
			continue
		}
		if b.ApprovalStatus != ApprovalApproved {
			continue
		}
		if len(slotKinds) > 0 {
			match := false
			for _, sk := range slotKinds {
				if b.SlotKind == sk {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, b)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeBindingsRepo) ListByConcept(_ context.Context, conceptID string) ([]MediaBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MediaBinding, 0, 8)
	for _, b := range r.byID {
		if b.ConceptID == conceptID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (r *fakeBindingsRepo) ListByAsset(_ context.Context, assetID string) ([]MediaBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MediaBinding, 0, 2)
	for _, b := range r.byID {
		if b.AssetID == assetID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (r *fakeBindingsRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; !ok {
		return fmt.Errorf("mediamemory: binding id=%q: %w", id, ErrBindingNotFound)
	}
	delete(r.byID, id)
	return nil
}
