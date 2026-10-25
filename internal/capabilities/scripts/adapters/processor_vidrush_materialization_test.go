package adapters

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

// TestRouteEntityImageToCanonicalLibrary pins the entity-image half of the
// dispatch: an entity image must NOT be projected into the run bundle
// (<Title>/<Language>/images), because the same image would then land once per
// run instead of once in the canonical image library. A run-scoped destination
// a caller may have set is actively CLEARED, so the run-bundle path cannot come
// back through a legacy field.
func TestRouteEntityImageToCanonicalLibrary(t *testing.T) {
	stale := scriptports.VerifiedArtifact{
		Candidate: scriptpkg.SegmentAssetCandidate{
			AssetID: "entity-image-michael-jordan", Provider: scriptpkg.VidRushProviderInternetImages,
			Entity: "Michael Jordan",
		},
		OutputDriveFolderID: "drive-root",
		OutputDriveSubpath:  []string{"Michael Jordan_ A Story", "en", "images"},
	}
	got := routeEntityImageToCanonicalLibrary(stale)
	if got.OutputDriveFolderID != "" {
		t.Fatalf("entity image kept a run-scoped Drive root %q: the library destination is resolved by the finalizer, never by the run", got.OutputDriveFolderID)
	}
	if len(got.OutputDriveSubpath) != 0 {
		t.Fatalf("entity image kept a run-scoped Drive subpath %#v: <Title>/<Language>/images is retired", got.OutputDriveSubpath)
	}
}

func TestRouteEntityImageToCanonicalLibraryIgnoresNonEntityMedia(t *testing.T) {
	artifact := scriptports.VerifiedArtifact{Candidate: scriptpkg.SegmentAssetCandidate{
		AssetID: "scene-image", Provider: scriptpkg.VidRushProviderInternetImages,
	}}
	got := routeEntityImageToCanonicalLibrary(artifact)
	if got.OutputDriveFolderID != "" || len(got.OutputDriveSubpath) != 0 {
		t.Fatalf("non-entity image was routed to output: folder=%q path=%#v", got.OutputDriveFolderID, got.OutputDriveSubpath)
	}
}

// TestEntityImageDriveLeafIsTheCanonicalIdentity is the single-owner pin for
// the per-image folder: the leaf comes from the canonical entity identity, so
// casing/whitespace variants of the same person converge on ONE Drive folder and
// no caller can mint a per-run one.
func TestEntityImageDriveLeafIsTheCanonicalIdentity(t *testing.T) {
	for _, variant := range []string{"Michael Jordan", "MICHAEL JORDAN", "  Michael   Jordan  ", "Michael Jordan's"} {
		candidate := scriptpkg.SegmentAssetCandidate{
			AssetID: "entity-image-1", Provider: scriptpkg.VidRushProviderInternetImages, Entity: variant,
		}
		if got := EntityImageDriveLeaf(candidate); got != "michael-jordan" {
			t.Fatalf("EntityImageDriveLeaf(%q) = %q, want michael-jordan", variant, got)
		}
	}
	// Distinct people must not collide onto one folder.
	if got := EntityImageDriveLeaf(scriptpkg.SegmentAssetCandidate{
		AssetID: "entity-image-2", Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Michael B. Jordan",
	}); got == "michael-jordan" {
		t.Fatalf("distinct people collided on folder %q", got)
	}
	// A non-entity image, an entity-less candidate and a nameless candidate all
	// decline to name a library folder (the caller falls back to the generic
	// per-image destination).
	for _, candidate := range []scriptpkg.SegmentAssetCandidate{
		{AssetID: "scene-image", Provider: scriptpkg.VidRushProviderInternetImages},
		{AssetID: "entity-image-3", Provider: scriptpkg.VidRushProviderInternetImages},
		{AssetID: "yt_clip", Provider: scriptpkg.VidRushProviderYouTube, Entity: "Michael Jordan"},
		{AssetID: "entity-image-4", Provider: scriptpkg.VidRushProviderImageGeneration, Entity: "   "},
	} {
		if got := EntityImageDriveLeaf(candidate); got != "" {
			t.Fatalf("EntityImageDriveLeaf(%#v) = %q, want empty", candidate, got)
		}
	}
}

// TestRouteClipToGenerationOutput pins the clip family of the run bundle: the
// provider clips a source.type=clips job actually consumed must be published
// into <DriveFolderID>/<Title>/<Language>/clips, mirroring entity images.
func TestRouteClipToGenerationOutput(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:         "Clip Lane Certification",
		Language:      "en",
		DriveFolderID: "clip-dir-root",
	}
	artifact := scriptports.VerifiedArtifact{Candidate: scriptpkg.SegmentAssetCandidate{
		AssetID: "yt_iHaK0M-207o_60_70_v1", Provider: scriptpkg.VidRushProviderYouTube,
	}}
	got := routeClipToGenerationOutput(plan, artifact)
	if got.OutputDriveFolderID != "clip-dir-root" {
		t.Fatalf("output drive folder = %q, want clip-dir-root", got.OutputDriveFolderID)
	}
	wantPath := []string{"Clip Lane Certification", "en", "clips"}
	if !reflect.DeepEqual(got.OutputDriveSubpath, wantPath) {
		t.Fatalf("output drive subpath = %#v, want %#v", got.OutputDriveSubpath, wantPath)
	}
}

func TestRouteClipToGenerationOutputIgnoresImages(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "Run", Language: "en", DriveFolderID: "drive-root"}
	artifact := scriptports.VerifiedArtifact{Candidate: scriptpkg.SegmentAssetCandidate{
		AssetID: "scene-image", Provider: scriptpkg.VidRushProviderInternetImages,
	}}
	got := routeClipToGenerationOutput(plan, artifact)
	if got.OutputDriveFolderID != "" || len(got.OutputDriveSubpath) != 0 {
		t.Fatalf("image was routed into the clips family: folder=%q path=%#v", got.OutputDriveFolderID, got.OutputDriveSubpath)
	}
}

func TestRouteClipToGenerationOutputFailsClosedWithoutDriveFolder(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "Run", Language: "en"}
	artifact := scriptports.VerifiedArtifact{Candidate: scriptpkg.SegmentAssetCandidate{
		AssetID: "yt_clip", Provider: scriptpkg.VidRushProviderYouTube,
	}}
	got := routeClipToGenerationOutput(plan, artifact)
	if got.OutputDriveFolderID != "" || len(got.OutputDriveSubpath) != 0 {
		t.Fatalf("a plan without an artifact root must not invent a destination: folder=%q path=%#v", got.OutputDriveFolderID, got.OutputDriveSubpath)
	}
}

// TestRouteGenerationOutputToPlanBundleDispatchesByFamily is the non-vacuity
// gate for the single dispatch point: each family reaches its own child folder
// and an unrelated candidate is left untouched.
func TestRouteGenerationOutputToPlanBundleDispatchesByFamily(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "Run", Language: "it", DriveFolderID: "clip-dir-root"}
	cases := []struct {
		name      string
		candidate scriptpkg.SegmentAssetCandidate
		wantPath  []string
	}{
		{
			name:      "entity image (library, not the run bundle)",
			candidate: scriptpkg.SegmentAssetCandidate{AssetID: "entity-image-mike", Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Mike Tyson"},
		},
		{
			name:      "generated entity image (library, not the run bundle)",
			candidate: scriptpkg.SegmentAssetCandidate{AssetID: "gen-abc", Provider: scriptpkg.VidRushProviderImageGeneration, Entity: "Mike Tyson"},
		},
		{
			name:      "youtube clip",
			candidate: scriptpkg.SegmentAssetCandidate{AssetID: "yt_clip", Provider: scriptpkg.VidRushProviderYouTube},
			wantPath:  []string{"Run", "it", "clips"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := routeGenerationOutputToPlanBundle(plan, scriptports.VerifiedArtifact{Candidate: tc.candidate})
			// A family with no wantPath is a library family: it must leave the run
			// bundle empty so the finalizer can place it under the images root.
			if len(tc.wantPath) == 0 {
				if got.OutputDriveFolderID != "" || len(got.OutputDriveSubpath) != 0 {
					t.Fatalf("library family was routed into the run bundle: folder=%q path=%#v", got.OutputDriveFolderID, got.OutputDriveSubpath)
				}
				return
			}
			if got.OutputDriveFolderID != "clip-dir-root" {
				t.Fatalf("output drive folder = %q, want clip-dir-root", got.OutputDriveFolderID)
			}
			if !reflect.DeepEqual(got.OutputDriveSubpath, tc.wantPath) {
				t.Fatalf("output drive subpath = %#v, want %#v", got.OutputDriveSubpath, tc.wantPath)
			}
		})
	}

	unrelated := scriptports.VerifiedArtifact{Candidate: scriptpkg.SegmentAssetCandidate{AssetID: "plain", Provider: "stock"}}
	if got := routeGenerationOutputToPlanBundle(plan, unrelated); got.OutputDriveFolderID != "" || len(got.OutputDriveSubpath) != 0 {
		t.Fatalf("unrelated candidate was routed: folder=%q path=%#v", got.OutputDriveFolderID, got.OutputDriveSubpath)
	}
}

type materializationProviderStub struct{}

type mediaSamplerStub struct {
	winner string
	calls  int
}

type mediaSamplerGroupStub struct {
	bound []scriptports.AssetBinding
}

func (s *mediaSamplerGroupStub) SampleGroup(_ context.Context, _ string, _ string, _ []string, candidates []scriptpkg.SegmentAssetCandidate, _ bool, groupSize int, bound []scriptports.AssetBinding) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.bound = append([]scriptports.AssetBinding(nil), bound...)
	used := make(map[string]struct{}, len(bound))
	for _, entry := range bound {
		used[entry.AssetID] = struct{}{}
	}
	selected := make([]scriptpkg.SegmentAssetCandidate, 0, groupSize)
	for _, candidate := range candidates {
		if _, alreadyUsed := used[candidate.AssetID]; alreadyUsed {
			continue
		}
		selected = append(selected, candidate)
		if len(selected) == groupSize {
			break
		}
	}
	return selected, nil
}

func TestGroupImageSelectionPassesRunLedgerAndSkipsBoundAssets(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-a",
		Assets:    scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{{AssetID: "already-used"}}},
	}
	bound := vidRushBoundAssets([]scriptpkg.VidRushSegmentResult{segment})
	group := &mediaSamplerGroupStub{}
	processor := (&VidRushMaterializationProcessor{}).WithMediaSamplerGroup(group)
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "already-used", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://example.test/a.jpg", DriveLink: "/a.jpg"},
		{AssetID: "fresh-1", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://example.test/b.jpg", DriveLink: "/b.jpg"},
		{AssetID: "fresh-2", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://example.test/c.jpg", DriveLink: "/c.jpg"},
	}
	selected, warning := processor.selectGroupSecondaryImages(context.Background(), scriptpkg.SegmentSemanticProfile{}, "scene-b", candidates, 2, bound)
	if warning != "" {
		t.Fatalf("unexpected selection warning: %s", warning)
	}
	if len(group.bound) != 1 || group.bound[0] != (scriptports.AssetBinding{AssetID: "already-used", SceneID: "scene-a"}) {
		t.Fatalf("sampler ledger = %#v", group.bound)
	}
	if len(selected) != 2 || selected[0].AssetID != "fresh-1" || selected[1].AssetID != "fresh-2" {
		t.Fatalf("selected group = %#v, want fresh-1 and fresh-2", selected)
	}
}

func TestVidRushBoundLedgerRegistersChosenGroupsInOrder(t *testing.T) {
	initial := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "scene-a",
		Assets:    scriptpkg.SegmentAssetSelection{PrimaryVideo: &scriptpkg.SegmentAssetCandidate{AssetID: "clip-a"}},
	}}
	ledger := vidRushBoundAssets(initial)
	ledger = appendVidRushBoundAssets(ledger, scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-a",
		Assets:    scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{{AssetID: "image-a"}, {AssetID: "image-b"}}},
	})
	ledger = appendVidRushBoundAssets(ledger, scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-a",
		Assets:    scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{{AssetID: "image-a"}}},
	})
	want := []scriptports.AssetBinding{{AssetID: "clip-a", SceneID: "scene-a"}, {AssetID: "image-a", SceneID: "scene-a"}, {AssetID: "image-b", SceneID: "scene-a"}}
	if !reflect.DeepEqual(ledger, want) {
		t.Fatalf("ledger = %#v, want %#v", ledger, want)
	}
}

func (s *mediaSamplerStub) Sample(_ context.Context, _ string, _ string, _ []string, _ []scriptpkg.SegmentAssetCandidate, _ bool) (string, error) {
	s.calls++
	if s.winner == "" {
		return "", nil
	}
	return s.winner, nil
}

func newTestMaterializationProcessor(registry *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer) *VidRushMaterializationProcessor {
	return NewVidRushMaterializationProcessor(registry, finalizer).WithMediaSampler(&firstCandidateMediaSampler{})
}

type firstCandidateMediaSampler struct{}

func (*firstCandidateMediaSampler) Sample(_ context.Context, _ string, _ string, _ []string, candidates []scriptpkg.SegmentAssetCandidate, _ bool) (string, error) {
	if len(candidates) == 0 {
		return "", nil
	}
	return candidates[0].AssetID, nil
}

func TestMaterializationUsesCanonicalMediaSamplerWhenConfigured(t *testing.T) {
	sampler := &mediaSamplerStub{winner: "sampler-winner"}
	processor := (&VidRushMaterializationProcessor{sampler: sampler})
	profile := scriptpkg.SegmentSemanticProfile{SegmentID: "seg-1", TextHash: "hash", Topic: "greek salad", VisualTerms: []scriptpkg.WeightedKeyword{{Value: "feta", Confidence: 1}}}
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "legacy-winner", Provider: scriptpkg.VidRushProviderYouTube, DriveLink: "/tmp/legacy.mp4", Query: "boxing"},
		{AssetID: "sampler-winner", Provider: scriptpkg.VidRushProviderYouTube, DriveLink: "/tmp/sampler.mp4", Query: "greek salad"},
	}
	got := processor.selectPrimaryWithMediaSampler(context.Background(), candidates, profile)
	if got == nil || got.AssetID != "sampler-winner" {
		t.Fatalf("selected candidate = %#v, want sampler-winner", got)
	}
	if sampler.calls != 1 {
		t.Fatalf("sampler calls = %d, want 1", sampler.calls)
	}
}

func (materializationProviderStub) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (materializationProviderStub) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (materializationProviderStub) Acquire(_ context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/clip.mp4", MIMEType: "video/mp4", SizeBytes: 10, ContentSHA256: "hash"}, nil
}
func (materializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, ContentSHA256: artifact.ContentSHA256, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
}

type failingMaterializationProvider struct{}

func (failingMaterializationProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (failingMaterializationProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (failingMaterializationProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, errors.New("provider download unavailable")
}
func (failingMaterializationProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
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
	candidate.ContentSHA256 = artifact.ContentSHA256
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
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/generated.png", MIMEType: "image/png", SizeBytes: 10, ContentSHA256: candidate.AssetID}, nil
}
func (s *generationMaterializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, ContentSHA256: artifact.ContentSHA256, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
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
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/image.jpg", MIMEType: "image/jpeg", SizeBytes: 10, ContentSHA256: "image-hash"}, nil
}
func (internetImageMaterializationProviderStub) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, ContentSHA256: artifact.ContentSHA256, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
}

func TestVidRushMaterializationGeneratesOnlyMissingImagesAndWarmsCache(t *testing.T) {
	provider := &generationMaterializationProviderStub{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
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
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
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
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
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

func TestVidRushMaterializationCarriesWarningsThroughIncrementalMaterialize(t *testing.T) {
	provider := &generationMaterializationProviderStub{fail: true}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{PromptVersion: "incremental-warning-v1", ImagesPerScene: 2}
	plan.MediaPlan.ProviderPolicy.ImageGeneration = "enabled"

	result, err := processor.Materialize(context.Background(), plan, scriptpkg.VidRushSegmentResult{
		SegmentID: "incremental-warning-segment",
		Text:      "a mountain at sunrise",
		TextHash:  "incremental-warning-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "FAILED_REQUIRED_IMAGE_COUNT: required=2 verified=0 segment=incremental-warning-segment"
	if !containsWarning(result.Warnings, want) {
		t.Fatalf("segment warnings = %#v, want %q", result.Warnings, want)
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
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{}
	plan.MediaPlan.ProviderPolicy.YouTube = "enabled"
	_, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "missing-provider",
	}}})
	if err == nil {
		t.Fatal("expected an enabled but unregistered provider to fail closed")
	}
}

func TestVidRushMaterializationStockOnlyFailsWhenPrimaryCannotBePersisted(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(failingMaterializationProvider{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	result, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "stock-only-required-primary",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID:      "stock-required",
			Provider:     scriptpkg.VidRushProviderInternetImages,
			SourceURL:    "https://cdn.example/required.mp4",
			RightsStatus: "unknown",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	segment := result.VidRushSegments[0]
	if segment.Assets.PrimaryVideo != nil || len(segment.Assets.SecondaryImages) != 0 {
		t.Fatalf("a failed acquisition must not bind any media: %+v", segment.Assets)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("a failed required acquisition must surface a warning")
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
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
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

func TestVidRushMaterializationRequiresPersistedCandidateForSecondaryImage(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(materializationProviderStub{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	result, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "segment-1",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "stock-1", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://cdn.example/clip", RightsStatus: "unknown",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result.VidRushSegments) != 1 {
		t.Fatalf("expected one materialized segment, got %#v", result)
	}
	images := result.VidRushSegments[0].Assets.SecondaryImages
	if len(images) != 1 {
		t.Fatalf("secondary images = %#v, want the persisted candidate", images)
	}
	image := images[0]
	if image.DriveLink == "" || image.PersistenceStatus != scriptpkg.VidRushStatusPersisted || image.IndexStatus != scriptpkg.VidRushStatusIndexed {
		t.Fatalf("secondary image is not durable: %#v", image)
	}
	if result.VidRushSegments[0].Assets.PrimaryVideo != nil {
		t.Fatalf("an image candidate must not become the primary video: %+v", result.VidRushSegments[0].Assets.PrimaryVideo)
	}
}

func TestVidRushMaterializationDoesNotBindUnknownRightsImage(t *testing.T) {
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(materializationProviderStub{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	result, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "segment-1",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "stock-1", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://cdn.example/clip", RightsStatus: "unknown",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	// The provider explicitly verifies rights in this stub, so the test also
	// pins that an unknown discovery status cannot leak into final binding.
	images := result.VidRushSegments[0].Assets.SecondaryImages
	if len(images) != 1 || images[0].RightsStatus != "verified" {
		t.Fatalf("rights policy was not applied: %#v", images)
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
	candidate.ContentSHA256 = artifact.ContentSHA256
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
	processor := newTestMaterializationProcessor(registry, finalizer)
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "recovery-segment",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "recovery-asset", Provider: scriptpkg.VidRushProviderInternetImages,
			SourceURL: "https://cdn.example/recovery.mp4", RightsStatus: "unknown",
		}}},
	}}}
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"

	first, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.VidRushSegments[0].Assets.SecondaryImages) != 0 {
		t.Fatal("failed finalization must not bind a secondary image")
	}
	second, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if finalizer.calls != 2 {
		t.Fatalf("finalizer calls = %d, want 2", finalizer.calls)
	}
	if len(second.VidRushSegments[0].Assets.SecondaryImages) != 1 {
		t.Fatal("successful recovery should bind the persisted secondary image")
	}
}

func TestVidRushProviderTimeoutsBoundRemoteOperations(t *testing.T) {
	tests := []struct {
		provider string
		want     time.Duration
	}{
		{provider: scriptpkg.VidRushProviderYouTube, want: vidRushImageAcquireTimeout},
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
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})

	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "single-materialize",
		Text:      "a mountain at sunrise",
		TextHash:  "single-materialize-hash",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID:      "single-stock",
			Provider:     scriptpkg.VidRushProviderInternetImages,
			SourceURL:    "https://cdn.example/single.mp4",
			RightsStatus: "unknown",
		}}},
	}

	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	result, err := processor.Materialize(context.Background(), plan, segment)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets.SecondaryImages) != 1 {
		t.Fatalf("single-segment Materialize must persist and bind the image through the shared boundary: %#v", result.Assets)
	}
	if result.Assets.SecondaryImages[0].DriveLink == "" {
		t.Fatalf("secondary image DriveLink = %q, want a persisted link", result.Assets.SecondaryImages[0].DriveLink)
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
	vidrushMaterializedCache = cacheutil.NewLRU(vidrushMaterializedL1Capacity)
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(internetImageMaterializationProviderStub{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})

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
	vidrushMaterializedCache = cacheutil.NewLRU(vidrushMaterializedL1Capacity)
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
		ContentSHA256:  "sha256-michael-jordan",
		DriveLink:      "https://drive.google.com/file/d/drive-asset-michael-jordan/view",
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
	if len(images) != 1 || images[0].AssetID != "drive-asset-michael-jordan" || images[0].DriveLink == "" || images[0].ContentSHA256 == "" {
		t.Fatalf("reused images = %+v, want the cataloged Drive/hash candidate", images)
	}
}

type refreshReacquireImageProvider struct {
	acquireCalls atomic.Int32
}

func (p *refreshReacquireImageProvider) Name() string {
	return scriptpkg.VidRushProviderInternetImages
}
func (p *refreshReacquireImageProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (p *refreshReacquireImageProvider) Acquire(_ context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	p.acquireCalls.Add(1)
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: "/tmp/fresh-image.jpg", MIMEType: "image/jpeg", SizeBytes: 10, ContentSHA256: "fresh-download-hash"}, nil
}
func (p *refreshReacquireImageProvider) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	candidate := artifact.Candidate
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, ContentSHA256: artifact.ContentSHA256, Width: 1920, Height: 1080, RightsStatus: "verified"}, nil
}

type refreshReacquireFinalizer struct {
	finalizeCalls atomic.Int32
}

func (f *refreshReacquireFinalizer) Finalize(_ context.Context, artifact scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	f.finalizeCalls.Add(1)
	candidate := artifact.Candidate
	candidate.AssetID = "fresh-drive-asset-michael-jordan"
	candidate.ContentSHA256 = artifact.ContentSHA256
	candidate.DriveLink = "https://drive.google.com/file/d/fresh-drive-asset-michael-jordan/view"
	candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	candidate.IndexStatus = scriptpkg.VidRushStatusIndexed
	return candidate, nil
}

// TestVidRushMaterializationForceRefreshReacquiresCatalogedDriveImage pins the
// stale-hash self-heal: with ForceRefreshAssets a durable catalog hit must go
// through Acquire→Verify→Finalize again (fresh digest), never reuse the
// recorded hash. Regression for the Garlasco L3 poisoning where the source
// image was re-encoded upstream and every refresh run kept referencing the
// dead content address instead of committing the new bytes.
func TestVidRushMaterializationForceRefreshReacquiresCatalogedDriveImage(t *testing.T) {
	vidrushMaterializedCache = cacheutil.NewLRU(vidrushMaterializedL1Capacity)
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
		ContentSHA256:  "stale-recorded-hash",
		DriveLink:      "https://drive.google.com/file/d/drive-asset-michael-jordan/view",
		Status:         entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: time.Now().UTC(), LastVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	provider := &refreshReacquireImageProvider{}
	finalizer := &refreshReacquireFinalizer{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessorWithCatalog(registry, finalizer, nil, repo)
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1, ForceRefresh: true}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	plan.MediaPlan.ForceRefreshAssets = true
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "catalog-refresh",
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
	if got := provider.acquireCalls.Load(); got != 1 {
		t.Fatalf("image acquire calls = %d, want 1: force refresh must re-download a cataloged image", got)
	}
	if got := finalizer.finalizeCalls.Load(); got != 1 {
		t.Fatalf("finalizer calls = %d, want 1: force refresh must commit the fresh bytes", got)
	}
	images := result.VidRushSegments[0].Assets.SecondaryImages
	if len(images) != 1 || images[0].AssetID != "fresh-drive-asset-michael-jordan" || images[0].ContentSHA256 != "fresh-download-hash" || images[0].DriveLink == "" {
		t.Fatalf("refreshed images = %+v, want the freshly acquired/finalized candidate, never the stale catalog hash", images)
	}
}

type refreshFailingAcquireProvider struct {
	acquireCalls atomic.Int32
}

func (p *refreshFailingAcquireProvider) Name() string {
	return scriptpkg.VidRushProviderInternetImages
}
func (p *refreshFailingAcquireProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (p *refreshFailingAcquireProvider) Acquire(_ context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	p.acquireCalls.Add(1)
	return scriptports.LocalArtifact{}, errors.New("source unreachable on refresh")
}
func (p *refreshFailingAcquireProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, errors.New("unreachable verifier")
}

// TestVidRushMaterializationForceRefreshStripsRecordedLifecycle pins the
// single strip point: with ForceRefreshAssets a catalog-hydrated candidate
// whose source cannot be re-downloaded must lose its recorded lifecycle, so
// the stale hash can never bind into selection or overlay plans as a ready
// asset. The run degrades to fewer images (visible warning) instead of
// failing the whole job at L3 with a dead content address.
func TestVidRushMaterializationForceRefreshStripsRecordedLifecycle(t *testing.T) {
	vidrushMaterializedCache = cacheutil.NewLRU(vidrushMaterializedL1Capacity)
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
		ContentSHA256:  "stale-recorded-hash",
		DriveLink:      "https://drive.google.com/file/d/drive-asset-michael-jordan/view",
		Status:         entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: time.Now().UTC(), LastVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	provider := &refreshFailingAcquireProvider{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessorWithCatalog(registry, &catalogReuseFinalizer{}, nil, repo)
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1, ForceRefresh: true}
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	plan.MediaPlan.ForceRefreshAssets = true
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "catalog-refresh-unreachable",
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
	if got := provider.acquireCalls.Load(); got != 1 {
		t.Fatalf("image acquire calls = %d, want 1: refresh must attempt the re-download", got)
	}
	for _, c := range result.VidRushSegments[0].Assets.Candidates {
		if readyVidRushCandidate(c) {
			t.Fatalf("candidate %+v stayed ready on refresh without an in-run download; recorded lifecycle must be stripped", c)
		}
		if c.ContentSHA256 == "stale-recorded-hash" && c.PersistenceStatus == scriptpkg.VidRushStatusPersisted {
			t.Fatalf("stale catalog hash still persisted on %+v; refresh must not reuse it", c)
		}
	}
	for _, img := range result.VidRushSegments[0].Assets.SecondaryImages {
		if img.ContentSHA256 == "stale-recorded-hash" {
			t.Fatalf("stale hash selected into secondary images %+v; unreachable sources must degrade, never bind", img)
		}
	}
}
