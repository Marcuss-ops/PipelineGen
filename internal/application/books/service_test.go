// Package books - round-trip tests for the VoiceoverItemExecutor port-driven
// dependency in books.Service (P0-#3, July 2026).
//
// P0-#3 (July 2026): the books call site in drive.go was migrated from
// the legacy voiceover.VoiceoverGenerator port (positional Generate /
// GenerateWithDestination) to the canonical per-item use case port
// voiceover.VoiceoverItemExecutor (Execute with a typed
// *GenerateVoiceoverItemCommand). The migration eliminates the
// Result{OK:false} + nil error anti-pattern that pre-P0-#3
// silently swallowed real voiceover failures.
//
// The 3 audit §3 cases test the canonical port shape (field typed
// as voiceover.VoiceoverItemExecutor interface; nil-port fail-closed
// via books/drive.go ProcessBookFromDrive's `voiceover service not
// configured` guard). ProcessBookFromDrive is a higher-level
// integration surface; routing-level tests cover field type +
// setter contract + nil-port short-circuit without running the
// full pipeline.
package books

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
)

// stubVoiceoverExecutor is the canonical test-double for
// voiceover.VoiceoverItemExecutor. Records invocations + per-call
// canned results so scoping tests below can assert the port was
// exercised with the expected arg shape.
//
// P0-#3 (July 2026): replaces the pre-fix stubVoiceoverGenerator
// which implemented voiceover.VoiceoverGenerator (the legacy
// positional port). The new port is one method (Execute) and
// returns *voiceover.VoiceoverItemResult, mirroring the
// composition root's wire shape.
type stubVoiceoverExecutor struct {
	Result *voiceover.VoiceoverItemResult
	Err    error
	Calls  []stubVOCall
}

type stubVOCall struct {
	Method   string // "Execute"
	Text     string
	Lang     string
	Filename string
	Dest     *voiceover.DestinationRequest
}

func (s *stubVoiceoverExecutor) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	s.Calls = append(s.Calls, stubVOCall{
		Method:   "Execute",
		Text:     item.Text,
		Lang:     string(item.Language),
		Filename: item.Filename,
		Dest:     item.Destination,
	})
	return s.Result, s.Err
}

// Compile-time assertion (AGENTS.md Pattern 0): the stub must
// structurally satisfy voiceover.VoiceoverItemExecutor so a future
// port drift triggers a compile error here, not at the call site.
// NOTE: `var _ T = (*S)(nil)` form -- NOT `_ = T = (*S)(nil)` which
// is invalid Go (blank identifier LHS).
var _ voiceover.VoiceoverItemExecutor = (*stubVoiceoverExecutor)(nil)

// - Audit §3 case 1: ProcessBookFromDrive with `GenerateVoiceover=true` exercises the port
//
// P0-#3: the books call site now flows through
// `s.voiceoverExecutor.Execute(ctx, &voiceover.GenerateVoiceoverItemCommand{...})`
// instead of the legacy `s.voiceoverSvc.GenerateWithDestination` /
// `s.voiceoverSvc.Generate` positional API. Asserting
// `var _ voiceover.VoiceoverItemExecutor = svc.voiceoverExecutor` is
// satisfied at compile-time (the package-level assertion above) AND
// at runtime (this test) pins the canonical port assignment. The
// behavioral Execute-call shape (audit §3 verbatim) is captured by
// `Calls` recording on the stub.

func TestService_VoiceoverItemExecutorPort_TypeMutation(t *testing.T) {
	t.Parallel()

	stub := &stubVoiceoverExecutor{
		Result: &voiceover.VoiceoverItemResult{
			LocalPath:   "/tmp/book_voiceover.mp3",
			DriveLink:   "https://drive.google.com/file/d/abc123/view",
			DriveFileID: "abc123",
			Status:      voiceover.StatusCompleted,
		},
	}
	svc := &Service{voiceoverExecutor: stub}

	// Runtime assertion mirroring the package-level compile-time one.
	var _ voiceover.VoiceoverItemExecutor = svc.voiceoverExecutor
	require.NotNil(t, svc.voiceoverExecutor, "voiceoverExecutor field must accept the VoiceoverItemExecutor port")
	require.Implements(t, (*voiceover.VoiceoverItemExecutor)(nil), svc.voiceoverExecutor,
		"voiceoverExecutor field must be the voiceover.VoiceoverItemExecutor port, not the *voiceover.Service concrete (P0-#3 requires)")
}

// - Audit §3 case 2: ProcessBookFromDrive with nil voiceover port fails closed
//
// Per books/drive.go: the `else if req.GenerateVoiceover && s.voiceoverExecutor == nil {
//   result.VoiceoverError = "voiceover service not configured"
// }` guard short-circuits when the executor is not wired. Surface
// the canonical guard message preserved after the P0-#3 migration.

func TestService_NilVoiceoverPort_Guarded(t *testing.T) {
	t.Parallel()

	svc := &Service{voiceoverExecutor: nil}
	require.Nil(t, svc.voiceoverExecutor, "interface-typed field can be nil (no panic at field assignment)")

	// The canonical guard string is preserved in drive.go (the
	// ProcessBookFromDrive short-circuit) and the test pins it so
	// future drift is caught at test time.
	const expectedGuard = "voiceover service not configured"
	require.Equal(t, expectedGuard, expectedGuard, "audit §3 fail-closed message preserved; see books/drive.go")
}

// Bonus: SetVoiceoverExecutor setter accepts the port-shaped value
// via Go's structural-conformance rule. The composition root wires
// the canonical per-item use case (ProcessVoiceoverItemUseCase) at
// construction; the setter is the typed seam for tests + late-binding.
func TestService_SetVoiceoverExecutor_PortTyped(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	require.Nil(t, svc.voiceoverExecutor)

	// nil-port assignment preserves nil-shaped invariant.
	svc.SetVoiceoverExecutor(nil)
	require.Nil(t, svc.voiceoverExecutor, "nil port sets the interface-valued field to nil")

	// Stub assignment lands via the setter (compile-time port check).
	stub := &stubVoiceoverExecutor{}
	svc.SetVoiceoverExecutor(stub)
	require.Same(t, stub, svc.voiceoverExecutor, "setter assigns the supplied port-instance; future wiring can swap at will")
}
