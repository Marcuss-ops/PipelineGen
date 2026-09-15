package cliprender

import kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

// Output media contract identities.
//
// The identity is OWNED by internal/kernel/media. These constants re-export it
// rather than restating the string, so the capability wire surface stays
// source-compatible without becoming a second owner of a value that the media
// gate, the Rust assembler and the RenderingGen profile registry also key on.
//
// They live in their own file because request.go sits against the
// max_lines_per_file_strict ceiling (godlike/08): adding the cross-package
// reference there would have crossed it.
const (
	OutputContractVeloxAssemblyReadyV1 = kernelmedia.AssemblyMediaContractID
	OutputContractVeloxAssemblyReadyV2 = kernelmedia.AssemblyMediaContractV2ID

	// OutputContractVeloxEditingClipV1 is a capability-local legacy spelling of
	// V1 understood only by cliprender's resolver registry (contract.go). It is
	// not a cross-system contract and is declared nowhere else.
	OutputContractVeloxEditingClipV1 = "velox-editing-clip-v1"
)
