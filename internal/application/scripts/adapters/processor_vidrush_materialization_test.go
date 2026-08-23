package adapters

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type materializationProviderStub struct{}

func (materializationProviderStub) Name() string { return scriptpkg.VidRushProviderArtlist }
func (materializationProviderStub) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (materializationProviderStub) Acquire(_ context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/clip.mp4", MIMEType: "video/mp4", SizeBytes: 10, LegacyFileMD5: "hash"}, nil
}
func (materializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, LegacyFileMD5: artifact.LegacyFileMD5, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
}

type failingArtlistMaterializationProvider struct{}

func (failingArtlistMaterializationProvider) Name() string { return scriptpkg.VidRushProviderArtlist }
func (failingArtlistMaterializationProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (failingArtlistMaterializationProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, errors.New("Artlist download unavailable")
}
func (failingArtlistMaterializationProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, errors.New("unreachable verifier")
}

type materializationFinalizerStub struct{}

func TestVidRushMaterializationMetadataOnlyPreservesCandidatesWithoutAcquisition(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{}
	plan.MediaPlan.Materialization.Mode = "metadata_only"
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "main",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "maya-image", Provider: scriptpkg.VidRushProviderInternetImages,
			Query: "Chichen Itza Maya pyramid Yucatan",
		}}},
	}}}
	result, err := NewVidRushMaterializationProcessor(nil, nil).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.VidRushSegments) != 1 || len(result.VidRushSegments[0].Assets.Candidates) != 1 {
		t.Fatalf("metadata-only candidates = %#v, want one preserved candidate", result.VidRushSegments)
	}
}

func (materializationFinalizerStub) Finalize(_ context.Context, artifact scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	candidate := artifact.Candidate
	candidate.LegacyFileMD5 = artifact.LegacyFileMD5
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
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/generated.png", MIMEType: "image/png", SizeBytes: 10, LegacyFileMD5: candidate.AssetID}, nil
}
func (s *generationMaterializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, LegacyFileMD5: artifact.LegacyFileMD5, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
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
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/image.jpg", MIMEType: "image/jpeg", SizeBytes: 10, LegacyFileMD5: "image-hash"}, nil
}
func (internetImageMaterializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, LegacyFileMD5: artifact.LegacyFileMD5, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
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

func TestVidRushMaterializationArtlistOnlyFailsWhenPrimaryCannotBePersisted(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(failingArtlistMaterializationProvider{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{}
	plan.MediaPlan.ProviderPolicy.Artlist = "enabled"
	_, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "artlist-only-required-primary",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID:      "artlist-required",
			Provider:     scriptpkg.VidRushProviderArtlist,
			SourceURL:    "https://cdn.example/required.mp4",
			RightsStatus: "unknown",
		}}},
	}}})
	if err == nil {
		t.Fatal("Artlist-only materialization must fail when no persisted primary is available")
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
	candidate.LegacyFileMD5 = artifact.LegacyFileMD5
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

func TestVidRushMaterializationMaterializeReusesSingleSegmentBoundary(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(materializationProviderStub{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})

	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "single-materialize",
		Text:      "a mountain at sunrise",
		TextHash:  "single-materialize-hash",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID:      "single-artlist",
			Provider:     scriptpkg.VidRushProviderArtlist,
			SourceURL:    "https://cdn.example/single.mp4",
			RightsStatus: "unknown",
		}}},
	}

	result, err := processor.Materialize(context.Background(), nil, segment)
	if err != nil {
		t.Fatal(err)
	}
	if result.Assets.PrimaryVideo == nil {
		t.Fatal("single-segment Materialize must persist and bind the primary video through the shared boundary")
	}
	if result.Assets.PrimaryVideo.DriveLink == "" {
		t.Fatalf("primary video DriveLink = %q, want a persisted link", result.Assets.PrimaryVideo.DriveLink)
	}
}

func TestVidRushMaterializationMaterializeFailsClosedWhenDependenciesMissing(t *testing.T) {
	processor := NewVidRushMaterializationProcessor(nil, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	_, err := processor.Materialize(context.Background(), plan, scriptpkg.VidRushSegmentResult{
		SegmentID: "missing-single-materialize",
	})
	if err == nil {
		t.Fatal("expected single-segment Materialize to fail closed when provider registry and finalizer are unavailable")
	}
}

// TestVidRushMaterializationEntityImageFullLifecycleChain certifies the full
// acquisition boundary from the certification spec: a search-discovered
// candidate (candidate_found, remote provenance only) must move through
// acquire → verify → persist → Drive, and only then bind as the entity image.
func TestVidRushMaterializationEntityImageFullLifecycleChain(t *testing.T) {
	vidrushMaterializedCache = sync.Map{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(internetImageMaterializationProviderStub{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessor(registry, materializationFinalizerStub{})

	plan := &scriptpkg.ResolvedGenerationPlan{
		Language: "en",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Extraction: mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{
				Enabled: true, EntityTypes: []string{"PERSON"},
			}},
		},
	}

	// The search step produced a discovered candidate: remote URL and query,
	// but no lifecycle state and no Drive link yet.
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
			ID: "scene-dwayne", SegmentID: "scene-dwayne", Index: 0,
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON",
			}}},
		}}},
		VidRushSegments: []scriptpkg.VidRushSegmentResult{{
			SegmentID: "scene-dwayne", SceneID: "scene-dwayne", Position: 0,
			Text:     "Dwayne Johnson trained in Los Angeles.",
			TextHash: "cert-dwayne-hash",
			Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
				AssetID:      "asset-dwayne-cert",
				Provider:     scriptpkg.VidRushProviderInternetImages,
				Query:        "Dwayne Johnson",
				SourceURL:    "https://images.example/dwayne.jpg",
				RightsStatus: "unknown",
			}}},
		}},
	}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}

	candidates := result.VidRushSegments[0].Assets.Candidates
	if len(candidates) != 1 {
		t.Fatalf("materialized candidates = %+v, want exactly one", candidates)
	}
	c := candidates[0]
	if c.Provider != scriptpkg.VidRushProviderInternetImages {
		t.Fatalf("provider = %q, want internet_images", c.Provider)
	}
	if c.SourceURL == "" {
		t.Fatal("source_url must be preserved through materialization")
	}
	if c.AcquisitionStatus != scriptpkg.VidRushStatusAcquired {
		t.Fatalf("acquisition_status = %q, want acquired", c.AcquisitionStatus)
	}
	if c.VerificationStatus != scriptpkg.VidRushStatusVerified {
		t.Fatalf("verification_status = %q, want verified", c.VerificationStatus)
	}
	if c.PersistenceStatus != scriptpkg.VidRushStatusPersisted {
		t.Fatalf("persistence_status = %q, want persisted", c.PersistenceStatus)
	}
	if c.DriveLink == "" {
		t.Fatal("drive_link must be populated by the finalizer")
	}

	// Only a fully materialized candidate reaches the entity-image binding.
	img := result.UpdatedSpecScene.Scenes[0].Annotations.PrimaryEntities[0].Image
	if img == nil || img.Status != "resolved" || img.AssetID != "asset-dwayne-cert" || img.DriveLink == "" {
		t.Fatalf("entity image binding = %+v, want resolved durable image", img)
	}
}

type catalogReuseImageProvider struct {
	acquireCalls atomic.Int32
}

func (p *catalogReuseImageProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (p *catalogReuseImageProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (p *catalogReuseImageProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	p.acquireCalls.Add(1)
	return scriptports.LocalArtifact{}, errors.New("catalog reuse must not download")
}
func (*catalogReuseImageProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, errors.New("catalog reuse must not verify a downloaded artifact")
}

type catalogReuseFinalizer struct {
	finalizeCalls atomic.Int32
}

func (f *catalogReuseFinalizer) Finalize(context.Context, scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	f.finalizeCalls.Add(1)
	return scriptpkg.SegmentAssetCandidate{}, errors.New("catalog reuse must not upload/finalize")
}

func TestVidRushMaterializationMarksCatalogURLBrokenAfterAcquireFailure(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/michael-jordan-broken.jpg")
	provider := &catalogReuseImageProvider{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessorWithCatalog(registry, &catalogReuseFinalizer{}, nil, repo)
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	_, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "catalog-broken",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "not-yet-materialized", Provider: scriptpkg.VidRushProviderInternetImages,
			Entity: "Michael Jordan", SourceURL: "https://images.example/michael-jordan-broken.jpg",
			RightsStatus: "unknown",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := entitycatalog.CanonicalizePersonName("Michael Jordan")
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("catalog rows = %d, err=%v", len(rows), err)
	}
	if rows[0].Status != entitycatalog.CandidateStatusBroken {
		t.Fatalf("URL status = %q, want broken after acquire failure", rows[0].Status)
	}
}

func TestVidRushMaterializationReusesCatalogedDriveImageWithoutAcquireOrFinalize(t *testing.T) {
	vidrushMaterializedCache = sync.Map{}
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/michael-jordan.jpg")
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("catalog rows = %d, err=%v", len(rows), err)
	}
	if err := repo.UpsertMaterialization(context.Background(), entitycatalog.Materialization{
		CandidateID:    rows[0].ID,
		AssetID:        "drive-asset-michael-jordan",
		LegacyFileMD5:  "sha256-michael-jordan",
		DriveLink:      "https://drive.google.com/file/d/drive-asset-michael-jordan/view",
		LocalPath:      "/nonexistent/local-copy-is-not-needed.jpg",
		Status:         entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: time.Now().UTC(), LastVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	provider := &catalogReuseImageProvider{}
	finalizer := &catalogReuseFinalizer{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessorWithCatalog(registry, finalizer, nil, repo)
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "catalog-reuse",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID:      "discovered-before-catalog-hydration",
			Provider:     scriptpkg.VidRushProviderInternetImages,
			Entity:       "Michael Jordan",
			Query:        "Michael Jordan",
			SourceURL:    "https://images.example/michael-jordan.jpg",
			RightsStatus: "unknown",
		}}},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if got := provider.acquireCalls.Load(); got != 0 {
		t.Fatalf("image acquire calls = %d, want 0 for a verified catalog materialization", got)
	}
	if got := finalizer.finalizeCalls.Load(); got != 0 {
		t.Fatalf("finalizer calls = %d, want 0 because Drive asset is already persisted", got)
	}
	images := result.VidRushSegments[0].Assets.SecondaryImages
	if len(images) != 1 || images[0].AssetID != "drive-asset-michael-jordan" || images[0].DriveLink == "" || images[0].LegacyFileMD5 == "" {
		t.Fatalf("reused images = %+v, want the cataloged Drive/hash candidate", images)
	}
}
