package local

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/workernodes"
)

type Broker struct {
	jobs           job.Store
	workers        *workernodes.WorkerNodesRepository
	progress       ProgressSink
	coalescer      *ProgressCoalescer
	finalizer      finalization.JobFinalizer
	preparation    finalization.ArtifactPreparationService
	folderResolver finalization.ArtifactFolderResolver
	log            *zap.Logger
	coalesceOn     bool // true when coalescer is configured; gated via nil-check
}

// Deps is the constructor dependency injection container (mandatory for
// PR-D setter ban, June 2026). Visible-field-line cap ≤8 (cap is enforced
// via Check 23 in scripts/ci-architectural-checks.sh — counted from a
// mirror under internal/app/lifecycle_deps_smoke_test.go).
//
// Progress + Coalescer are TYPICAL for production (coalescer buffered
// behind 100ms window). Pass Coalescer == nil to disable coalescing
// (declares Window=0 semantics inside NewProgressCoalescer) — broker
// will route Progress calls directly to the sink in that mode.
//
// Finalizer is the JobFinalizer for artifact-producing jobs (Spina
// Dorsale, Fase 3). nil = CompleteWithArtifacts will return
// ErrFinalizerNotConfigured. Non-nil = the broker delegates artifact-
// producing completions through the transactional finalization spine.
type Deps struct {
	Jobs      job.Store
	Workers   *workernodes.WorkerNodesRepository
	Progress  ProgressSink
	Coalescer *ProgressCoalescer
	Finalizer finalization.JobFinalizer
	Log       *zap.Logger
}

// New constructs the broker via Deps (PR-D setter ban, June 2026).
// Compiles a typed sentinel if any load-bearing field is missing —
// mirror of PR-Retention's ctor-validation pattern.
func New(d Deps) (*Broker, error) {
	if d.Jobs == nil {
		return nil, fmt.Errorf("local.New: Deps.Jobs is required")
	}
	if d.Progress == nil {
		return nil, fmt.Errorf("local.New: Deps.Progress is required")
	}
	if d.Log == nil {
		return nil, fmt.Errorf("local.New: Deps.Log is required")
	}
	return &Broker{
		jobs:       d.Jobs,
		workers:    d.Workers,
		progress:   d.Progress,
		coalescer:  d.Coalescer,
		finalizer:  d.Finalizer,
		log:        d.Log,
		coalesceOn: d.Coalescer != nil,
	}, nil
}

// WithArtifactPreparation wires the canonical pre-finalization publication
// step. Staged worker artifacts are not considered published until this
// service has resolved and delivered them through the configured publisher.
func (b *Broker) WithArtifactPreparation(p finalization.ArtifactPreparationService) *Broker {
	b.preparation = p
	return b
}

// WithArtifactFolderResolver wires the parent-video folder resolver for
// sidecar artifacts (RenderingGen overlays). Optional: when nil, overlays
// fall back to the destination path builder instead of publishing below the
// already-resolved video folder.
func (b *Broker) WithArtifactFolderResolver(r finalization.ArtifactFolderResolver) *Broker {
	b.folderResolver = r
	return b
}
