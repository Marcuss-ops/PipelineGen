// Package replay — engine.go owns the replay engine: the use-case that
// loads a saved bundle, resolves the replay mode against the CURRENT
// execution environment, generates a distinct replay job id, materializes
// and verifies the assets, and resolves the execution strategy (reusing the
// render.SmartResolver) so a replay can reach zero encoding.
//
// Replay ≠ re-generation. The engine enters the SAME deterministic engine
// (bundle → validate mode → materialize+verify → strategy resolver) with
// zero LLM, zero research, zero clip search and zero editorial choices.
//
// Two modes (the distinction is load-bearing for exactness):
//
//	exact   → same plan, same assets, SAME execution environment. Any
//	          version mismatch (renderer / Rust protocol / FFmpeg / encoder
//	          policy) FAILS — never a silent change of the result.
//	current → same plan, same assets, but the CURRENT renderer/FFmpeg/encoder
//	          policy. Used to answer "how does the result change with the new
//	          renderer?".
package replay

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

var (
	// ErrBundleNotFound is returned when the original job has no saved replay
	// bundle (the job never produced one, or it was never saved).
	ErrBundleNotFound = errors.New("replay bundle not found")
	// ErrExactVersionMismatch is returned by exact mode when the recorded
	// execution environment does not match the current one. Exact replay
	// fails, it never silently changes versions.
	ErrExactVersionMismatch = errors.New("replay exact mode: execution environment mismatch")
	// ErrInvalidMode is returned for a mode that is neither exact nor current.
	ErrInvalidMode = errors.New("invalid replay mode")
	// ErrNotWiredEngine is returned when a required engine dependency
	// (bundle store or asset source) is not configured.
	ErrNotWiredEngine = errors.New("replay engine: not wired")
)

// Mode is the replay mode discriminator.
type Mode string

const (
	// ModeExact re-executes the plan with the exact recorded execution
	// environment; any version mismatch fails.
	ModeExact Mode = "exact"
	// ModeCurrent re-executes the plan with the current execution
	// environment (current renderer/FFmpeg/encoder policy).
	ModeCurrent Mode = "current"
)

// ParseMode canonicalizes a mode string. Empty (absent body) defaults to
// exact — the safe, fail-closed default. Returns ErrInvalidMode for anything
// else.
func ParseMode(raw string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(raw))) {
	case "", ModeExact:
		return ModeExact, nil
	case ModeCurrent:
		return ModeCurrent, nil
	default:
		return "", fmt.Errorf("%w: %q (want %q or %q)", ErrInvalidMode, raw, ModeExact, ModeCurrent)
	}
}

// Environment is the CURRENT execution environment at replay time. Exact
// mode compares it field-by-field against the recorded bundle.
type Environment struct {
	RendererVersion     string
	RustProtocolVersion string
	FFmpegVersion       string
	EncoderPolicyHash   string
}

// PreparedReplay is the fully-prepared replay: the bundle's sealed plan and
// identity, the distinct replay job id, the verified materialized assets,
// and the resolved execution strategy.
type PreparedReplay struct {
	OriginalJobID string
	ReplayJobID   string
	Mode          Mode
	PlanSHA256    string
	Plan          render.RenderPlan
	Materialized  []MaterializedAsset
	Strategy      render.CompatibilityReport
}

// Dispatcher is the narrow side-effect port that enqueues a prepared replay
// into the job system and returns the resulting status (canonical "queued").
// The engine is pure; dispatching is the only side effect.
type Dispatcher interface {
	Dispatch(ctx context.Context, prepared PreparedReplay) (status string, err error)
}

// Engine orchestrates replay preparation. It is constructed with the bundle
// store (load), the asset source (materialize+verify) and the strategy
// resolver (zero-encoding decision); a nil strategy resolver degrades to a
// FULL_RENDER report with an explanatory reason.
type Engine struct {
	bundles  BundleStore
	assets   AssetSource
	strategy render.StrategyResolver
	idFor    func(originalJobID string) string
}

// NewEngine constructs the engine. The replay id generator defaults to
// "<original>-replay-<nanos>" (always distinct from the original); tests
// override it with SetIDGenerator for determinism.
func NewEngine(bundles BundleStore, assets AssetSource, strategy render.StrategyResolver) *Engine {
	e := &Engine{bundles: bundles, assets: assets, strategy: strategy}
	e.idFor = func(original string) string {
		return original + "-replay-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	}
	return e
}

// SetIDGenerator overrides the replay id generator. A nil generator is
// ignored (the default stays in place).
func (e *Engine) SetIDGenerator(fn func(originalJobID string) string) {
	if fn != nil {
		e.idFor = fn
	}
}

// Prepare loads and validates the bundle, resolves the mode, generates the
// replay id, materializes+verifies every asset, and resolves the execution
// strategy. Any failure is fail-closed: an unverifiable replay never
// proceeds.
func (e *Engine) Prepare(ctx context.Context, originalJobID string, mode Mode, env Environment) (*PreparedReplay, error) {
	if e == nil || e.bundles == nil {
		return nil, ErrNotWiredEngine
	}
	originalJobID = strings.TrimSpace(originalJobID)
	if originalJobID == "" {
		return nil, fmt.Errorf("%w: original_job_id is required", ErrInvalidBundle)
	}
	switch mode {
	case ModeExact, ModeCurrent:
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidMode, mode)
	}

	bundle, err := e.bundles.Get(ctx, originalJobID)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, fmt.Errorf("%w: %s", ErrBundleNotFound, originalJobID)
	}
	if err := bundle.Validate(); err != nil {
		return nil, err
	}

	if mode == ModeExact {
		if mismatch := exactVersionMismatch(*bundle, env); mismatch != "" {
			return nil, fmt.Errorf("%w: %s", ErrExactVersionMismatch, mismatch)
		}
	}

	replayID := strings.TrimSpace(e.idFor(originalJobID))
	if replayID == "" || replayID == originalJobID {
		return nil, fmt.Errorf("%w: replay job id %q must be non-empty and distinct from the original", ErrInvalidBundle, replayID)
	}

	prepared := &PreparedReplay{
		OriginalJobID: originalJobID,
		ReplayJobID:   replayID,
		Mode:          mode,
		PlanSHA256:    bundle.PlanSHA256,
		Plan:          bundle.RenderPlan,
		Strategy: render.CompatibilityReport{
			Mode:    render.ExecutionRender,
			Reasons: []string{"strategy resolver not configured"},
		},
	}

	if e.assets == nil {
		return nil, fmt.Errorf("%w: asset source is required to materialize replay assets", ErrNotWiredEngine)
	}
	prepared.Materialized = make([]MaterializedAsset, 0, len(bundle.Assets))
	for _, asset := range bundle.Assets {
		m, err := e.assets.Materialize(ctx, asset)
		if err != nil {
			return nil, fmt.Errorf("replay materialize asset %s: %w", asset.AssetID, err)
		}
		prepared.Materialized = append(prepared.Materialized, m)
	}

	if e.strategy != nil {
		report, err := e.strategy.Resolve(ctx, bundle.RenderPlan)
		if err != nil {
			return nil, fmt.Errorf("replay strategy: %w", err)
		}
		prepared.Strategy = report
	}
	return prepared, nil
}

// exactVersionMismatch returns a non-empty, human-readable list of every
// environment dimension that differs between the recorded bundle and the
// current environment, or "" when they all match. Empty encoder policy
// (legacy plan) must match empty: exact replay cannot pin what was never
// recorded.
func exactVersionMismatch(bundle ReplayBundle, env Environment) string {
	var mismatches []string
	if bundle.RendererVersion != env.RendererVersion {
		mismatches = append(mismatches, fmt.Sprintf("renderer: recorded %q != current %q", bundle.RendererVersion, env.RendererVersion))
	}
	if bundle.RustProtocolVersion != env.RustProtocolVersion {
		mismatches = append(mismatches, fmt.Sprintf("rust protocol: recorded %q != current %q", bundle.RustProtocolVersion, env.RustProtocolVersion))
	}
	if bundle.FFmpegVersion != env.FFmpegVersion {
		mismatches = append(mismatches, fmt.Sprintf("ffmpeg: recorded %q != current %q", bundle.FFmpegVersion, env.FFmpegVersion))
	}
	if bundle.EncoderPolicyHash != env.EncoderPolicyHash {
		mismatches = append(mismatches, fmt.Sprintf("encoder policy: recorded %q != current %q", bundle.EncoderPolicyHash, env.EncoderPolicyHash))
	}
	if len(mismatches) == 0 {
		return ""
	}
	return strings.Join(mismatches, "; ")
}
