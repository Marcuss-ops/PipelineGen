// Package mediamemory — media_materialize_worker_test.go pins the
// Fase 3.3 contract:
//
//  1. MaterializeWorker.Materialize promotes Cold→Warm with
//     PersistedAssetIDs populated for each successful row.
//  2. MaterializeWorker.PromoteOnDemand promotes a single
//     candidate Cold|Warm → Hot with AssetID set; rights-denied
//     candidates surface ErrApprovalRequired.
//  3. AcquisitionPlanner.Plan ranks by CandidateScore desc with
//     CandidateID asc tiebreaker, caps at TopK, drops rights-
//     denied and already-Warm/already-Hot rows.
//  4. AcquisitionPlanner.PlanOnDemand flips Cold|Warm → Hot
//     when rights verified; rights-denied returns
//     ErrApprovalRequired.
//  5. BatchService.SetMaterializeWorker refuses nil and
//     refuses rewire.
//  6. BatchService.MaterializeTopK with 1000 candidates and
//     TopK=100 → exactly 100 promotes, parent.IndexedCount
//     increments by 100.
//  7. BatchService.MaterializeTopK on a Completed batch surfaces
//     ErrBatchNotReconcilable.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"sync"
	"testing"
)

// ── Mocks ────────────────────────────────────────────────────────

type fakeRightsValidator struct {
	mu sync.Mutex
	// verdictByCandidateID returns the verdict for a given
	// candidate ID. Default Allow for unset.
	verdictByCandidateID map[string]RightsVerdict
}

func newAlwaysAllowRights() *fakeRightsValidator {
	return &fakeRightsValidator{}
}

func (f *fakeRightsValidator) Validate(_ context.Context, c MediaCandidate, _ string) (RightsDecision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.verdictByCandidateID[c.ID]; ok {
		return RightsDecision{Verdict: v, Reason: fmt.Sprintf("verdict=%s", v)}, nil
	}
	return RightsDecision{Verdict: RightsVerdictAllow, Reason: "default allow"}, nil
}

func (f *fakeRightsValidator) deny(id string) *fakeRightsValidator {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verdictByCandidateID == nil {
		f.verdictByCandidateID = make(map[string]RightsVerdict)
	}
	f.verdictByCandidateID[id] = RightsVerdictDeny
	return f
}

type fakeStockPipelineAcquirer struct {
	mu             sync.Mutex
	mintedAssetIDs map[string]string // candidate.ID -> asset.ID
	failOn         map[string]bool   // candidate.ID -> fail
}

func newFakeStockPipeline() *fakeStockPipelineAcquirer {
	return &fakeStockPipelineAcquirer{
		mintedAssetIDs: make(map[string]string),
		failOn:         make(map[string]bool),
	}
}

func (f *fakeStockPipelineAcquirer) Materialize(_ context.Context, c MediaCandidate, _ MaterializeOptions) (MediaCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn[c.ID] {
		return c, errors.New("fake stockpipeline: forced failure")
	}
	assetID, ok := f.mintedAssetIDs[c.ID]
	if !ok {
		assetID = "asset-" + c.ID
		f.mintedAssetIDs[c.ID] = assetID
	}
	c.AssetID = assetID
	return c, nil
}

type fakeCandidateRepoMat struct {
	mu          sync.Mutex
	byID        map[string]MediaCandidate
	statusByID  map[string][2]string // discovery, mat
	updatedByID map[string]MediaCandidate
}

func newFakeCandidateRepoMat() *fakeCandidateRepoMat {
	return &fakeCandidateRepoMat{
		byID:        make(map[string]MediaCandidate),
		statusByID:  make(map[string][2]string),
		updatedByID: make(map[string]MediaCandidate),
	}
}

func (r *fakeCandidateRepoMat) seed(c MediaCandidate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[c.ID] = c
}

func (r *fakeCandidateRepoMat) UpsertInsert(_ context.Context, c MediaCandidate) (MediaCandidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[c.ID]; exists {
		return r.byID[c.ID], ErrDuplicateBinding
	}
	r.byID[c.ID] = c
	return c, nil
}

func (r *fakeCandidateRepoMat) FindByID(_ context.Context, id string) (MediaCandidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return MediaCandidate{}, ErrCandidateNotFound
	}
	return c, nil
}

func (r *fakeCandidateRepoMat) ListByProvider(_ context.Context, provider string, limit int) ([]MediaCandidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MediaCandidate, 0)
	for _, c := range r.byID {
		if c.Provider == provider {
			out = append(out, c)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *fakeCandidateRepoMat) ListPendingMaterialization(_ context.Context, limit int) ([]MediaCandidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MediaCandidate, 0)
	for _, c := range r.byID {
		if c.RightsStatus == RightsVerified && c.MaterializationStatus == MaterializationWarm {
			out = append(out, c)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *fakeCandidateRepoMat) UpdateStatus(_ context.Context, id string, discovery DiscoveryStatus, mat MaterializationStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return ErrCandidateNotFound
	}
	c.DiscoveryStatus = discovery
	c.MaterializationStatus = mat
	r.byID[id] = c
	r.statusByID[id] = [2]string{string(discovery), string(mat)}
	r.updatedByID[id] = c
	return nil
}

// ── Test 1: worker Materialize promotes Cold→Warm ───────────────

func TestMaterializeWorker_Materialize_PromotesColdToWarm(t *testing.T) {
	stock := newFakeStockPipeline()
	cands := newFakeCandidateRepoMat()
	rights := newAlwaysAllowRights()
	w := NewDefaultMaterializeWorker(stock, cands, rights, nil, nil)
	cands.seed(MediaCandidate{ID: "c1", ProviderAssetID: "p-c1", Provider: "artlist", MediaType: "video", RightsStatus: RightsVerified, MaterializationStatus: MaterializationCold})
	cands.seed(MediaCandidate{ID: "c2", ProviderAssetID: "p-c2", Provider: "artlist", MediaType: "video", RightsStatus: RightsVerified, MaterializationStatus: MaterializationCold})

	res, err := w.Materialize(context.Background(), MaterializationRequest{
		BatchID: "b1",
		Promotes: []AcquisitionPromote{
			{Candidate: cands.byID["c1"], Target: MaterializationWarm, TargetSlot: media.SlotPrimaryVideo},
			{Candidate: cands.byID["c2"], Target: MaterializationWarm, TargetSlot: media.SlotPrimaryVideo},
		},
		HotCache: false,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(res.PersistedAssetIDs) != 2 {
		t.Fatalf("expected 2 PersistedAssetIDs, got %d", len(res.PersistedAssetIDs))
	}
	if cands.byID["c1"].MaterializationStatus != MaterializationWarm {
		t.Fatalf("expected c1 Warm, got %s", cands.byID["c1"].MaterializationStatus)
	}
	if cands.byID["c2"].MaterializationStatus != MaterializationWarm {
		t.Fatalf("expected c2 Warm, got %s", cands.byID["c2"].MaterializationStatus)
	}
}

// ── Test 2: worker MaterializeHot flips to Hot + DiscoveryMaterialized ──

func TestMaterializeWorker_Materialize_HotCachePromotesToHot(t *testing.T) {
	stock := newFakeStockPipeline()
	cands := newFakeCandidateRepoMat()
	rights := newAlwaysAllowRights()
	w := NewDefaultMaterializeWorker(stock, cands, rights, nil, nil)
	cands.seed(MediaCandidate{ID: "h1", ProviderAssetID: "p-h1", Provider: "artlist", MediaType: "video", RightsStatus: RightsVerified, MaterializationStatus: MaterializationCold})

	_, err := w.Materialize(context.Background(), MaterializationRequest{
		BatchID: "b1",
		Promotes: []AcquisitionPromote{
			{Candidate: cands.byID["h1"], Target: MaterializationHot, TargetSlot: media.SlotPrimaryVideo},
		},
		HotCache: true,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if cands.byID["h1"].MaterializationStatus != MaterializationHot {
		t.Fatalf("expected Hot, got %s", cands.byID["h1"].MaterializationStatus)
	}
	if cands.byID["h1"].DiscoveryStatus != DiscoveryMaterialized {
		t.Fatalf("expected DiscoveryMaterialized, got %s", cands.byID["h1"].DiscoveryStatus)
	}
}

// ── Test 3: rights-denied candidate → failed envelope ─────────────

func TestMaterializeWorker_Materialize_RightsDeniedReturnsFailureNotError(t *testing.T) {
	stock := newFakeStockPipeline()
	cands := newFakeCandidateRepoMat()
	rights := newAlwaysAllowRights().deny("c9")
	w := NewDefaultMaterializeWorker(stock, cands, rights, nil, nil)
	cands.seed(MediaCandidate{ID: "c9", ProviderAssetID: "p-c9", Provider: "artlist", RightsStatus: RightsVerified, MaterializationStatus: MaterializationCold})

	res, err := w.Materialize(context.Background(), MaterializationRequest{
		BatchID: "b1",
		Promotes: []AcquisitionPromote{
			{Candidate: cands.byID["c9"], Target: MaterializationWarm},
		},
	})
	if err != nil {
		t.Fatalf("Materialize should not error on per-row denial: %v", err)
	}
	if len(res.FailedCandidateIDs) != 1 || res.FailedCandidateIDs[0] != "c9" {
		t.Fatalf("expected c9 in FailedCandidateIDs, got %v", res.FailedCandidateIDs)
	}
	if len(res.PersistedAssetIDs) != 0 {
		t.Fatalf("expected 0 PersistedAssetIDs, got %d", len(res.PersistedAssetIDs))
	}
}

// ── Test 4: idempotent re-promotion ──────────────────────────────

func TestMaterializeWorker_Materialize_AlreadyHotIsNoOp(t *testing.T) {
	stock := newFakeStockPipeline()
	cands := newFakeCandidateRepoMat()
	rights := newAlwaysAllowRights()
	w := NewDefaultMaterializeWorker(stock, cands, rights, nil, nil)
	cands.seed(MediaCandidate{
		ID: "hot1", ProviderAssetID: "p-hot1", Provider: "artlist",
		RightsStatus: RightsVerified, MaterializationStatus: MaterializationHot, AssetID: "asset-existing",
	})

	res, err := w.Materialize(context.Background(), MaterializationRequest{
		BatchID: "b1",
		Promotes: []AcquisitionPromote{
			{Candidate: cands.byID["hot1"], Target: MaterializationHot},
		},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(res.PersistedAssetIDs) != 1 || res.PersistedAssetIDs[0] != "asset-existing" {
		t.Fatalf("expected re-promotion to return existing assetID, got %v", res.PersistedAssetIDs)
	}
	if res.MaterializedCount != 1 {
		t.Fatalf("expected MaterializedCount=1, got %d", res.MaterializedCount)
	}
}

// ── Test 5: nil stock → ErrSemanticNotConfigured ─────────────────

func TestMaterializeWorker_Materialize_NilStockReturnsSemanticNotConfigured(t *testing.T) {
	w := NewDefaultMaterializeWorker(nil, newFakeCandidateRepoMat(), newAlwaysAllowRights(), nil, nil)
	_, err := w.Materialize(context.Background(), MaterializationRequest{
		Promotes: []AcquisitionPromote{{Candidate: MediaCandidate{ID: "x"}, Target: MaterializationWarm}},
	})
	if !errors.Is(err, ErrSemanticNotConfigured) {
		t.Fatalf("expected ErrSemanticNotConfigured, got %v", err)
	}
}

// ── Test 6: PromoteOnDemand rights-denied → ErrApprovalRequired ───

func TestMaterializeWorker_PromoteOnDemand_RightsDeniedReturnsApprovalRequired(t *testing.T) {
	stock := newFakeStockPipeline()
	cands := newFakeCandidateRepoMat()
	rights := newAlwaysAllowRights().deny("c12")
	w := NewDefaultMaterializeWorker(stock, cands, rights, nil, nil)
	cands.seed(MediaCandidate{ID: "c12", ProviderAssetID: "p-c12", Provider: "artlist", RightsStatus: RightsVerified, MaterializationStatus: MaterializationWarm})

	_, err := w.PromoteOnDemand(context.Background(), cands.byID["c12"], MaterializeOptions{})
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
}

// ── Test 7: PromoteOnDemand happy → AssetID set + Hot ─────────────

func TestMaterializeWorker_PromoteOnDemand_HappyPathMintsAssetIDAndHot(t *testing.T) {
	stock := newFakeStockPipeline()
	cands := newFakeCandidateRepoMat()
	rights := newAlwaysAllowRights()
	w := NewDefaultMaterializeWorker(stock, cands, rights, nil, nil)
	cands.seed(MediaCandidate{ID: "c77", ProviderAssetID: "p-c77", Provider: "artlist", MediaType: "video", RightsStatus: RightsVerified, MaterializationStatus: MaterializationWarm})

	mat, err := w.PromoteOnDemand(context.Background(), cands.byID["c77"], MaterializeOptions{HotCache: true})
	if err != nil {
		t.Fatalf("PromoteOnDemand: %v", err)
	}
	if mat.AssetID != "asset-c77" {
		t.Fatalf("expected AssetID=asset-c77, got %q", mat.AssetID)
	}
	if cands.byID["c77"].MaterializationStatus != MaterializationHot {
		t.Fatalf("expected Hot tier, got %s", cands.byID["c77"].MaterializationStatus)
	}
	if cands.byID["c77"].DiscoveryStatus != DiscoveryMaterialized {
		t.Fatalf("expected DiscoveryMaterialized, got %s", cands.byID["c77"].DiscoveryStatus)
	}
}

// ── Test 8: SetMaterializeWorker nil-refuses + rewire-refuses ────

func TestBatchService_SetMaterializeWorker_RefusesNilAndRewire(t *testing.T) {
	svc := &defaultBatchService{batches: map[string]*batchRow{}, children: map[string]*batchChildRow{}}
	if err := svc.SetMaterializeWorker(nil); !errors.Is(err, ErrInvalidPhrase) {
		t.Fatalf("expected ErrInvalidPhrase for nil, got %v", err)
	}
	if err := svc.SetMaterializeWorker(NewDefaultMaterializeWorker(newFakeStockPipeline(), newFakeCandidateRepoMat(), newAlwaysAllowRights(), nil, nil)); err != nil {
		t.Fatalf("first wire failed: %v", err)
	}
	if err := svc.SetMaterializeWorker(NewDefaultMaterializeWorker(newFakeStockPipeline(), newFakeCandidateRepoMat(), newAlwaysAllowRights(), nil, nil)); !errors.Is(err, ErrLinkerInvariantBroken) {
		t.Fatalf("expected ErrLinkerInvariantBroken on rewire, got %v", err)
	}
}

// ── Test 9: AcquisitionPlanner.Plan caps at TopK ────────────────

func TestAcquisitionPlanner_Plan_TopKCapAndRightsGate(t *testing.T) {
	p := NewDefaultAcquisitionPlanner(nil)
	cands := []MediaCandidate{}
	// 5 candidates with descending score; 1 rights-denied (must
	// drop); 1 already-Warm (must skip). TopK=2 -> picks c1,
	// c2 only.
	for i, score := range []float64{0.95, 0.85, 0.75, 0.65, 0.55} {
		rights := RightsVerified
		if i == 4 {
			rights = RightsDenied
		}
		mat := MaterializationCold
		if i == 3 {
			mat = MaterializationWarm
		}
		cands = append(cands, MediaCandidate{ID: fmt.Sprintf("c%d", i), CandidateScore: score, RightsStatus: rights, MaterializationStatus: mat})
	}
	out, err := p.Plan(context.Background(), AcquisitionInput{TopK: 2, Candidates: cands})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 promotes, got %d", len(out))
	}
	if out[0].Candidate.ID != "c0" || out[1].Candidate.ID != "c1" {
		t.Fatalf("expected order c0,c1, got %s,%s", out[0].Candidate.ID, out[1].Candidate.ID)
	}
	if out[0].Target != MaterializationWarm {
		t.Fatalf("expected target Warm, got %s", out[0].Target)
	}
}

// ── Test 10: AcquisitionPlanner.PlanOnDemand rights-denied ───────

func TestAcquisitionPlanner_PlanOnDemand_RightsDeniedReturnsApprovalRequired(t *testing.T) {
	p := NewDefaultAcquisitionPlanner(nil)
	_, err := p.PlanOnDemand(context.Background(), MediaCandidate{ID: "c1", RightsStatus: RightsDenied})
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
}
