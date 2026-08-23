// Package voiceover — promo_test.go (P0-#3 central contract tests, July 2026).
//
// P0-#3 cutover pin: the legacy voiceoverGenBridge (June 2026) routed
// the promo path through Service.GenerateWithDestination and returned
// `(*domainvo.Result{OK:false}, nil)` on real failures — silently
// swallowing the error so the workflow's `if voErr != nil` check at
// generate.go:151 saw nil and treated the call as a success on the
// aggregate OK axis.
//
// The post-P0-#3 adapter (promoVoiceoverAdapter) returns a typed Go
// error wrapping ErrPromoVoiceoverGeneration. The tests below pin
// the central contract:
//
//  1. Success path returns *domainvo.Result{OK: true} with DriveLink,
//     DriveFileID, Voice, Filename mapped from the per-item result.
//  2. Failure path returns (nil, err) where
//     errors.Is(err, ErrPromoVoiceoverGeneration) — the typed-sentinel
//     contract callers branch on.
//  3. Nil executor returns a typed error (composition-time fail-fast
//     mirrors the NewService panic for the wired field).
//  4. Command mapping (Text, Language, Destination{Kind:"explicit"},
//     TextHash, RequestID, Strategy, ParentJobID) is correct — a
//     future drift on the typed-port shape breaks the test.
package voiceover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	domainvo "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
)

// stubItemExecutor is the canonical test-double for
// voiceover.VoiceoverItemExecutor. Records invocations + per-call
// canned results so the contract tests can assert the adapter
// threaded the typed GenerateVoiceoverItemCommand correctly.
type stubItemExecutor struct {
	result *VoiceoverItemResult
	err    error
	calls  []stubItemCall
}

type stubItemCall struct {
	item *GenerateVoiceoverItemCommand
}

func (s *stubItemExecutor) Execute(_ context.Context, item *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error) {
	s.calls = append(s.calls, stubItemCall{item: item})
	return s.result, s.err
}

// Compile-time assertion: the stub must structurally satisfy
// voiceover.VoiceoverItemExecutor so a future port drift triggers a
// compile error here, not at the call site.
var _ VoiceoverItemExecutor = (*stubItemExecutor)(nil)

// TestPromoVoiceoverAdapter_SuccessPath_P0_3 pins the happy path:
// the adapter maps the domain command to a typed per-item command,
// calls the executor, and translates the typed result back to
// *domainvo.Result with OK=true + DriveLink + DriveFileID + Voice.
func TestPromoVoiceoverAdapter_SuccessPath_P0_3(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{
		result: &VoiceoverItemResult{
			LocalPath:   "/tmp/promo_test.mp3",
			DriveLink:   "https://drive.google.com/file/d/test-id/view",
			DriveFileID: "test-id",
			Voice:       "it-IT-IsabellaNeural",
			Filename:    "test.mp3",
			Status:      StatusCompleted,
		},
	}
	a := &promoVoiceoverAdapter{executor: stub, log: zap.NewNop()}

	cmd := domainvo.GenerateVoiceoverCommand{
		Text:   "Hello world",
		Locale: "it",
		Destination: domainvo.DestinationRef{
			FolderID: "folder-123",
		},
	}

	result, err := a.Generate(context.Background(), cmd)

	require.NoError(t, err, "P0-#3: success path MUST return nil error")
	require.NotNil(t, result)
	require.True(t, result.OK, "P0-#3: success path MUST set Result.OK=true")
	require.Equal(t, "it", result.Locale)
	require.Equal(t, "Hello world", result.Text)
	require.Equal(t, "it-IT-IsabellaNeural", result.Voice)
	require.Equal(t, "https://drive.google.com/file/d/test-id/view", result.DriveLink,
		"P0-#3: DriveLink MUST be threaded from VoiceoverItemResult.DriveLink")
	require.Equal(t, "test-id", result.DriveFileID,
		"P0-#3: DriveFileID MUST be threaded from VoiceoverItemResult.DriveFileID")
	require.Equal(t, string(StatusCompleted), result.Status)

	// Command-mapping contract: the adapter translates
	// domainvo.GenerateVoiceoverCommand → *voiceover.GenerateVoiceoverItemCommand
	// with the canonical shape. A future drift on the typed-port
	// shape breaks this assertion.
	require.Len(t, stub.calls, 1, "P0-#3: adapter MUST invoke executor exactly once")
	item := stub.calls[0].item
	require.NotNil(t, item)
	require.Equal(t, "Hello world", item.Text)
	require.Equal(t, Language("it"), item.Language)
	require.NotEmpty(t, item.Filename, "P0-#3: adapter MUST derive a non-empty Filename")
	require.False(t, item.TextHash.IsEmpty(), "P0-#3: adapter MUST pre-compute TextHash via voiceover.ComputeTextHash")
	require.NotEmpty(t, item.RequestID, "P0-#3: adapter MUST generate a non-empty RequestID")
	require.Equal(t, item.RequestID, item.ParentJobID,
		"P0-#3: promo path is synchronous (no dispatcher) so RequestID serves as ParentJobID")
	require.Equal(t, "replace", string(item.Strategy),
		"P0-#3: Strategy MUST default to 'replace' (matches pre-fix Service.GenerateWithDestination default)")

	// Destination mapping contract: Kind="explicit" with caller-supplied
	// FolderID. This is the canonical routing shape the
	// ProcessVoiceoverItemUseCase expects (no GroupsResolver call).
	require.NotNil(t, item.Destination, "P0-#3: adapter MUST build a Destination when FolderID is set")
	require.Equal(t, "explicit", item.Destination.Kind,
		"P0-#3: Destination.Kind MUST be 'explicit' (caller-supplied FolderID, no resolver)")
	require.Equal(t, "folder-123", item.Destination.FolderID,
		"P0-#3: Destination.FolderID MUST be threaded from the domain command's Destination.FolderID")
}

// TestPromoVoiceoverAdapter_FailurePath_P0_3 pins the central P0-#3
// contract: real failures surface as a typed Go error wrapping
// ErrPromoVoiceoverGeneration. The workflow's `if voErr != nil`
// check at generate.go:151 correctly flips the response to OK=false
// (no more silent Result{OK:false} + nil).
func TestPromoVoiceoverAdapter_FailurePath_P0_3(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{
		err: errors.New("tts provider unavailable (simulated)"),
	}
	a := &promoVoiceoverAdapter{executor: stub, log: zap.NewNop()}

	cmd := domainvo.GenerateVoiceoverCommand{
		Text:   "Hello world",
		Locale: "it",
	}

	result, err := a.Generate(context.Background(), cmd)

	require.Nil(t, result, "P0-#3: failure path MUST return nil result (caller inspects err only)")
	require.Error(t, err, "P0-#3: failure path MUST return a non-nil error")
	require.True(t, errors.Is(err, ErrPromoVoiceoverGeneration),
		"P0-#3: failure path MUST wrap ErrPromoVoiceoverGeneration so callers can errors.Is branch")
	require.Contains(t, err.Error(), "tts provider unavailable",
		"P0-#3: wrapped error MUST carry the underlying executor error verbatim")
}

// TestPromoVoiceoverAdapter_NilExecutor_P0_3 pins the
// composition-time fail-fast contract: an adapter with a nil
// executor (composition root miss) returns a typed error immediately
// rather than panicking mid-call. This mirrors the panic in
// NewService when ProcessItem is nil — both surfaces
// (constructor + adapter) catch the same invariant violation.
func TestPromoVoiceoverAdapter_NilExecutor_P0_3(t *testing.T) {
	t.Parallel()

	a := &promoVoiceoverAdapter{executor: nil, log: zap.NewNop()}

	result, err := a.Generate(context.Background(), domainvo.GenerateVoiceoverCommand{
		Text:   "Hello world",
		Locale: "it",
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrPromoVoiceoverGeneration),
		"P0-#3: nil-executor adapter MUST surface ErrPromoVoiceoverGeneration")
}

// TestPromoVoiceoverAdapter_CommandValidation_P0_3 pins the
// "validate command" surface: an empty-text command is rejected by
// the domain command's Normalize + Validate before the executor is
// invoked. The adapter returns a typed error.
func TestPromoVoiceoverAdapter_CommandValidation_P0_3(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{} // no calls expected
	a := &promoVoiceoverAdapter{executor: stub, log: zap.NewNop()}

	cmd := domainvo.GenerateVoiceoverCommand{
		Text:   "", // empty → Validate fails
		Locale: "it",
	}

	result, err := a.Generate(context.Background(), cmd)

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrPromoVoiceoverGeneration),
		"P0-#3: invalid command MUST surface ErrPromoVoiceoverGeneration")
	require.Empty(t, stub.calls, "P0-#3: invalid command MUST short-circuit BEFORE the executor invocation")
}
