package cliprender

// contract.go owns the canonical VeloxEditing output contract resolution.
// The precise codec/pixel/timebase values live HERE (single canonical owner,
// per the feature spec §12 "VeloxEditing compatibility contract") — the
// request only selects the contract ID + resolution/fps, and no other file
// duplicates these settings.
//
// The contract is a pure function of the request: no I/O, no ports. It is
// the one preparation phase that requires no adapter.
//
// Structure (registry refactor July 2026):
//   - ValidateContract walks the normative contractChecks table: adding a
//     dimension means appending ONE row, not extending an if-ladder. Each
//     row is a pure predicate returning the mismatch detail VERBATIM to the
//     historical message (format and argument order preserved); table order
//     is normative because the FIRST failing dimension wins the error race.
//   - The resolver dispatches through a closed registry map keyed on the
//     declared output contract ID.

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// ErrUnsupportedContract is the typed sentinel for an output.contract ID the
// capability does not know. Fail-closed: an unknown contract is never
// silently mapped to a default.
var ErrUnsupportedContract = errors.New("clip.render: unsupported output contract")

// ErrContractMismatch is returned when the probed output does not satisfy
// the resolved VeloxEditing contract on any dimension. Fail-closed: a
// rendered file that violates the contract is never published as a derived
// asset — the single-pass render is re-run or the job fails, never silently
// accepted.
var ErrContractMismatch = errors.New("clip.render: rendered output violates the output contract")

// ErrOutputContractMismatch classifies request/contract incompatibilities.
// It intentionally exposes the stable machine-readable code requested by the
// clip.render API while retaining ErrContractMismatch for post-render probes.
var ErrOutputContractMismatch = errors.New("OUTPUT_CONTRACT_MISMATCH")

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func ui(v uint64) string { return strconv.FormatUint(v, 10) }

// quote mirrors fmt %q formatting for the %q-bearing historical literals.
func quote(s string) string { return strconv.Quote(s) }

// contractCheck is one pure validation row: apply reports
// (mismatch, detailVerbatim) — detail WITHOUT the sentinel prefix.
type contractCheck struct {
	dim   string
	apply func(c *ResolvedContract, p *OutputProbe) (mismatch bool, detail string)
}

var contractChecks = []contractCheck{
	{
		dim: "container",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if p.Container != "" && p.Container != c.Container {
				return true, "container " + quote(p.Container) + " != " + quote(c.Container)
			}
			return false, ""
		},
	},
	{
		dim: "video-stream-present",
		apply: func(_ *ResolvedContract, p *OutputProbe) (bool, string) {
			if !p.HasVideo {
				return true, "output has no video stream"
			}
			return false, ""
		},
	},
	{
		dim: "video-codec",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if p.VideoCodec != c.VideoCodec {
				return true, "video codec " + quote(p.VideoCodec) + " != " + quote(c.VideoCodec)
			}
			return false, ""
		},
	},
	{
		dim: "video-profile",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if p.VideoProfile != "" && p.VideoProfile != c.VideoProfile {
				return true, "video profile " + quote(p.VideoProfile) + " != " + quote(c.VideoProfile)
			}
			return false, ""
		},
	},
	{
		dim: "video-level",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if p.VideoLevel != "" && p.VideoLevel != c.VideoLevel {
				return true, "video level " + quote(p.VideoLevel) + " != " + quote(c.VideoLevel)
			}
			return false, ""
		},
	},
	{
		dim: "pixel-format",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if p.PixelFormat != c.PixelFormat {
				return true, "pixel format " + quote(p.PixelFormat) + " != " + quote(c.PixelFormat)
			}
			return false, ""
		},
	},
	{
		dim: "geometry",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if p.Width != c.Width || p.Height != c.Height {
				return true, "geometry " + ui(uint64(p.Width)) + "x" + ui(uint64(p.Height)) +
					" != " + ui(uint64(c.Width)) + "x" + ui(uint64(c.Height))
			}
			return false, ""
		},
	},
	{
		// FPS: exact rational via cross-multiplication — no float epsilon.
		dim: "fps",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.FPSNum > 0 && c.FPSDen > 0 &&
				p.FPSNum*c.FPSDen != c.FPSNum*p.FPSDen {
				return true, "fps " + ui(uint64(p.FPSNum)) + "/" + ui(uint64(p.FPSDen)) +
					" != " + ui(uint64(c.FPSNum)) + "/" + ui(uint64(c.FPSDen))
			}
			return false, ""
		},
	},
	{
		// Timebase: exact rational.
		dim: "video-timebase",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.VideoTimeBaseNum > 0 && c.VideoTimeBaseDen > 0 &&
				p.VideoTimeBaseNum != 0 && p.VideoTimeBaseDen != 0 &&
				p.VideoTimeBaseNum*c.VideoTimeBaseDen != c.VideoTimeBaseNum*p.VideoTimeBaseDen {
				return true, "video timebase " + ui(uint64(p.VideoTimeBaseNum)) + "/" + ui(uint64(p.VideoTimeBaseDen)) +
					" != " + ui(uint64(c.VideoTimeBaseNum)) + "/" + ui(uint64(c.VideoTimeBaseDen))
			}
			return false, ""
		},
	},
	{
		dim: "audio-timebase",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.AudioTimeBaseNum > 0 && c.AudioTimeBaseDen > 0 &&
				p.AudioTimeBaseNum != 0 && p.AudioTimeBaseDen != 0 &&
				p.AudioTimeBaseNum*c.AudioTimeBaseDen != c.AudioTimeBaseNum*p.AudioTimeBaseDen {
				return true, "audio timebase " + ui(uint64(p.AudioTimeBaseNum)) + "/" + ui(uint64(p.AudioTimeBaseDen)) +
					" != " + ui(uint64(c.AudioTimeBaseNum)) + "/" + ui(uint64(c.AudioTimeBaseDen))
			}
			return false, ""
		},
	},
	{
		// SAR: exact rational.
		dim: "sar",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.SARNum > 0 && c.SARDen > 0 && p.SARNum != 0 && p.SARDen != 0 &&
				p.SARNum*c.SARDen != c.SARNum*p.SARDen {
				return true, "SAR " + ui(uint64(p.SARNum)) + "/" + ui(uint64(p.SARDen)) +
					" != " + ui(uint64(c.SARNum)) + "/" + ui(uint64(c.SARDen))
			}
			return false, ""
		},
	},
	{
		dim: "color-range",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.ColorRange != "" && p.ColorRange != "" && p.ColorRange != c.ColorRange {
				return true, "color_range " + quote(p.ColorRange) + " != " + quote(c.ColorRange)
			}
			return false, ""
		},
	},
	{
		dim: "color-space",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.ColorSpace != "" && p.ColorSpace != "" && p.ColorSpace != c.ColorSpace {
				return true, "color_space " + quote(p.ColorSpace) + " != " + quote(c.ColorSpace)
			}
			return false, ""
		},
	},
	{
		dim: "color-transfer",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.ColorTransfer != "" && p.ColorTransfer != "" && p.ColorTransfer != c.ColorTransfer {
				return true, "color_transfer " + quote(p.ColorTransfer) + " != " + quote(c.ColorTransfer)
			}
			return false, ""
		},
	},
	{
		dim: "color-primaries",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.ColorPrimaries != "" && p.ColorPrimaries != "" && p.ColorPrimaries != c.ColorPrimaries {
				return true, "color_primaries " + quote(p.ColorPrimaries) + " != " + quote(c.ColorPrimaries)
			}
			return false, ""
		},
	},
	{
		dim: "field-order",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.FieldOrder != "" && p.FieldOrder != "" && p.FieldOrder != c.FieldOrder {
				return true, "field_order " + quote(p.FieldOrder) + " != " + quote(c.FieldOrder)
			}
			return false, ""
		},
	},
	{
		// GOP.
		dim: "keyframe-interval",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.KeyframeInterval > 0 && p.KeyframeInterval != 0 && p.KeyframeInterval != c.KeyframeInterval {
				return true, "keyframe interval " + itoa(int64(p.KeyframeInterval)) +
					" != " + itoa(int64(c.KeyframeInterval))
			}
			return false, ""
		},
	},
	{
		// Audio contract block (guarded by contract-declared stream count).
		dim: "audio-contract",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			return checkAudioBlock(c, p)
		},
	},
	{
		// Stream layout.
		dim: "video-streams",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.VideoStreams > 0 && p.VideoStreams != 0 && p.VideoStreams != c.VideoStreams {
				return true, "video streams " + itoa(int64(p.VideoStreams)) +
					" != " + itoa(int64(c.VideoStreams))
			}
			return false, ""
		},
	},
	{
		dim: "audio-streams",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if c.AudioStreams > 0 && p.AudioStreams != 0 && p.AudioStreams != c.AudioStreams {
				return true, "audio streams " + itoa(int64(p.AudioStreams)) +
					" != " + itoa(int64(c.AudioStreams))
			}
			return false, ""
		},
	},
	{
		// StartPTS (always 0 for assembly-ready).
		dim: "start-pts",
		apply: func(c *ResolvedContract, p *OutputProbe) (bool, string) {
			if p.StartPTS != c.StartPTS {
				return true, "start_pts " + itoa(p.StartPTS) + " != " + itoa(c.StartPTS)
			}
			return false, ""
		},
	},
}

// checkAudioBlock covers every audio dimension required when the contract
// declares audio streams. Kept as its own pure step so the audio block can be
// tested without a fully-populated video probe. Detail literals reproduce the
// historical ladder byte-for-byte (including probe-before-contract order).
func checkAudioBlock(c *ResolvedContract, p *OutputProbe) (bool, string) {
	if c.AudioStreams <= 0 {
		return false, ""
	}
	if !p.HasAudio {
		return true, "output has no audio stream (contract requires " +
			c.AudioCodec + "/" + strconv.Itoa(c.SampleRate) + "Hz/" + strconv.Itoa(c.Channels) + "ch)"
	}
	if p.AudioCodec != c.AudioCodec {
		return true, "audio codec " + quote(p.AudioCodec) + " != " + quote(c.AudioCodec)
	}
	if c.AudioProfile != "" && p.AudioProfile != "" && p.AudioProfile != c.AudioProfile {
		return true, "audio profile " + quote(p.AudioProfile) + " != " + quote(c.AudioProfile)
	}
	if p.SampleRate != c.SampleRate {
		return true, "sample rate " + strconv.Itoa(p.SampleRate) + " != " + strconv.Itoa(c.SampleRate)
	}
	if p.Channels != c.Channels {
		return true, "channels " + strconv.Itoa(p.Channels) + " != " + strconv.Itoa(c.Channels)
	}
	if c.AudioChannelLayout != "" && p.ChannelLayout != "" && p.ChannelLayout != c.AudioChannelLayout {
		return true, "channel_layout " + quote(p.ChannelLayout) + " != " + quote(c.AudioChannelLayout)
	}
	if c.AudioBitrate != "" && p.AudioBitrate != "" && p.AudioBitrate != c.AudioBitrate {
		return true, "audio bitrate " + quote(p.AudioBitrate) + " != " + quote(c.AudioBitrate)
	}
	return false, ""
}

// ValidateContract is the pure post-render contract gate: it compares the
// probed output facts against the fully-resolved contract by walking the
// normative contractChecks table and failing fast on the FIRST mismatching
// dimension — identical evaluation order and error texts as the historical
// if-ladder this table replaced.
func ValidateContract(contract *ResolvedContract, probe *OutputProbe) error {
	if contract == nil {
		return fmt.Errorf("%w: contract is nil", ErrContractMismatch)
	}
	if probe == nil {
		return fmt.Errorf("%w: probe is nil", ErrContractMismatch)
	}
	for _, chk := range contractChecks {
		if bad, detail := chk.apply(contract, probe); bad {
			return fmt.Errorf("%w: %s", ErrContractMismatch, detail)
		}
	}
	return nil
}

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
	resolved := FromVideoContract(c)
	if req != nil && req.Output != nil {
		resolved.Width = req.Output.Width
		resolved.Height = req.Output.Height
		resolved.FPSNum = req.Output.FPSNum
		resolved.FPSDen = req.Output.FPSDen
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
	return resolveAssemblyContract(req, kernelmedia.DefaultAssemblyMediaContract())
}

func resolveVeloxAssemblyReadyV2(req *RenderRequest) (*ResolvedContract, error) {
	c := kernelmedia.DefaultAssemblyMediaContractV2()
	if err := validateRequestedFPS(c, req); err != nil {
		return nil, err
	}
	return resolveAssemblyContract(req, c)
}
