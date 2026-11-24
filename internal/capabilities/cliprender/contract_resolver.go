package cliprender

import (
	"context"
	"errors"
	"fmt"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// ---------------------------------------------------------------------------
// Resolver registry (SSOT)
// ---------------------------------------------------------------------------

// NewContractResolver returns the canonical ContractResolver. Contract
// values are materialized from kernel/media; this package only projects the
// kernel contract into its capability-local representation.
func NewContractResolver() ContractResolver {
	return defaultContractResolver{}
}

type defaultContractResolver struct{}

// contractBuilders is the closed registry of supported output contracts.
// Registering an additional ID is a real spec decision (the ID becomes part
// of the compatibility surface); nothing else may branch on contract ID.
var contractBuilders = map[string]func(*RenderRequest) (*ResolvedContract, error){
	OutputContractVeloxAssemblyReadyV1: resolveVeloxAssemblyReadyV1,
	OutputContractVeloxAssemblyReadyV2: resolveVeloxAssemblyReadyV2,
	OutputContractVeloxEditingClipV1:   resolveVeloxAssemblyReadyV1, // legacy alias
}

// validateContractCheckTable validates the declarative validation table before
// it can be used. Dimension names are the stable identifiers used by reports,
// so duplicates or incomplete rows indicate a programming error.
func validateContractCheckTable(checks []contractCheck) error {
	seen := make(map[string]struct{}, len(checks))
	for i, check := range checks {
		if check.dim == "" {
			return fmt.Errorf("contract check %d has an empty dimension", i)
		}
		if check.apply == nil {
			return fmt.Errorf("contract check %q has a nil predicate", check.dim)
		}
		if _, exists := seen[check.dim]; exists {
			return fmt.Errorf("duplicate contract check dimension %q", check.dim)
		}
		seen[check.dim] = struct{}{}
	}
	return nil
}

func validateContractBuilderRegistry(builders map[string]func(*RenderRequest) (*ResolvedContract, error)) error {
	if len(builders) == 0 {
		return errors.New("contract builder registry is empty")
	}
	for id, builder := range builders {
		if id == "" {
			return errors.New("contract builder registry contains an empty contract ID")
		}
		if builder == nil {
			return fmt.Errorf("contract builder %q is nil", id)
		}
	}
	return nil
}

// validateContractTables is intentionally called at package initialization:
// malformed contract metadata must fail closed during startup rather than
// surface as a partial runtime result.
func validateContractTables() error {
	if err := validateContractCheckTable(contractChecks); err != nil {
		return err
	}
	return validateContractBuilderRegistry(contractBuilders)
}

func init() {
	if err := validateContractTables(); err != nil {
		panic("cliprender: invalid contract tables: " + err.Error())
	}
}

func (defaultContractResolver) Resolve(_ context.Context, req *RenderRequest) (*ResolvedContract, error) {
	if req == nil || req.Output == nil {
		return nil, fmt.Errorf("%w: request output block is missing", ErrUnsupportedContract)
	}
	if req.Audio != nil && req.Audio.Mode != "" && req.Audio.Mode != AudioModeCopyIfCompatible {
		return nil, fmt.Errorf("%w: audio.mode %q is not supported: the native mux copies the source stream", ErrInvalidRequest, req.Audio.Mode)
	}
	build, ok := contractBuilders[req.Output.Contract]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedContract, req.Output.Contract)
	}
	return build(req)
}

// resolveAssemblyContract projects the canonical kernel/media contract into
// cliprender's flat working representation. Request geometry/timing remain
// inputs for legacy V1 compatibility; V2 is expected to be validated against
// its exact canonical dimensions by the request boundary.
func resolveAssemblyContract(req *RenderRequest, c kernelmedia.AssemblyMediaContract) (*ResolvedContract, error) {
	resolved := &ResolvedContract{AssemblyMediaContract: c}
	if req != nil && req.Output != nil {
		resolved.Width = req.Output.Width
		resolved.Height = req.Output.Height
		resolved.FPS = kernelmedia.FrameRate{Num: req.Output.FPSNum, Den: req.Output.FPSDen}
	}
	// copy_if_compatible copies the source audio stream verbatim (this lane has
	// no audio transcode), so the certified bitrate is the source's, not the
	// contract's. Pinning 128k rejected every clip whose source carried a
	// higher-bitrate AAC track after the render had already succeeded.
	if req != nil && req.Audio != nil && req.Audio.Mode == AudioModeCopyIfCompatible {
		resolved.AudioBitrate = ""
	}
	return resolved, nil
}

func validateRequestedFPS(contract kernelmedia.AssemblyMediaContract, req *RenderRequest) error {
	if req == nil || req.Output == nil {
		return nil
	}
	if req.Output.FPSNum*contract.FPS.Den != contract.FPS.Num*req.Output.FPSDen {
		return fmt.Errorf("%w: contract=%s expected fps=%d/%d got fps=%d/%d", ErrOutputContractMismatch, contract.ID, contract.FPS.Num, contract.FPS.Den, req.Output.FPSNum, req.Output.FPSDen)
	}
	return nil
}

func resolveVeloxAssemblyReadyV1(req *RenderRequest) (*ResolvedContract, error) {
	c := kernelmedia.DefaultAssemblyMediaContract()
	// V1 is the frozen assembly lane: 1920x1080@24/1. A request that asks for
	// another frame rate cannot be honoured by the contract, so it must fail at
	// resolve time instead of rendering 25 fps bytes that the canonical emission
	// gate then rejects after the GPU work is already spent.
	if err := validateRequestedFPS(c, req); err != nil {
		return nil, err
	}
	return resolveAssemblyContract(req, c)
}

func resolveVeloxAssemblyReadyV2(req *RenderRequest) (*ResolvedContract, error) {
	c := kernelmedia.DefaultAssemblyMediaContractV2()
	if err := validateRequestedFPS(c, req); err != nil {
		return nil, err
	}
	return resolveAssemblyContract(req, c)
}
