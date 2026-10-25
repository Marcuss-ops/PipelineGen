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

type certificationProvider struct {
	mu          sync.Mutex
	searches    []scriptports.VidRushSearchRequest
	acquires    []string
	failAcquire map[string]bool
	failVerify  map[string]bool
}

func (p *certificationProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (p *certificationProvider) Search(_ context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.mu.Lock()
	p.searches = append(p.searches, req)
	p.mu.Unlock()
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "cert-" + strings.ReplaceAll(req.SceneID, " ", "-"), Provider: scriptpkg.VidRushProviderInternetImages,
		Query: req.Query, SourceURL: "https://cdn.example/" + req.SceneID + ".m3u8",
		SourcePageURL: "https://example/clip/" + req.SceneID, RelevanceScore: .9,
		TechnicalQualityScore: .9, ProviderReliability: .9,
	}}, nil
}
func (p *certificationProvider) Acquire(_ context.Context, c scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	p.mu.Lock()
	p.acquires = append(p.acquires, c.AssetID)
	fail := p.failAcquire[c.AssetID]
	p.mu.Unlock()
	if fail {
		return scriptports.LocalArtifact{}, fmt.Errorf("download failed for %s", c.AssetID)
	}
	c.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: c, LocalPath: "/materialized/" + c.AssetID + ".mp4", MIMEType: "video/mp4", SizeBytes: 42, ContentSHA256: "sha-" + c.AssetID}, nil
}
func (p *certificationProvider) Verify(_ context.Context, a scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	p.mu.Lock()
	fail := p.failVerify[a.Candidate.AssetID]
	p.mu.Unlock()
	if fail {
		return scriptports.VerifiedArtifact{}, fmt.Errorf("verification failed for %s", a.Candidate.AssetID)
	}
	c := a.Candidate
	c.VerificationStatus = scriptpkg.VidRushStatusVerified
	c.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: c, LocalPath: a.LocalPath, MIMEType: a.MIMEType, SizeBytes: a.SizeBytes, ContentSHA256: a.ContentSHA256, DurationMs: 10000, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
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
	c.ContentSHA256 = a.ContentSHA256
	c.LocalPath, c.MIMEType = a.LocalPath, a.MIMEType
	// SegmentAssetCandidate currently exposes the canonical Drive identity as
	// DriveLink; DriveFileID is carried by the persistence-layer record.
	c.DriveLink = "https://drive.google.com/file/d/drive-" + c.AssetID
	c.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	c.IndexStatus = "queued"
	f.byAsset[c.AssetID] = c
	return c, nil
}

func stockOnlyCertificationPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{Language: "en", PromptVersion: "cert-v1", MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
	}}
}

func newCertificationProcessor(t *testing.T, provider *certificationProvider, finalizer *certificationFinalizer) *VidRushMaterializationProcessor {
	t.Helper()
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	return newTestMaterializationProcessor(registry, finalizer)
}

func certificationSegment(id, text string, candidates ...scriptpkg.SegmentAssetCandidate) scriptpkg.VidRushSegmentResult {
	hash := "hash-" + id
	return scriptpkg.VidRushSegmentResult{SegmentID: id, SceneID: id, Text: text, TextHash: hash, Insights: scriptpkg.SegmentInsights{
		SegmentID: id, TextHash: hash, NounChunks: strings.Fields(text),
	}, Assets: scriptpkg.SegmentAssetSelection{Candidates: candidates}}
}

func TestVidRushCertification_ProviderIdentityAndDurableBinding(t *testing.T) {
	provider := &certificationProvider{}
	finalizer := &certificationFinalizer{}
	processor := newCertificationProcessor(t, provider, finalizer)
	plan := stockOnlyCertificationPlan()
	texts := []string{"coastal road", "barista latte art", "trail runner sunrise"}
	for i, text := range texts {
		id := fmt.Sprintf("scene-%d", i+1)
		candidate := scriptpkg.SegmentAssetCandidate{AssetID: id + "-asset", Provider: scriptpkg.VidRushProviderInternetImages, Query: text, SourceURL: "https://cdn.example/" + id + ".m3u8", SourcePageURL: "https://example/" + id, RelevanceScore: .9, TechnicalQualityScore: .9, ProviderReliability: .9}
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
		t.Fatalf("provider acquire calls=%d, want 3", len(provider.acquires))
	}
}

func TestVidRushCertification_GhostAssetAndFallbackAreFailClosed(t *testing.T) {
	provider := &certificationProvider{failAcquire: map[string]bool{"bad-download": true}, failVerify: map[string]bool{"bad-verify": true}}
	finalizer := &certificationFinalizer{}
	processor := newCertificationProcessor(t, provider, finalizer)
	plan := stockOnlyCertificationPlan()
	makeCandidate := func(id string) scriptpkg.SegmentAssetCandidate {
		return scriptpkg.SegmentAssetCandidate{AssetID: id, Provider: scriptpkg.VidRushProviderInternetImages, Query: "warehouse robots", SourceURL: "https://cdn.example/" + id + ".m3u8", SourcePageURL: "https://example/" + id, RelevanceScore: .9, TechnicalQualityScore: .9, ProviderReliability: .9}
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
	images := got.Assets.SecondaryImages
	if len(images) != 1 || images[0].AssetID != "good" {
		t.Fatalf("persisted images=%#v, want the good candidate", images)
	}
	if finalizer.calls != 1 {
		t.Fatalf("finalizer calls=%d, want only successful candidate", finalizer.calls)
	}
}
