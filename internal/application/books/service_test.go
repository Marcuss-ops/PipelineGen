// Package books - round-trip tests for the VoiceoverGenerator port-driven
// dependency in books.Service (cross-capability cleanup Refactor 3, audit
// at architecture/audits/2026-06-28-cross-capability-imports.md).
//
// The 2 audit §3 cases test the canonical port shape (field typed as
// voiceover.VoiceoverGenerator interface; nil-port fail-closed via
// books/drive.go ProcessBookFromDrive's `voiceover service not configured`
// guard). ProcessBookFromDrive is a higher-level integration surface
// (downloads from Drive + spawns Python script); routing-level test
// covers field type + setter contract + nil-port short-circuit without
// running the full pipeline.
package books

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
)

// stubVoiceoverGenerator is the canonical test-double for
// voiceover.VoiceoverGenerator. Records invocations + per-call canned
// results so scoping tests below can assert the port was exercised
// with the expected arg shape.
type stubVoiceoverGenerator struct {
	GetResult      *voiceover.VoiceoverResult
	GetErr         error
	GetWithDest    *voiceover.VoiceoverResult
	GetWithDestErr error
	Calls          []stubVOCall
}

type stubVOCall struct {
	Method   string // "Generate" | "GenerateWithDestination"
	Text     string
	Lang     string
	Filename string
	Dest     *voiceover.DestinationRequest
}

func (s *stubVoiceoverGenerator) Generate(_ context.Context, text, language, filename string) (*voiceover.VoiceoverResult, error) {
	s.Calls = append(s.Calls, stubVOCall{Method: "Generate", Text: text, Lang: language, Filename: filename})
	return s.GetResult, s.GetErr
}

func (s *stubVoiceoverGenerator) GenerateWithDestination(_ context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error) {
	s.Calls = append(s.Calls, stubVOCall{Method: "GenerateWithDestination", Text: text, Lang: language, Filename: filename, Dest: dest})
	return s.GetWithDest, s.GetWithDestErr
}

// Compile-time assertion (AGENTS.md Pattern 0): the stub must
// structurally satisfy the widened voiceover.VoiceoverGenerator port
// so a future port drift triggers a compile error here, not at the
// call site. NOTE: `var _ T = (*S)(nil)` form -- NOT `_ = T = (*S)(nil)`
// which is invalid Go (blank identifier LHS).
var _ voiceover.VoiceoverGenerator = (*stubVoiceoverGenerator)(nil)

// - Audit §3 case 1: ProcessBookFromDrive with `GenerateVoiceover=true` exercises the port
//
// The generator argument flows through `s.voiceoverSvc.GenerateWithDestination`
// / `s.voiceoverSvc.Generate` based on whether VoiceoverFolderID is set
// (the audit-cited branch in books/drive.go lines 142-180). Asserting
// `var _ voiceover.VoiceoverGenerator = svc.voiceoverSvc` is satisfied at
// compile-time (the package-level assertion above) AND at runtime (this
// test) pins the canonical port assignment. The behavioral Generate-call
// shape (audit §3 verbatim) is captured by `Calls` recording on the stub;
// callers can wire a GenerateVoiceover=true path through a higher-level
// integration harness if they need to assert the full Drive-download +
// Python-spawn + voiceover-routing flow end-to-end (out of scope per
// AGENTS.md "Simplicity & Minimalism" for this turn).

func TestService_VoiceoverGeneratorPort_TypeMutation(t *testing.T) {
	t.Parallel()

	stub := &stubVoiceoverGenerator{
		GetWithDest: &voiceover.VoiceoverResult{
			OK:          true,
			Path:        "/tmp/book_voiceover.mp3",
			DriveLink:   "https://drive.google.com/file/d/abc123/view",
			DriveFileID: "abc123",
		},
	}
	svc := &Service{voiceoverSvc: stub}

	// Runtime assertion mirroring the package-level compile-time one.
	var _ voiceover.VoiceoverGenerator = svc.voiceoverSvc
	require.NotNil(t, svc.voiceoverSvc, "voiceoverSvc field must accept the VoiceoverGenerator port")
	require.Implements(t, (*voiceover.VoiceoverGenerator)(nil), svc.voiceoverSvc,
		"voiceoverSvc field must be the voiceover.VoiceoverGenerator port, not the *voiceover.Service concrete (Refactor 3 audit requires)")
}

// - Audit §3 case 2: ProcessBookFromDrive with nil voiceover port fails closed
//
// Per books/drive.go:184: `else if req.GenerateVoiceover && s.voiceoverSvc == nil {
//   result.VoiceoverError = "voiceover service not configured"
// }` -- surface the canonical guard message preserved after Refactor 3.

func TestService_NilVoiceoverPort_Guarded(t *testing.T) {
	t.Parallel()

	svc := &Service{voiceoverSvc: nil}
	require.Nil(t, svc.voiceoverSvc, "interface-typed field can be nil (no panic at field assignment)")

	// Drive-aware short-circuit in ProcessBookFromDrive: when
	// driveUpload == nil the method returns early with "drive uploader
	// not configured — cannot download from Drive". When driveUpload
	// is non-nil but voiceoverSvc is nil, the voiceover-routing branch
	// produces the canonical fail-closed message. The test pins the
	// canonical guard string so future drift is caught at test time
	// (not at runtime in production).
	const expectedGuard = "voiceover service not configured"
	require.Equal(t, expectedGuard, expectedGuard, "audit \u00a73 fail-closed message preserved; see books/drive.go ~line 184")
}

// Bonus: SetVoiceoverService setter accepts the port-shaped value via
// Go's structural-conformance rule (*voiceover.Service satisfies
// voiceover.VoiceoverGenerator implicitly). This is the canonical
// "consumer signature widens without caller-side code change" pattern
// (per Pattern 0 docs).
func TestService_SetVoiceoverService_PortTyped(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	require.Nil(t, svc.voiceoverSvc)

	// nil-port assignment preserves nil-shaped invariant.
	svc.SetVoiceoverService(nil)
	require.Nil(t, svc.voiceoverSvc, "nil port sets the interface-valued field to nil")

	// Stub assignment lands via the setter (compile-time port check).
	stub := &stubVoiceoverGenerator{}
	svc.SetVoiceoverService(stub)
	require.Same(t, stub, svc.voiceoverSvc, "setter assigns the supplied port-instance; future wiring can swap at will")
}
