// Package mediaexec contains capability-neutral media execution contracts.
// It deliberately has no FFmpeg implementation or process lifecycle code.
//
// SINGLE OWNER: the canonical definition of every media-execution contract
// (VideoProfile, EncoderPolicy, ExecutionConfig, NormalizeOptions,
// CutAndNormalizeOptions, WatermarkOptions, MediaInfo) lives in
// kernel/media (exec_types.go). This package RE-EXPORTS them as type aliases
// so capability and composition code keeps the mediaexec.* spelling without a
// second, drift-prone copy of the structs. Do NOT reintroduce a struct (or a
// method on these types) here: kernel/media owns the contract, and
// types_parity_test.go fails closed on any divergence.
package mediaexec

import (
	"context"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// VideoProfile describes the fully resolved video artifact independently of
// configuration, transport, or encoder implementation.
// SSOT: kernel/media.VideoContract (AssemblyReadyVideoContractID); the frozen
// values live in kernel/media.DefaultAssemblyMediaContractV2().
type VideoProfile = kernelmedia.VideoProfile

// EncoderPolicy describes how a VideoProfile is encoded.
type EncoderPolicy = kernelmedia.EncoderPolicy

// ExecutionConfig is the resolved media configuration passed from the
// composition root to media capabilities. Platform configuration is mapped
// into this contract once; adapters do not read platform/config themselves.
type ExecutionConfig = kernelmedia.ExecutionConfig

// NormalizeOptions is the canonical normalization request. The scalar fields
// (Width/Height/FPS/Codec/Preset/CRF) remain for source compatibility with
// legacy callers; adapters use them only as fallback overrides when the
// canonical Profile/Policy values are incomplete.
type NormalizeOptions = kernelmedia.NormalizeOptions

// CutAndNormalizeOptions is the canonical cut+normalize request.
type CutAndNormalizeOptions = kernelmedia.CutAndNormalizeOptions

// WatermarkOptions is the canonical watermark request.
type WatermarkOptions = kernelmedia.WatermarkOptions

// MediaInfo is the canonical probe result of a media file.
type MediaInfo = kernelmedia.MediaInfo

// AudioProcessor exposes media audio execution without naming an implementation.
// Implementations belong to infrastructure adapters such as rustexec.
type AudioProcessor interface {
	MergeInputs(context.Context, []string, string) error
	RemoveSilence(context.Context, string, string) error
	Probe(context.Context, string) (*MediaInfo, error)
	RenderAudioPlan(context.Context, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, string) (audio.FinalAudioAsset, error)
}
