package wiring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"go.uber.org/zap"
)

// ClipRenderRuntime is the single composition-root owner of the render
// backend graph. All clip-producing flows (clip.render and script.generate)
// must use this instance so they share the same resolver, capability probe,
// Chronon certifier and Rust executor.
type ClipRenderRuntime struct {
	RustExecutor    *rustexec.Executor
	ClipRenderer    clipadapters.ClipRenderExecutor
	ChrononRenderer clipadapters.ClipRenderExecutor
	Resolver        *cliprender.RenderBackendResolver
	Probe           cliprender.BackendCapabilityProbe
	Executor        *clipadapters.ClipRenderExecutorAdapter
}

// BuildClipRenderRuntime lazily creates the canonical render graph and stores
// it on ComposeRoot. Repeated callers therefore cannot accidentally create a
// second resolver/certifier with different backend state.
func BuildClipRenderRuntime(cfg *config.Config, root *ComposeRoot, log *zap.Logger) (*ClipRenderRuntime, error) {
	if root == nil {
		return nil, fmt.Errorf("clip render runtime: composition root is nil")
	}
	if root.ClipRenderRuntime != nil {
		return root.ClipRenderRuntime, nil
	}
	if cfg == nil {
		return nil, fmt.Errorf("clip render runtime: config is required")
	}
	if root.MediaExec == (mediaexec.ExecutionConfig{}) {
		return nil, fmt.Errorf("clip render runtime: resolved media execution config is required")
	}
	if log == nil {
		log = zap.NewNop()
	}

	rustExecutor := rustexec.NewExecutor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	clipRenderer := rustexec.NewClipRendererWithExecutor(
		rustExecutor, root.MediaExec.Policy, root.MediaExec.Profile, log,
	)
	backendProbe := rustexec.NewFFmpegBackendCapabilityProbe(cfg.External.FfmpegPath)
	backendResolver := cliprender.NewRenderBackendResolver(cliprender.NewRenderBackendRegistry())
	chrononBin := strings.TrimSpace(os.Getenv("CHRONON_RENDER_BIN"))
	if chrononBin == "" {
		for _, candidate := range []string{
			filepath.Clean(filepath.Join("..", "Chronon3d", "build", "chronon", "linux-video-fast-dev", "apps", "chronon3d_cli", "chronon3d_cli")),
			filepath.Clean(filepath.Join("..", "Chronon3d", ".tmp", "chronon-builds", "native-verify", "apps", "chronon3d_cli", "chronon3d_cli")),
			"/opt/chronon3d/bin/chronon3d_cli",
		} {
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				chrononBin = candidate
				break
			}
		}
	}
	chrononRenderer := NewChrononClipRenderExecutor(chrononBin, cfg.External.FfmpegPath, log)
	if root.DB != nil && root.DB.DB != nil {
		chrononRenderer.WithChrononMetrics(wireChrononMetricsAdapter(root.DB.DB, log))
	}
	certifier := NewChrononNativeCertifier(chrononBin, cfg.External.FfmpegPath, cfg.Video.EncoderPolicy().Codec, log)
	if certifier != nil {
		// Certification gates backend selection.  Starting it asynchronously
		// allowed the first production renders to observe an uncertified
		// Chronon and fall back to Rust/FFmpeg before the real probe completed.
		// Pay the probe once at composition-root startup so every render sees a
		// stable, authoritative capability result.
		certifier.Certify(context.Background())
	}
	probe := &chrononCertifiedCapabilityProbe{
		base: &chrononAwareCapabilityProbe{base: backendProbe, chrononBin: chrononBin},
		cert: certifier,
	}
	runtime := &ClipRenderRuntime{
		RustExecutor:    rustExecutor,
		ClipRenderer:    clipRenderer,
		ChrononRenderer: chrononRenderer,
		Resolver:        backendResolver,
		Probe:           probe,
	}
	runtime.Executor = clipadapters.NewClipRenderExecutorAdapter(
		runtime.ClipRenderer, runtime.ChrononRenderer, runtime.Resolver, runtime.Probe,
	)
	root.ClipRenderRuntime = runtime
	return runtime, nil
}
