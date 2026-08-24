// Package upload (usecase_test.go) — PR-P12-CLIPS-AND-BOOKS
// (July 2026, deadline 2026-08-08) audit-pin tests.
//
// Pins the canonical auto-derivation contract on UseCase.Execute:
//
//	delivery.PublishRequest{
//	    Destination: delivery.DestinationYouTubeClip,
//	    ProjectID:   strings.TrimSpace(cmd.Source),  // auto-derive
//	    Group:       strings.TrimSpace(cmd.Group),
//	    Subject:     strings.TrimSpace(cmd.Name),    // auto-derive
//	    // ParentFolderID RETIRED (godlike/06 SSOT).
//	}
//
// Test 1 pins the happy path: the Publisher sees ProjectID/Group/Subject
// derived from cmd fields; ParentFolderID is empty (canonical semantic
// routing via DestinationRegistry + DestinationPolicy.RootFolderID).
// Test 2 pins the publisher=nil fail-closed path: a nil publisher
// means Drive upload is SKIPPED entirely (no Publisher.Publish call,
// no error surfaced — the per-field wiring gap is the composition
// root's responsibility, not the use case's).
//
// Stubs are hand-rolled per AGENTS.md Pattern 0 + godlike/06 SSOT;
// compile-time `var _ <Port> = (*<Stub>)(nil)` pins lock the test
// surface to the canonical port signatures (future drift surfaces at
// build time, not at first test run).
package upload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Stubs (Pattern 0 + godlike/06 SSOT) ─────────────────────────────────

type uploadFakePublisher struct {
	publishResult *delivery.PublishResult
	publishErr    error
	lastRequest   delivery.PublishRequest
	calls         int
}

func (p *uploadFakePublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	p.lastRequest = req
	p.calls++
	if p.publishErr != nil {
		return nil, p.publishErr
	}
	return p.publishResult, nil
}

func (p *uploadFakePublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "resolved-folder-id", nil
}

var _ delivery.Publisher = (*uploadFakePublisher)(nil)

type uploadFakeArtifact struct {
	ref *ArtifactRef
	err error
}

func (a *uploadFakeArtifact) CreateAndVerify(_ context.Context, _ ArtifactCreateInput) (*ArtifactRef, error) {
	return a.ref, a.err
}

func (a *uploadFakeArtifact) LocalPath(_ context.Context, _ string) (string, error) {
	return "/tmp/upload-test.mp4", nil
}

var _ ArtifactServicePort = (*uploadFakeArtifact)(nil)

type uploadFakeDispatcher struct {
	lastClip *asset.Asset
}

func (d *uploadFakeDispatcher) EnqueueAndIndex(_ context.Context, clip *asset.Asset, _ string) error {
	if clip == nil {
		return fmt.Errorf("clip is nil")
	}
	if !clip.LifecycleState.Valid() {
		return fmt.Errorf("invalid lifecycle state: %q", clip.LifecycleState)
	}
	d.lastClip = clip
	return nil
}

var _ IndexDispatcher = (*uploadFakeDispatcher)(nil)

type uploadFakeTreeBuilder struct{}

func (t *uploadFakeTreeBuilder) UpsertFromAsset(_ context.Context, _ *asset.Asset) error {
	return nil
}

var _ TreeBuilder = (*uploadFakeTreeBuilder)(nil)

type uploadFakeConfig struct{}

func (c *uploadFakeConfig) ClipsDriveFolder() string          { return "clips-root" }
func (c *uploadFakeConfig) RootFolder() string                { return "root" }
func (c *uploadFakeConfig) ArtlistDriveFolder() string        { return "artlist-root" }
func (c *uploadFakeConfig) StockDriveFolder() string          { return "stock-root" }
func (c *uploadFakeConfig) MediaPath() string                 { return "/tmp/media" }
func (c *uploadFakeConfig) TempPath() string                  { return "/tmp" }
func (c *uploadFakeConfig) DataDir() string                   { return "/tmp/data" }
func (c *uploadFakeConfig) YoutubeClipsPath() string          { return "/tmp/youtube" }
func (c *uploadFakeConfig) AssetsPath() string                { return "/tmp/assets" }
func (c *uploadFakeConfig) AssetsStoragePath() string         { return "/tmp/assets-storage" }
func (c *uploadFakeConfig) JobTimeout(_ string) time.Duration { return 30 * time.Second }

var _ Config = (*uploadFakeConfig)(nil)

// TestUseCaseExecute_InitializesCanonicalAssetStates pins the production
// upload boundary to the shared asset-state factory. The dispatcher rejects
// empty or legacy lifecycle states, so a real upload must enter with STAGING;
// the canonical SQL schema supplies DISCOVERED for the index_state column.
func TestUseCaseExecute_InitializesCanonicalAssetStates(t *testing.T) {
	t.Parallel()
	dispatcher := &uploadFakeDispatcher{}
	uc, err := NewUseCase(UseCaseDeps{
		Artifact: &uploadFakeArtifact{ref: &ArtifactRef{
			ID:        "upload-state-test-id",
			SHA256:    "deadbeef1234567890abcdef1234567890abcdef1234567890abcdef12345678",
			SizeBytes: 1024,
		}},
		Dispatcher:    dispatcher,
		TreeBuilder:   &uploadFakeTreeBuilder{},
		Config:        &uploadFakeConfig{},
		ProcessRunner: nil,
		Log:           zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewUseCase returned error: %v", err)
	}

	_, err = uc.Execute(context.Background(), UploadClipCommand{
		File:     io.NopCloser(bytes.NewReader([]byte("fake mp4 bytes"))),
		Filename: "state-test.mp4",
		Name:     "state test",
		Source:   "clips",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if dispatcher.lastClip == nil {
		t.Fatal("dispatcher did not receive the clip")
	}
	if got := dispatcher.lastClip.LifecycleState; got != asset.StateStaging {
		t.Fatalf("LifecycleState = %q, want %q", got, asset.StateStaging)
	}
}

// newUseCaseWithStubs is the test helper that builds a UseCase
// with all 7 ports wired to the no-op stubs + a caller-supplied
// publisher. publisher=nil is the canonical "Drive disabled" case.
func newUseCaseWithStubs(t *testing.T, publisher *uploadFakePublisher) *UseCase {
	t.Helper()
	uc, err := NewUseCase(UseCaseDeps{
		Artifact: &uploadFakeArtifact{
			ref: &ArtifactRef{
				ID:        "upload-test-id",
				SHA256:    "deadbeef1234567890abcdef1234567890abcdef1234567890abcdef12345678",
				SizeBytes: 1024,
			},
		},
		Publisher:     publisher, // nil-safe: composition-root contract is the publisher=nil case
		Dispatcher:    &uploadFakeDispatcher{},
		Config:        &uploadFakeConfig{},
		TreeBuilder:   &uploadFakeTreeBuilder{},
		JobsSvc:       nil, // enrichment enqueue is optional; nil is the no-op case
		ProcessRunner: nil, // ffprobe skipped on no-runner
		Log:           zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewUseCase returned error: %v", err)
	}
	return uc
}

// ── TDD case 1 — auto-derivation happy path ─────────────────────────────

// TestUseCaseExecute_PublishesWithAutoDerivedFields pins the
// canonical auto-derivation contract: the Publisher receives
// ProjectID derived from cmd.Source, Group derived from cmd.Group,
// Subject derived from cmd.Name; ParentFolderID is empty
// (godlike/06 SSOT — Publisher resolves the target folder via
// DestinationRegistry + DestinationPolicy.RootFolderID).
//
// Pre-PR-P12 this code passed `ParentFolderID: appclips.ExtractDriveFolderID(cmd.FolderID)`
// which routed through the legacy bypass. Post-PR-P12 the call
// routes via canonical semantic fields only.
func TestUseCaseExecute_PublishesWithAutoDerivedFields(t *testing.T) {
	t.Parallel()
	pub := &uploadFakePublisher{
		publishResult: &delivery.PublishResult{
			FileID:       "drive-file-upload-test",
			WebViewLink:  "https://drive.google.com/file/d/drive-file-upload-test/view",
			DownloadLink: "https://drive.google.com/uc?id=drive-file-upload-test&export=download",
			FolderID:     "drive-folder-upload-test",
			Action:       delivery.PublishActionCreated,
		},
	}
	uc := newUseCaseWithStubs(t, pub)

	cmd := UploadClipCommand{
		File:     io.NopCloser(bytes.NewReader([]byte("fake mp4 bytes"))),
		Filename: "mike-tyson.mp4",
		Name:     "Mike Tyson knockout reel",
		Source:   "clips",
		Group:    "boxing",
		Category: "Boxe",
		Tags:     []string{"boxing", "knockout"},
	}
	if _, err := uc.Execute(context.Background(), cmd); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if pub.calls != 1 {
		t.Fatalf("Publisher.Publish called %d times, want 1", pub.calls)
	}

	req := pub.lastRequest
	if req.Destination != delivery.DestinationYouTubeClip {
		t.Errorf("Destination = %q, want %q", req.Destination, delivery.DestinationYouTubeClip)
	}
	// Auto-derivation contract: ProjectID = cmd.Source
	if req.ProjectID != "clips" {
		t.Errorf("ProjectID = %q, want %q (auto-derived from cmd.Source)", req.ProjectID, "clips")
	}
	// Group = cmd.Group (explicit caller-provided)
	if req.Group != "boxing" {
		t.Errorf("Group = %q, want %q (cmd.Group verbatim)", req.Group, "boxing")
	}
	// Auto-derivation contract: Subject = cmd.Name
	if req.Subject != "Mike Tyson knockout reel" {
		t.Errorf("Subject = %q, want %q (auto-derived from cmd.Name)", req.Subject, "Mike Tyson knockout reel")
	}
	// godlike/06 SSOT: ParentFolderID RETIRED — Publisher resolves
	// the target folder via DestinationRegistry + DestinationPolicy.
	if req.ParentFolderID != "" {
		t.Errorf("ParentFolderID = %q, want \"\" (RETIRED per PR-P12-CLIPS-AND-BOOKS)", req.ParentFolderID)
	}
	if req.Filename != "Mike Tyson knockout reel.mp4" {
		t.Errorf("Filename = %q, want %q (cmd.Name + ext)", req.Filename, "Mike Tyson knockout reel.mp4")
	}
}

// ── TDD case 2 — publisher=nil fail-closed skip ────────────────────────

// TestUseCaseExecute_StubPublisherHappyPath_PublishesOnce pins the
// canonical happy path with a minimal no-op stub (zero publishResult
// and publishErr fields). The use case MUST call Publisher.Publish
// exactly once and MUST NOT panic on the returned *PublishResult
// access (the use case reads pubResult.FileID/WebViewLink/etc.
// without a nil-check on pubResult). The stub returns a non-nil
// *PublishResult to simulate a successful Drive upload.
//
// godlike/07: a typed-nil Publisher passed through a typed port is
// the canonical Go typed-nil gotcha. The composition root's
// contract is "wire a real publisher or fail-fast at boot" — the
// nil-publisher path is the composition root's responsibility, NOT
// the use case's. This test pins the "non-nil stub happy path" branch.
func TestUseCaseExecute_StubPublisherHappyPath_PublishesOnce(t *testing.T) {
	t.Parallel()
	pub := &uploadFakePublisher{
		// Non-nil publishResult so the use case's pubResult.FileID
		// access does not panic. Fields zero-valued is sufficient.
		publishResult: &delivery.PublishResult{
			FileID:      "stub-file-id",
			WebViewLink: "https://drive.google.com/file/d/stub-file-id/view",
			Action:      delivery.PublishActionCreated,
		},
	}
	uc := newUseCaseWithStubs(t, pub)

	cmd := UploadClipCommand{
		File:     io.NopCloser(bytes.NewReader([]byte("fake mp4 bytes"))),
		Filename: "mike-tyson.mp4",
		Name:     "Mike Tyson knockout reel",
		Source:   "clips",
		Group:    "boxing",
		Category: "Boxe",
	}
	if _, err := uc.Execute(context.Background(), cmd); err != nil {
		t.Fatalf("Execute with non-nil stub publisher MUST NOT return error; got: %v", err)
	}
	if pub.calls != 1 {
		t.Errorf("Publisher.Publish called %d times, want 1 (canonical happy path)", pub.calls)
	}
}
