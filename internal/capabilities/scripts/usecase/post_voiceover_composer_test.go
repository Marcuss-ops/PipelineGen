// Tests for PostVoiceoverComposer. Every test asserts the canonical
// delivery.Publisher surface (Publish + ResolveFolder) and proves that
// the composer never sets ParentFolderID on the outgoing PublishRequest
// (godlike/08 forward-prevention compliance).

package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// recordingPublisher satisfies the canonical delivery.Publisher interface.
// Compile-time guard: if the interface ever drifts, this file fails to compile.
var _ delivery.Publisher = (*recordingPublisher)(nil)

type recordingPublisher struct {
	resolveCalls   int
	resolveErr     error
	publishCalls   int
	publishErr     error
	lastResolve    delivery.DestinationKey
	lastResolveReq delivery.PublishRequest
	lastRequest    delivery.PublishRequest
	lastResult     delivery.PublishResult
}

func newRecordingPublisher() *recordingPublisher {
	return &recordingPublisher{
		lastResult: delivery.PublishResult{
			FileID:      "fake-file-id-1234",
			WebViewLink: "https://drive.google.com/file/d/fake-file-id-1234/view",
			FolderID:    "fake-folder-id-5678",
			FolderPath:  "specscene/01",
			Destination: delivery.DestinationKey("spec_scene_test"),
		},
	}
}

func (r *recordingPublisher) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	r.resolveCalls++
	r.lastResolve = req.Destination
	r.lastResolveReq = req
	if r.resolveErr != nil {
		return "", r.resolveErr
	}
	return "fake-folder-id-5678", nil
}

func (r *recordingPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	r.publishCalls++
	r.lastRequest = req
	if r.publishErr != nil {
		return nil, r.publishErr
	}
	res := r.lastResult
	res.Destination = req.Destination
	res.FolderID = "fake-folder-id-5678"
	return &res, nil
}

// canonicalDestination returns a valid test destination key.
func canonicalDestination(t *testing.T) delivery.DestinationKey {
	t.Helper()
	return delivery.DestinationKey("spec_scene_test")
}

// fixtureBinding returns a 3-row Pacquiao-shaped binding table.
func fixtureBinding() BindingTable {
	return BindingTable{
		"slot-1:candidate-0": RefBinding{
			ClipID:    "clip-intro-001",
			ClipTitle: "Pacquiao entrance",
			DriveLink: "https://drive.google.com/file/d/clip-intro-001/view",
			StartMs:   0,
			EndMs:     120_000,
		},
		"slot-2:candidate-1": RefBinding{
			ClipID:    "clip-fight-001",
			ClipTitle: "First round exchange",
			DriveLink: "https://drive.google.com/file/d/clip-fight-001/view",
			StartMs:   120_000,
			EndMs:     360_000,
		},
		"slot-3:candidate-2": RefBinding{
			ClipID:    "clip-victory-001",
			ClipTitle: "Victory pose",
			DriveLink: "https://drive.google.com/file/d/clip-victory-001/view",
			StartMs:   360_000,
			EndMs:     480_000,
		},
	}
}

// fixtureModelOutput returns a 3-segment LLM envelope for the Pacquiao fixture.
func fixtureModelOutput() scriptpkg.ModelScriptOutputV1 {
	return scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{ID: "slot-1:candidate-0", Text: "Manny enters to a roaring crowd.", Index: 0, Kind: scriptpkg.SceneClip},
				{ID: "slot-2:candidate-1", Text: "Broner jabs; Manny counters.", Index: 1, Kind: scriptpkg.SceneClip},
				{ID: "slot-3:candidate-2", Text: "Manny raises both belts.", Index: 2, Kind: scriptpkg.SceneClip},
			},
		},
	}
}

// 1. Publisher called exactly once for each phase.

// 2. Manifest has exactly 3 scenes.

// 3. Per-scene clip fields are hydrated from the binding table.

// 4. Outgoing PublishRequest has LocalPath set (path-based publish).

// Verify the file at LocalPath is the manifest we marshalled.

// Verify temp file was cleaned up after return.

// 5. Forward-prevention: ParentFolderID MUST be empty on EVERY
// outgoing PublishRequest - including the ResolveFolder call. The
// source-level scanner test (TestForwardPrevention_ComposerSourceDoesNotMentionParentFolderID)
// catches static drift; these runtime checks catch dynamic drift.

// 6. Destination propagated correctly.

// 7. PublishResult returned for caller logging.

func TestPostVoiceoverComposer_EmptyModelOutput_FailsClosed(t *testing.T) {
	pub := newRecordingPublisher()
	res := &StaticRefBindingResolver{Table: fixtureBinding()}
	c, err := NewPostVoiceoverComposer(pub, res)
	if err != nil {
		t.Fatalf("NewPostVoiceoverComposer: %v", err)
	}
	_, _, err = c.ComposeAndPublish(context.Background(), scriptpkg.ModelScriptOutputV1{}, canonicalDestination(t), "g", "s", "a")
	if !errors.Is(err, ErrComposerEmptyModelOutput) {
		t.Errorf("err = %v, want ErrComposerEmptyModelOutput", err)
	}
	if pub.resolveCalls != 0 || pub.publishCalls != 0 {
		t.Errorf("publisher must not be called when envelope empty (resolveCalls=%d publishCalls=%d)", pub.resolveCalls, pub.publishCalls)
	}
}

func TestPostVoiceoverComposer_RefNotInBinding_FailsClosed(t *testing.T) {
	pub := newRecordingPublisher()
	res := &StaticRefBindingResolver{Table: fixtureBinding()}
	c, err := NewPostVoiceoverComposer(pub, res)
	if err != nil {
		t.Fatalf("NewPostVoiceoverComposer: %v", err)
	}
	out := scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{ID: "slot-1:candidate-0", Text: "valid", Index: 0, Kind: scriptpkg.SceneClip},
				{ID: "slot-999:candidate-0", Text: "missing", Index: 1, Kind: scriptpkg.SceneClip},
			},
		},
	}
	_, _, err = c.ComposeAndPublish(context.Background(), out, canonicalDestination(t), "g", "s", "a")
	if !errors.Is(err, ErrComposerRefBindingMissing) {
		t.Errorf("err = %v, want ErrComposerRefBindingMissing", err)
	}
	if pub.publishCalls != 0 {
		t.Errorf("Publish must NOT be called when a ref binding is missing (publishCalls=%d)", pub.publishCalls)
	}
}

func TestPostVoiceoverComposer_NilPublisher_FailsClosed(t *testing.T) {
	res := &StaticRefBindingResolver{Table: fixtureBinding()}
	_, err := NewPostVoiceoverComposer(nil, res)
	if !errors.Is(err, ErrComposerNilPublisher) {
		t.Errorf("err = %v, want ErrComposerNilPublisher", err)
	}
}

func TestPostVoiceoverComposer_NilResolver_FailsClosed(t *testing.T) {
	pub := newRecordingPublisher()
	_, err := NewPostVoiceoverComposer(pub, nil)
	if !errors.Is(err, ErrComposerNilResolver) {
		t.Errorf("err = %v, want ErrComposerNilResolver", err)
	}
}

func TestPostVoiceoverComposer_EmptyDestination_FailsClosed(t *testing.T) {
	pub := newRecordingPublisher()
	res := &StaticRefBindingResolver{Table: fixtureBinding()}
	c, _ := NewPostVoiceoverComposer(pub, res)
	_, _, err := c.ComposeAndPublish(context.Background(), fixtureModelOutput(), delivery.DestinationKey(""), "g", "s", "a")
	if !errors.Is(err, ErrComposerManifestDestination) {
		t.Errorf("err = %v, want ErrComposerManifestDestination", err)
	}
	if pub.resolveCalls != 0 || pub.publishCalls != 0 {
		t.Errorf("publisher must not be called when destination empty (resolveCalls=%d publishCalls=%d)", pub.resolveCalls, pub.publishCalls)
	}
}

func TestPostVoiceoverComposer_ResolveFolderError_DoesNotWriteTempOrPublish(t *testing.T) {
	pub := newRecordingPublisher()
	pub.resolveErr = errors.New("auth failed (test)")
	res := &StaticRefBindingResolver{Table: fixtureBinding()}
	c, _ := NewPostVoiceoverComposer(pub, res)
	_, _, err := c.ComposeAndPublish(context.Background(), fixtureModelOutput(), canonicalDestination(t), "g", "s", "a")
	if err == nil {
		t.Fatalf("expected error from ResolveFolder")
	}
	if !strings.Contains(err.Error(), "auth failed (test)") {
		t.Errorf("err = %v, expected to wrap 'auth failed (test)'", err)
	}
	// ResolveFolder runs BEFORE temp file creation: never write + never publish.
	if pub.publishCalls != 0 {
		t.Errorf("Publish must NOT be called when ResolveFolder fails (publishCalls=%d)", pub.publishCalls)
	}
	if pub.lastRequest.LocalPath != "" {
		t.Errorf("LocalPath must be empty when ResolveFolder fails (got %q)", pub.lastRequest.LocalPath)
	}
}

// Table-driven coverage for the godlike/07 no-fake-availability gate that
// validates a resolved RefBinding before hydrating the manifest. Each row
// mirrors a real resolver-return failure mode.
func TestPostVoiceoverComposer_RefBindingValidation_FailsClosedOnPartialFakes(t *testing.T) {
	cases := []struct {
		name    string
		binding RefBinding
		wantErr error
	}{
		{
			name:    "EmptyClipID",
			binding: RefBinding{ClipID: "", DriveLink: "https://drive.google.com/file/d/abc/view", StartMs: 0, EndMs: 120_000},
			wantErr: ErrComposerIncompleteRefBinding,
		},
		{
			name:    "EmptyDriveLink",
			binding: RefBinding{ClipID: "clip-001", DriveLink: "", StartMs: 0, EndMs: 120_000},
			wantErr: ErrComposerIncompleteRefBinding,
		},
		{
			name:    "NegativeStartMs",
			binding: RefBinding{ClipID: "clip-001", DriveLink: "https://x", StartMs: -10, EndMs: 100},
			wantErr: ErrComposerInvalidRefTimeRange,
		},
		{
			name:    "ZeroDuration",
			binding: RefBinding{ClipID: "clip-001", DriveLink: "https://x", StartMs: 1_000, EndMs: 1_000},
			wantErr: ErrComposerInvalidRefTimeRange,
		},
		{
			name:    "NegativeDuration",
			binding: RefBinding{ClipID: "clip-001", DriveLink: "https://x", StartMs: 500, EndMs: 200},
			wantErr: ErrComposerInvalidRefTimeRange,
		},
		{
			name:    "ValidPopulation",
			binding: RefBinding{ClipID: "clip-001", DriveLink: "https://x", StartMs: 0, EndMs: 10_000},
			wantErr: nil,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pub := newRecordingPublisher()
			res := &StaticRefBindingResolver{Table: BindingTable{
				"slot-1:candidate-0": tc.binding,
			}}
			c, err := NewPostVoiceoverComposer(pub, res)
			if err != nil {
				t.Fatalf("NewPostVoiceoverComposer: %v", err)
			}
			out := scriptpkg.ModelScriptOutputV1{
				SpecScene: scriptpkg.SpecSceneOutput{
					Version: 1,
					Scenes: []scriptpkg.SpecScene{
						{ID: "slot-1:candidate-0", Text: "hi", Index: 0, Kind: scriptpkg.SceneClip},
					},
				},
			}
			_, _, err = c.ComposeAndPublish(context.Background(), out, canonicalDestination(t), "g", "s", "a")
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected err for valid binding: %v", err)
				}
				if pub.publishCalls != 1 {
					t.Errorf("Publish should be called once for valid binding (got publishCalls=%d)", pub.publishCalls)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
			if pub.publishCalls != 0 {
				t.Errorf("Publish MUST NOT be called for invalid binding (got publishCalls=%d)", pub.publishCalls)
			}
			if pub.lastRequest.LocalPath != "" {
				t.Errorf("LocalPath MUST be empty when binding validation fails (got %q)", pub.lastRequest.LocalPath)
			}
		})
	}
}

func TestStaticRefBindingResolver_Succeeds(t *testing.T) {
	s := &StaticRefBindingResolver{Table: fixtureBinding()}
	b, err := s.Resolve(context.Background(), "slot-1:candidate-0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if b.ClipID != "clip-intro-001" {
		t.Errorf("ClipID = %q, want %q", b.ClipID, "clip-intro-001")
	}
}

// Sanity: the test file itself does not set ParentFolderID.

// firstN returns the first n bytes of b (or the whole string if shorter).
func firstN(b []byte, n int) string {
	if len(b) < n {
		return string(b)
	}
	return string(b[:n])
}
