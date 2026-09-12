package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	infraartifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformrenderinggen "github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	"go.uber.org/zap"
)

const clipRenderAsyncCompletionEnv = "CLIP_RENDER_ASYNC_COMPLETION"

func clipRenderAsyncCompletionEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(clipRenderAsyncCompletionEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// clipRenderContinuationEnqueuer is the composition adapter for the
// capability-owned ContinuationEnqueuer port. It reuses the SAME clip.render
// job type and injects the canonical ParentLink into the payload before the
// broker persists it.
type clipRenderContinuationEnqueuer struct {
	jobs job.Service
}

var _ cliprender.ContinuationEnqueuer = (*clipRenderContinuationEnqueuer)(nil)

func (e *clipRenderContinuationEnqueuer) EnqueueContinuation(ctx context.Context, req cliprender.ContinuationRequest) (string, error) {
	if e == nil || e.jobs == nil {
		return "", fmt.Errorf("clip render continuation enqueue: job service is not wired")
	}
	if strings.TrimSpace(req.ParentJobID) == "" || strings.TrimSpace(req.ActiveKey) == "" {
		return "", fmt.Errorf("clip render continuation enqueue: parent_job_id and active_key are required")
	}
	if err := req.Continuation.Validate(); err != nil {
		return "", err
	}

	base := map[string]any{
		"render_phase": "settle",
		"continuation": req.Continuation,
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return "", fmt.Errorf("clip render continuation enqueue: encode payload: %w", err)
	}
	raw = job.InjectParentLink(raw, job.ParentLink{ParentJobID: req.ParentJobID, ParentRunID: req.ParentRunID})
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("clip render continuation enqueue: decode linked payload: %w", err)
	}

	child, err := e.jobs.Enqueue(ctx, &job.EnqueueRequest{
		Type:          cliprender.TypeClipRender,
		Payload:       payload,
		CorrelationID: req.Continuation.Submission.CorrelationID,
		MaxRetries:    3,
		ActiveKey:     req.ActiveKey,
	})
	if err != nil {
		return "", err
	}
	if child == nil || strings.TrimSpace(child.ID) == "" {
		return "", fmt.Errorf("clip render continuation enqueue: broker returned an empty child job")
	}
	return child.ID, nil
}

// clipRenderAsyncExecutor is the single composition object handed to Worker.
// It preserves the normal Render contract while exposing Submit/Settle and the
// optional continuation dependencies through AsyncCompletionProvider.
type clipRenderAsyncExecutor struct {
	inner    cliprender.RenderExecutor
	async    cliprender.AsyncRenderExecutor
	store    cliprender.ContinuationStore
	enqueuer cliprender.ContinuationEnqueuer
}

var _ cliprender.RenderExecutor = (*clipRenderAsyncExecutor)(nil)
var _ cliprender.AsyncRenderExecutor = (*clipRenderAsyncExecutor)(nil)
var _ cliprender.AsyncCompletionProvider = (*clipRenderAsyncExecutor)(nil)

func (e *clipRenderAsyncExecutor) Render(ctx context.Context, plan cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
	return e.inner.Render(ctx, plan)
}

func (e *clipRenderAsyncExecutor) Submit(ctx context.Context, plan cliprender.ClipRenderPlanV1) error {
	return e.async.Submit(ctx, plan)
}

func (e *clipRenderAsyncExecutor) Settle(ctx context.Context, plan cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
	return e.async.Settle(ctx, plan)
}

func (e *clipRenderAsyncExecutor) AsyncCompletionDependencies() (cliprender.ContinuationStore, cliprender.ContinuationEnqueuer, bool) {
	return e.store, e.enqueuer, true
}

// wrapClipRenderAsyncCompletion creates the durable CAS handoff and the
// idempotent settle enqueuer. Default is OFF; when the env switch is ON any
// missing dependency is a boot error rather than a silent fallback to the
// blocking path.
func wrapClipRenderAsyncCompletion(cfg *config.Config, root *ComposeRoot, inner cliprender.RenderExecutor, log *zap.Logger) (cliprender.RenderExecutor, error) {
	if !clipRenderAsyncCompletionEnabled() {
		return inner, nil
	}
	if cfg == nil || root == nil || root.Jobs == nil || root.Jobs.Facade == nil {
		return nil, fmt.Errorf("clip render async completion: config/root/jobs are required")
	}
	asyncExec, ok := inner.(cliprender.AsyncRenderExecutor)
	if !ok {
		return nil, fmt.Errorf("clip render async completion: RenderingGen executor does not implement Submit/Settle")
	}

	casRoot := filepath.Join(cfg.Storage.AbsDataDir(), "cas")
	stager, err := infraartifacts.NewLocalStore(infraartifacts.Config{Workspace: filepath.Join(casRoot, ".staging")})
	if err != nil {
		return nil, fmt.Errorf("clip render async completion: local CAS stager: %w", err)
	}
	store, err := cas.NewStore(cas.Config{Root: casRoot, Stager: stager})
	if err != nil {
		return nil, fmt.Errorf("clip render async completion: CAS store: %w", err)
	}
	continuationStore, err := platformrenderinggen.NewCASContinuationStore(store)
	if err != nil {
		return nil, fmt.Errorf("clip render async completion: continuation store: %w", err)
	}
	enqueuer := &clipRenderContinuationEnqueuer{jobs: root.Jobs.Facade}
	if log != nil {
		log.Info("clip.render async completion enabled",
			zap.String("env", clipRenderAsyncCompletionEnv),
			zap.String("cas_root", casRoot),
			zap.String("completion_transport", "settle continuation / event-driven WaitTerminal"),
		)
	}
	return &clipRenderAsyncExecutor{
		inner:    inner,
		async:    asyncExec,
		store:    continuationStore,
		enqueuer: enqueuer,
	}, nil
}
