package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type materializationProviderStub struct{}

func (materializationProviderStub) Name() string { return scriptpkg.VidRushProviderArtlist }
func (materializationProviderStub) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (materializationProviderStub) Acquire(_ context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/clip.mp4", MIMEType: "video/mp4", SizeBytes: 10, FileHash: "hash"}, nil
}
func (materializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, FileHash: artifact.FileHash, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
}

type materializationFinalizerStub struct{}

func (materializationFinalizerStub) Finalize(_ context.Context, artifact scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	candidate := artifact.Candidate
	candidate.FileHash = artifact.FileHash
	candidate.Width = artifact.Width
	candidate.Height = artifact.Height
	candidate.DriveLink = "https://drive.google.com/file/d/" + candidate.AssetID
	candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	candidate.IndexStatus = scriptpkg.VidRushStatusIndexed
	return candidate, nil
}

type generationMaterializationProviderStub struct {
	calls int
	fail  bool
}

func (s *generationMaterializationProviderStub) Name() string {
	return scriptpkg.VidRushProviderImageGeneration
}
func (s *generationMaterializationProviderStub) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (s *generationMaterializationProviderStub) Acquire(_ context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	s.calls++
	if s.fail {
		return scriptports.LocalArtifact{}, errors.New("generation unavailable")
	}
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/generated.png", MIMEType: "image/png", SizeBytes: 10, FileHash: candidate.AssetID}, nil
}
func (s *generationMaterializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, FileHash: artifact.FileHash, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
}

type internetImageMaterializationProviderStub struct{}

func (internetImageMaterializationProviderStub) Name() string {
	return scriptpkg.VidRushProviderInternetImages
}
func (internetImageMaterializationProviderStub) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (internetImageMaterializationProviderStub) Acquire(_ context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/image.jpg", MIMEType: "image/jpeg", SizeBytes: 10, FileHash: "image-hash"}, nil
}
func (internetImageMaterializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, FileHash: artifact.FileHash, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
}

func TestVidRushMaterializationGeneratesOnlyMissingImagesAndWarmsCache(t *testing.T) {
	provider := &generationMaterializationProviderStub{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{PromptVersion: "test-v1", ImagesPerScene: 2}
	plan.MediaPlan.ProviderPolicy.ImageGeneration = "enabled"
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "generated-cache-segment", Text: "a mountain at sunrise", TextHash: "generated-cache-hash",
		Assets: scriptpkg.SegmentAssetSelection{},
	}}}
	first, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(first.VidRushSegments[0].Assets.SecondaryImages); got != 2 {
		t.Fatalf("generated image count = %d, want 2", got)
	}
	if provider.calls != 2 {
		t.Fatalf("generation calls = %d, want 2", provider.calls)
	}
	if _, err := processor.Process(context.Background(), plan, input); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 {
		t.Fatalf("warm generation calls = %d, want 2", provider.calls)
	}
}

func TestVidRushMaterializationUsesDefaultImageTargetForGeneration(t *testing.T) {
	provider := &generationMaterializationProviderStub{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{PromptVersion: "default-target-v1"}
	plan.MediaPlan.ProviderPolicy.ImageGeneration = "enabled"

	result, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "default-generation-target", Text: "a city at sunrise", TextHash: "default-generation-target-hash",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != vidRushDefaultImagesPerScene {
		t.Fatalf("generated image count = %d, want default %d", got, vidRushDefaultImagesPerScene)
	}
	if provider.calls != vidRushDefaultImagesPerScene {
		t.Fatalf("generation calls = %d, want default %d", provider.calls, vidRushDefaultImagesPerScene)
	}
}

func TestVidRushMaterializationReportsRequiredImageCountFailure(t *testing.T) {
	provider := &generationMaterializationProviderStub{fail: true}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{PromptVersion: "required-count-v1", ImagesPerScene: 2}
	plan.MediaPlan.ProviderPolicy.ImageGeneration = "enabled"
	result, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "required-count-segment", Text: "fallback image", TextHash: "required-count-hash",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.VidRushSegments[0].Assets.SecondaryImages) != 0 {
		t.Fatalf("secondary images = %#v, want none after failed generation", result.VidRushSegments[0].Assets.SecondaryImages)
	}
	if !containsWarning(result.Warnings, "FAILED_REQUIRED_IMAGE_COUNT: required=2 verified=0 segment=required-count-segment") {
		t.Fatalf("warnings = %#v, want FAILED_REQUIRED_IMAGE_COUNT", result.Warnings)
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}

func TestVidRushMaterializationFailsClosedWhenEnabledDependenciesAreMissing(t *testing.T) {
	processor := NewVidRushMaterializationProcessor(nil, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	_, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "missing-image-finalizer",
	}}})
	if err == nil {
		t.Fatal("expected missing VidRush dependencies to fail closed")
	}
}

func TestVidRushMaterializationFailsClosedWhenEnabledProviderIsNotRegistered(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{}
	plan.MediaPlan.ProviderPolicy.Artlist = "enabled"
	_, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "missing-artlist-provider",
	}}})
	if err == nil {
		t.Fatal("expected an enabled but unregistered provider to fail closed")
	}
}

func TestVidRushMaterializationVerifiesWebImagesBeforeGenerationFallback(t *testing.T) {
	imageProvider := internetImageMaterializationProviderStub{}
	generationProvider := &generationMaterializationProviderStub{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(imageProvider); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(generationProvider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{PromptVersion: "fallback-order-v1", ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.ImageGeneration = "enabled"
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "web-before-generation", Text: "a verified web image", TextHash: "web-before-generation-hash",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "web-image-1", Provider: scriptpkg.VidRushProviderInternetImages,
			SourceURL: "https://images.example/image.jpg", RightsStatus: "verified",
		}}},
	}}}
	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if generationProvider.calls != 0 {
		t.Fatalf("generation calls = %d, want 0 after web image verification", generationProvider.calls)
	}
	images := result.VidRushSegments[0].Assets.SecondaryImages
	if len(images) != 1 || images[0].AssetID != "web-image-1" {
		t.Fatalf("secondary images = %#v, want the verified web image", images)
	}
}

func TestVidRushMaterializationRequiresPersistedCandidateForPrimary(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(materializationProviderStub{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})
	result, err := processor.Process(context.Background(), nil, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "segment-1",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "artlist-1", Provider: scriptpkg.VidRushProviderArtlist, SourceURL: "https://cdn.example/clip", RightsStatus: "unknown",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result.VidRushSegments) != 1 {
		t.Fatalf("expected one materialized segment, got %#v", result)
	}
	primary := result.VidRushSegments[0].Assets.PrimaryVideo
	if primary == nil {
		t.Fatal("expected persisted Artlist candidate to become primary")
	}
	if primary.DriveLink == "" || primary.PersistenceStatus != scriptpkg.VidRushStatusPersisted || primary.IndexStatus != scriptpkg.VidRushStatusIndexed {
		t.Fatalf("primary is not durable: %#v", *primary)
	}
}

func TestVidRushMaterializationDoesNotBindUnknownRightsImage(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(materializationProviderStub{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})
	result, err := processor.Process(context.Background(), nil, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "segment-1",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "artlist-1", Provider: scriptpkg.VidRushProviderArtlist, SourceURL: "https://cdn.example/clip", RightsStatus: "unknown",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	// The provider explicitly verifies rights in this stub, so the test also
	// pins that an unknown discovery status cannot leak into final binding.
	if result.VidRushSegments[0].Assets.PrimaryVideo == nil || result.VidRushSegments[0].Assets.PrimaryVideo.RightsStatus != "verified" {
		t.Fatalf("rights policy was not applied: %#v", result.VidRushSegments[0].Assets.PrimaryVideo)
	}
}

type retryingMaterializationFinalizer struct {
	calls int
}

func (f *retryingMaterializationFinalizer) Finalize(_ context.Context, artifact scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	f.calls++
	if f.calls == 1 {
		return scriptpkg.SegmentAssetCandidate{}, errors.New("transient finalizer failure")
	}
	candidate := artifact.Candidate
	candidate.FileHash = artifact.FileHash
	candidate.Width = artifact.Width
	candidate.Height = artifact.Height
	candidate.DriveLink = "https://drive.google.com/file/d/" + candidate.AssetID
	candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	candidate.IndexStatus = scriptpkg.VidRushStatusIndexed
	return candidate, nil
}

func TestVidRushMaterializationRetriesAfterFinalizerFailure(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(materializationProviderStub{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	finalizer := &retryingMaterializationFinalizer{}
	processor := NewVidRushMaterializationProcessor(registry, finalizer)
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "recovery-segment",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "recovery-asset", Provider: scriptpkg.VidRushProviderArtlist,
			SourceURL: "https://cdn.example/recovery.mp4", RightsStatus: "unknown",
		}}},
	}}}

	first, err := processor.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.VidRushSegments[0].Assets.PrimaryVideo != nil {
		t.Fatal("failed finalization must not bind a primary video")
	}
	second, err := processor.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if finalizer.calls != 2 {
		t.Fatalf("finalizer calls = %d, want 2", finalizer.calls)
	}
	if second.VidRushSegments[0].Assets.PrimaryVideo == nil {
		t.Fatal("successful recovery should bind the persisted primary video")
	}
}

func TestVidRushProviderTimeoutsBoundRemoteOperations(t *testing.T) {
	tests := []struct {
		provider string
		want     time.Duration
	}{
		{provider: scriptpkg.VidRushProviderArtlist, want: vidRushArtlistAcquireTimeout},
		{provider: scriptpkg.VidRushProviderInternetImages, want: vidRushImageAcquireTimeout},
		{provider: scriptpkg.VidRushProviderImageGeneration, want: vidRushGenerationAcquireTimeout},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			before := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), vidRushProviderTimeout(test.provider))
			defer cancel()
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("provider context has no deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > test.want || time.Since(before) > 100*time.Millisecond {
				t.Fatalf("provider timeout remaining = %s, want a positive duration no greater than %s", remaining, test.want)
			}
		})
	}
}
