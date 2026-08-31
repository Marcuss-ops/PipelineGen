package adapters

// This file is intentionally hermetic. It certifies the contracts that can be
// proven without external credentials; the LIVE test is opt-in and lives in a
// separate file so a local `go test ./...` can never turn a fake into a green
// production certification.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type certificationArtlistProvider struct {
	mu          sync.Mutex
	searches    []scriptports.VidRushSearchRequest
	acquires    []string
	failAcquire map[string]bool
	failVerify  map[string]bool
}

func (p *certificationArtlistProvider) Name() string { return scriptpkg.VidRushProviderArtlist }
func (p *certificationArtlistProvider) Search(_ context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.mu.Lock()
	p.searches = append(p.searches, req)
	p.mu.Unlock()
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "art-" + strings.ReplaceAll(req.SceneID, " ", "-"), Provider: scriptpkg.VidRushProviderArtlist,
		Query: req.Query, SourceURL: "https://cdn.artlist.example/" + req.SceneID + ".m3u8",
		SourcePageURL: "https://artlist.example/clip/" + req.SceneID, RelevanceScore: .9,
		TechnicalQualityScore: .9, ProviderReliability: .9,
	}}, nil
}
func (p *certificationArtlistProvider) Acquire(_ context.Context, c scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	p.mu.Lock()
	p.acquires = append(p.acquires, c.AssetID)
	fail := p.failAcquire[c.AssetID]
	p.mu.Unlock()
	if fail {
		return scriptports.LocalArtifact{}, fmt.Errorf("download failed for %s", c.AssetID)
	}
	c.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: c, LocalPath: "/materialized/" + c.AssetID + ".mp4", MIMEType: "video/mp4", SizeBytes: 42, LegacyFileMD5: "sha-" + c.AssetID}, nil
}
func (p *certificationArtlistProvider) Verify(_ context.Context, a scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	p.mu.Lock()
	fail := p.failVerify[a.Candidate.AssetID]
	p.mu.Unlock()
	if fail {
		return scriptports.VerifiedArtifact{}, fmt.Errorf("verification failed for %s", a.Candidate.AssetID)
	}
	c := a.Candidate
	c.VerificationStatus = scriptpkg.VidRushStatusVerified
	c.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: c, LocalPath: a.LocalPath, MIMEType: a.MIMEType, SizeBytes: a.SizeBytes, LegacyFileMD5: a.LegacyFileMD5, DurationMs: 10000, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
}

type certificationFinalizer struct {
	mu      sync.Mutex
	calls   int
	byAsset map[string]scriptpkg.SegmentAssetCandidate
}

func (f *certificationFinalizer) Finalize(_ context.Context, a scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byAsset == nil {
		f.byAsset = map[string]scriptpkg.SegmentAssetCandidate{}
	}
	if existing, ok := f.byAsset[a.Candidate.AssetID]; ok {
		return existing, nil
	}
	f.calls++
	c := a.Candidate
	c.AssetID = strings.TrimSpace(c.AssetID)
	c.LegacyFileMD5 = a.LegacyFileMD5
	c.LocalPath, c.MIMEType = a.LocalPath, a.MIMEType
	// SegmentAssetCandidate currently exposes the canonical Drive identity as
	// DriveLink; DriveFileID is carried by the persistence-layer record.
	c.DriveLink = "https://drive.google.com/file/d/drive-" + c.AssetID
	c.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	c.IndexStatus = "queued"
	f.byAsset[c.AssetID] = c
	return c, nil
}

func artlistOnlyCertificationPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{Language: "en", PromptVersion: "cert-v1", MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{Artlist: mediadomain.MediaToggleEnabled},
	}}
}

func newCertificationProcessor(t *testing.T, provider *certificationArtlistProvider, finalizer *certificationFinalizer) *VidRushMaterializationProcessor {
	t.Helper()
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	return NewVidRushMaterializationProcessor(registry, finalizer)
}

func certificationSegment(id, text string, candidates ...scriptpkg.SegmentAssetCandidate) scriptpkg.VidRushSegmentResult {
	hash := "hash-" + id
	return scriptpkg.VidRushSegmentResult{SegmentID: id, SceneID: id, Text: text, TextHash: hash, Insights: scriptpkg.SegmentInsights{
		SegmentID: id, TextHash: hash, ArtlistQueries: []string{text}, NounChunks: strings.Fields(text),
	}, Assets: scriptpkg.SegmentAssetSelection{Candidates: candidates}}
}

func TestVidRushCertification_ArtlistOnlyIdentityAndDurableBinding(t *testing.T) {
	provider := &certificationArtlistProvider{}
	finalizer := &certificationFinalizer{}
	processor := newCertificationProcessor(t, provider, finalizer)
	plan := artlistOnlyCertificationPlan()
	texts := []string{"coastal road", "barista latte art", "trail runner sunrise"}
	for i, text := range texts {
		id := fmt.Sprintf("scene-%d", i+1)
		candidate := scriptpkg.SegmentAssetCandidate{AssetID: id + "-asset", Provider: scriptpkg.VidRushProviderArtlist, Query: text, SourceURL: "https://cdn.artlist.example/" + id + ".m3u8", SourcePageURL: "https://artlist.example/" + id, RelevanceScore: .9, TechnicalQualityScore: .9, ProviderReliability: .9}
		got, err := processor.Materialize(context.Background(), plan, certificationSegment(id, text, candidate))
		if err != nil {
			t.Fatalf("scene %s: %v", id, err)
		}
		if got.SegmentID != id || got.SceneID != id || got.TextHash != "hash-"+id || len(got.Assets.Candidates) != 1 {
			t.Fatalf("identity changed: %#v", got)
		}
		selected := got.Assets.Candidates[0]
		if selected.AssetID != id+"-asset" || selected.DriveLink == "" || !readyVidRushCandidate(selected) {
			t.Fatalf("non-durable binding: %#v", selected)
		}
		if !strings.Contains(selected.Query, text) {
			t.Fatalf("query crossed scene boundary: %#v", selected)
		}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.searches) != 0 {
		t.Fatalf("materialization must not bypass discovery through provider Search: %d calls", len(provider.searches))
	}
	if len(provider.acquires) != 3 {
		t.Fatalf("Artlist acquire calls=%d, want 3", len(provider.acquires))
	}
}

func TestVidRushCertification_SemanticProfileArtlistQueriesAreVisual(t *testing.T) {
	seg := scriptpkg.CanonicalSegment{ID: "coffee", Text: "A barista pours steamed milk into an espresso and creates latte art.", TextHash: "coffee-hash"}
	profile := scriptpkg.BuildSegmentSemanticProfile(seg, scriptpkg.EntityResult{NounChunks: []string{"barista", "steamed milk", "espresso", "latte art", "coffee shop"}}, "model", "prompt")
	queries := scriptpkg.BuildArtlistQueries(profile, 5)
	joined := strings.ToLower(strings.Join(queries, " | "))
	for _, want := range []string{"barista", "espresso", "latte art"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("queries=%q missing %q", queries, want)
		}
	}
	for _, forbidden := range []string{"creates a", "he begins", "remains closely", "reveals how", "carved out"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("non-visual phrase %q leaked into queries=%q", forbidden, queries)
		}
	}
}

func TestVidRushCertification_DeterministicRankingAndScoreBreakdown(t *testing.T) {
	profile := scriptpkg.SegmentSemanticProfile{SegmentID: "barista", Topic: "barista latte art", VisualTerms: []scriptpkg.WeightedKeyword{{Value: "latte art", Confidence: 1}}}
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "asset-b", Provider: scriptpkg.VidRushProviderArtlist, Query: "barista latte art", SemanticScore: .8, RelevanceScore: .9, TechnicalQualityScore: .9, ProviderReliability: .9, DurationMs: 10000},
		{AssetID: "asset-a", Provider: scriptpkg.VidRushProviderArtlist, Query: "coffee shop", SemanticScore: .8, RelevanceScore: .9, TechnicalQualityScore: .9, ProviderReliability: .9, DurationMs: 10000},
	}
	ranker := NewVidRushWindowRanker()
	expected := ""
	for i := 0; i < 100; i++ {
		got := ranker.Rank(candidates, profile, 10000)
		if len(got) != 2 {
			t.Fatal("ranking dropped candidates")
		}
		if i == 0 {
			expected = got[0].AssetID
		}
		if got[0].AssetID != expected || got[0].Score != FinalScore(FinalScoreComponents{Semantic: got[0].SemanticScore, Transcript: got[0].RelevanceScore, Visual: profileSemanticMatch(got[0], profile), Technical: got[0].TechnicalQualityScore, DurationFit: durationFitScore(got[0].DurationMs, 10000), ProviderTrust: providerTrustScore(got[0])}) {
			t.Fatalf("non-deterministic/incomplete score at run %d: %#v", i, got)
		}
	}
}

func TestVidRushCertification_GhostAssetAndFallbackAreFailClosed(t *testing.T) {
	provider := &certificationArtlistProvider{failAcquire: map[string]bool{"bad-download": true}, failVerify: map[string]bool{"bad-verify": true}}
	finalizer := &certificationFinalizer{}
	processor := newCertificationProcessor(t, provider, finalizer)
	plan := artlistOnlyCertificationPlan()
	makeCandidate := func(id string) scriptpkg.SegmentAssetCandidate {
		return scriptpkg.SegmentAssetCandidate{AssetID: id, Provider: scriptpkg.VidRushProviderArtlist, Query: "warehouse robots", SourceURL: "https://cdn.artlist.example/" + id + ".m3u8", SourcePageURL: "https://artlist.example/" + id, RelevanceScore: .9, TechnicalQualityScore: .9, ProviderReliability: .9}
	}
	got, err := processor.Materialize(context.Background(), plan, certificationSegment("warehouse", "warehouse robots", makeCandidate("bad-download"), makeCandidate("bad-verify"), makeCandidate("good")))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got.Assets.Candidates {
		if (c.AssetID == "bad-download" || c.AssetID == "bad-verify") && readyVidRushCandidate(c) {
			t.Fatalf("ghost/failing candidate became bindable: %#v", c)
		}
	}
	if got.Assets.PrimaryVideo == nil || got.Assets.PrimaryVideo.AssetID != "good" {
		t.Fatalf("winner=%#v, want good candidate", got.Assets.PrimaryVideo)
	}
	if finalizer.calls != 1 {
		t.Fatalf("finalizer calls=%d, want only successful candidate", finalizer.calls)
	}
}
