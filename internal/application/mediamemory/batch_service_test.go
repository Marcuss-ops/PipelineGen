// Package mediamemory — batch_service_test.go is the Fase 3.1
// unit-test surface for defaultBatchService.
//
// godlike/06 SSOT (test pin set):
//   - RunCatalogOnly fan-out: (queries × providers) → BatchChild rows
//   - terminal-state AppendCandidate refusal: ErrBatchNotReconcilable
//   - Mode closed-set enforcement: ErrInvalidBatchMode
//   - Idempotent CreateBatch by Spec.Name (resume-friendly).
package mediamemory

import (
	"context"
	"errors"
	"testing"
)

// ── Reusable fixtures ──────────────────────────────────────────────

// catalogOnlyFixture builds a defaultBatchService + a DiscoveryWorker
// whose upstream SearchFanOut returns N candidates per (provider,
// query) tuple. Tests use this to drive catalog_only (3 queries ×
// 2 providers = 6 children).
func catalogOnlyFixture(t *testing.T, perChild int) (*defaultBatchService, *fakeSearchFanOutDiscovery, *fakeCandidateRepoDiscovery) {
	t.Helper()
	repo := newFakeCandidateRepoDiscovery()
	fo := &fakeSearchFanOutDiscovery{
		results: map[string]SearchFanOutResult{},
	}
	for _, prov := range []string{"artlist", "youtube"} {
		for _, q := range []string{"Maya pyramids", "Maya calendar", "Maya codices"} {
			cands := make([]MediaCandidate, 0, perChild)
			for i := 0; i < perChild; i++ {
				cands = append(cands, MediaCandidate{
					Provider:        prov,
					ProviderAssetID: prov + "-" + q + "-" + itoa(i),
					SourceURL:       "https://example/" + prov + "/" + q + "/" + itoa(i),
					Title:           prov + " " + q + " " + itoa(i),
					DurationMs:      8000,
					RightsStatus:    RightsVerified,
				})
			}
			fo.results[prov+"|"+q] = SearchFanOutResult{Candidates: cands}
		}
	}
	worker := NewDefaultDiscoveryWorker(repo, fo, NoopLogger(), nil)
	svc := NewDefaultBatchServiceWithWorker(repo, nil, nil, fo, worker, NoopLogger(), nil)
	return svc, fo, repo
}

// itoa is a tiny base-10 int-to-string helper scoped to test fixtures.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [16]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

// ── RunCatalogOnly fan-out per (query × provider) ─────────────────

func TestBatchService_RunCatalogOnly_FanOutPerProvider(t *testing.T) {
	t.Parallel()

	svc, _, repo := catalogOnlyFixture(t, 2 /*per-child candidates*/)

	spec := BatchSpec{
		Name:            "maya-catalog-fixture",
		Queries:         []string{"Maya pyramids", "Maya calendar", "Maya codices"},
		Providers:       []string{"artlist", "youtube"},
		Language:        "it",
		MediaTypes:      []string{"video"},
		MaxCandidates:   100,
		MaterializeTopK: 0,
		Mode:            ModeCatalogOnly,
	}

	parent, children, err := svc.RunCatalogOnly(context.Background(), spec)
	if err != nil {
		t.Fatalf("RunCatalogOnly error: %v", err)
	}
	// godlike/06 SSOT: 3 queries × 2 providers = 6 children.
	if len(children) != 6 {
		t.Fatalf("children count = %d, want 6 (3x2 fan-out)", len(children))
	}
	// Every child distinct: same (query, provider) per spec.
	seen := make(map[string]bool, 6)
	for _, c := range children {
		key := c.Query + "|" + c.Provider
		if seen[key] {
			t.Fatalf("duplicate child key %q", key)
		}
		seen[key] = true
	}
	// godlike/06 SSOT: parent CandidateCount = 6 children × 2
	// candidates = 12.
	if parent.CandidateCount != 12 {
		t.Fatalf("parent.CandidateCount = %d, want 12 (fan-out math)", parent.CandidateCount)
	}
	if len(repo.byID) != 12 {
		t.Fatalf("repo persisted %d rows, want 12", len(repo.byID))
	}
	// godlike/06 SSOT: every persisted row carries canonical Cold-
	// tier + Searched status (worker default).
	for id, c := range repo.byID {
		if c.MaterializationStatus != MaterializationCold {
			t.Fatalf("persisted %q MaterializationStatus = %q, want Cold",
				id, string(c.MaterializationStatus))
		}
		if c.DiscoveryStatus != DiscoverySearched {
			t.Fatalf("persisted %q DiscoveryStatus = %q, want Searched",
				id, string(c.DiscoveryStatus))
		}
		if c.AssetID != "" {
			t.Fatalf("persisted %q AssetID = %q (must be empty Cold tier)", id, c.AssetID)
		}
	}
}

// ── terminal-state AppendCandidate refusal ──────────────────────────

func TestBatchService_AppendCandidate_TerminalStateFailsClosed(t *testing.T) {
	t.Parallel()

	svc, _, _ := catalogOnlyFixture(t, 1)

	parent, _, err := svc.CreateBatch(context.Background(), BatchSpec{
		Name:          "terminal-refusal-test",
		Queries:       []string{"q1"},
		Providers:     []string{"p1"},
		MaxCandidates: 10,
		Mode:          ModeCatalogOnly,
	})
	if err != nil {
		t.Fatalf("CreateBatch error: %v", err)
	}
	// Reconcile to force terminal-state rewrite. Even though no
	// children are in flight, the parent becomes Completed (no
	// children in flight / no failed children).
	_, err = svc.Reconcile(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	batch, _ := svc.Get(context.Background(), parent.ID)
	if batch.State != BatchCompleted {
		t.Fatalf("parent state after Reconcile = %q, want Completed", string(batch.State))
	}
	// AppendCandidate MUST refuse now (terminal-state guard).
	err = svc.AppendCandidate(context.Background(), batch.Children[0], MediaCandidate{
		Provider: "p1", ProviderAssetID: "late", SourceURL: "https://x",
	})
	if err == nil {
		t.Fatalf("AppendCandidate accepted a row after terminal-state Reconcile")
	}
	if !errors.Is(err, ErrBatchNotReconcilable) {
		t.Fatalf("error = %v, want wrapped ErrBatchNotReconcilable", err)
	}
}

// ── Mode closed-set enforcement ────────────────────────────────────

func TestBatchService_CreateBatch_RejectsUnknownMode(t *testing.T) {
	t.Parallel()

	repo := newFakeCandidateRepoDiscovery()
	fo := &fakeSearchFanOutDiscovery{}
	worker := NewDefaultDiscoveryWorker(repo, fo, NoopLogger(), nil)
	svc := NewDefaultBatchServiceWithWorker(repo, nil, nil, fo, worker, NoopLogger(), nil)

	_, _, err := svc.CreateBatch(context.Background(), BatchSpec{
		Name:          "unknown-mode",
		Queries:       []string{"q1"},
		Providers:     []string{"p1"},
		MaxCandidates: 5,
		Mode:          "garbage_mode",
	})
	if err == nil {
		t.Fatalf("CreateBatch accepted unknown mode")
	}
	if !errors.Is(err, ErrInvalidBatchMode) {
		t.Fatalf("error = %v, want wrapped ErrInvalidBatchMode", err)
	}
}

func TestBatchService_CreateBatch_RejectsEmptyName(t *testing.T) {
	t.Parallel()

	svc, _, _ := catalogOnlyFixture(t, 0)
	_, _, err := svc.CreateBatch(context.Background(), BatchSpec{
		Queries: []string{"q1"}, Providers: []string{"p1"},
		MaxCandidates: 5, Mode: ModeCatalogOnly,
	})
	if err == nil {
		t.Fatalf("CreateBatch accepted empty Name")
	}
	if !errors.Is(err, ErrInvalidPhrase) {
		t.Fatalf("error = %v, want wrapped ErrInvalidPhrase", err)
	}
}

// ── Idempotent-by-name CreateBatch (resume-friendly) ───────────────

func TestBatchService_CreateBatch_IdempotentByName(t *testing.T) {
	t.Parallel()

	svc, _, _ := catalogOnlyFixture(t, 1)

	spec := BatchSpec{
		Name:          "idempotent-fixture",
		Queries:       []string{"q1"},
		Providers:     []string{"p1"},
		MaxCandidates: 5,
		Mode:          ModeCatalogOnly,
	}
	first, _, err := svc.CreateBatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("first CreateBatch error: %v", err)
	}
	// second call with same Name MUST return the same ID + same children.
	second, children, err := svc.CreateBatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("second CreateBatch error: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second.ID = %q, want %q (idempotent-by-name)", second.ID, first.ID)
	}
	if len(children) != 1 {
		t.Fatalf("second children count = %d, want 1", len(children))
	}
	if children[0].ID != first.Children[0] {
		t.Fatalf("second children[0].ID = %q, want %q",
			children[0].ID, first.Children[0])
	}
}

// TestBatchService_CreateBatch_RejectsNameMatchesSpecDrift pins the
// godlike/06 SSOT contract: when CreateBatch is called twice with
// the same Spec.Name BUT a different Spec body (here: Mode flips
// from catalog_only to materialize_top_k), the second call MUST
// surface wrapped ErrBatchSpecDrift instead of silently
// overwriting the parent's Spec.
func TestBatchService_CreateBatch_RejectsNameMatchesSpecDrift(t *testing.T) {
	t.Parallel()

	svc, _, _ := catalogOnlyFixture(t, 1)

	spec := BatchSpec{
		Name:          "drift-fixture",
		Queries:       []string{"q1"},
		Providers:     []string{"p1"},
		MaxCandidates: 5,
		Mode:          ModeCatalogOnly,
	}
	if _, _, err := svc.CreateBatch(context.Background(), spec); err != nil {
		t.Fatalf("first CreateBatch error: %v", err)
	}

	drifted := spec
	drifted.Mode = ModeMaterializeTopK
	drifted.MaterializeTopK = 10

	if _, _, err := svc.CreateBatch(context.Background(), drifted); err == nil {
		t.Fatalf("CreateBatch accepted Spec drift (Mode catalog_only -> materialize_top_k)")
	} else if !errors.Is(err, ErrBatchSpecDrift) {
		t.Fatalf("CreateBatch drift error = %v, want wrapped ErrBatchSpecDrift (NOT ErrInvalidPhrase)", err)
	}
}

// TestBatchService_Pin_FailSafeUntypedSpecIsRejected pins the
// closed-set guard: a Spec with an UNKNOWN Mode value (i.e. not
// in {catalog_only, materialize_top_k, ""}) never reaches the
// idempotent-by-name path because validateSpec rejects it
// up-front under wrapped ErrInvalidBatchMode.
func TestBatchService_Pin_FailSafeUnknownSpecModeRejected(t *testing.T) {
	t.Parallel()

	svc, _, _ := catalogOnlyFixture(t, 0)

	_, _, err := svc.CreateBatch(context.Background(), BatchSpec{
		Name:          "fail-safe-mode",
		Queries:       []string{"q1"},
		Providers:     []string{"p1"},
		MaxCandidates: 5,
		Mode:          BatchMode("not_a_real_mode"),
	})
	if err == nil {
		t.Fatalf("CreateBatch accepted unknown BatchMode")
	}
	if !errors.Is(err, ErrInvalidBatchMode) {
		t.Fatalf("error = %v, want wrapped ErrInvalidBatchMode", err)
	}
}

// Compile-time pin.
var _ BatchService = (*defaultBatchService)(nil)
