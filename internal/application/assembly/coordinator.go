// Package assembly owns the single orchestration seam for eager Velox
// placement and later finalization. It does not perform HTTP calls to a
// worker; it only enqueues canonical broker jobs.
package assembly

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/assembly"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type AssetResolver interface {
	Resolve(ctx context.Context, assetIDs []string) ([]assembly.AssetRequirement, error)
}

type PrepareEarlyCommand struct {
	AssemblyID  string
	ParentJobID string
	ClipIDs     []string
	Project     string
}

type RegisterArtifactCommand struct {
	AssemblyID string
	Asset      assembly.AssetRequirement
}

type FinalizePlanCommand struct {
	Project string
	Plan    assembly.FinalizeV1
}

type Coordinator struct {
	jobs     job.Service
	assets   AssetResolver
	sessions SessionRepository
}

func NewCoordinator(jobs job.Service, assets AssetResolver, sessions SessionRepository) (*Coordinator, error) {
	if jobs == nil || assets == nil || sessions == nil {
		return nil, fmt.Errorf("assembly coordinator requires jobs, assets, and sessions")
	}
	return &Coordinator{jobs: jobs, assets: assets, sessions: sessions}, nil
}

func (c *Coordinator) PrepareEarly(ctx context.Context, cmd PrepareEarlyCommand) (*Session, error) {
	if cmd.AssemblyID == "" || cmd.ParentJobID == "" || len(cmd.ClipIDs) == 0 {
		return nil, fmt.Errorf("assembly id, parent job id, and clip ids are required")
	}
	assets, err := c.assets.Resolve(ctx, cmd.ClipIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve source clips: %w", err)
	}
	payload := assembly.PrepareV1{ContractVersion: assembly.ContractVersion, AssemblyID: cmd.AssemblyID, ParentJobID: cmd.ParentJobID, Revision: 1, OutputContract: assembly.OutputContract, Assets: assets}
	payload.PreparationHash, err = assembly.PreparationHash(payload)
	if err != nil {
		return nil, fmt.Errorf("preparation hash: %w", err)
	}
	if err := payload.Validate(); err != nil {
		return nil, err
	}
	j, err := c.jobs.Enqueue(ctx, &job.EnqueueRequest{Type: assembly.PrepareJobType, Payload: payload, Project: cmd.Project, CorrelationID: cmd.ParentJobID, ActiveKey: "assembly.prepare:" + cmd.AssemblyID})
	if err != nil {
		return nil, fmt.Errorf("enqueue assembly.prepare: %w", err)
	}
	s := &Session{AssemblyID: cmd.AssemblyID, ParentJobID: cmd.ParentJobID, PreparationJobID: j.ID, PreparationHash: payload.PreparationHash, Status: StatusPrefetchQueued, Revision: 1, UpdatedAt: time.Now().UTC()}
	if err := c.sessions.Put(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

func (c *Coordinator) TryFinalize(ctx context.Context, p assembly.FinalizeV1, project string) (*job.Job, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	j, err := c.jobs.Enqueue(ctx, &job.EnqueueRequest{Type: assembly.FinalizeJobType, Payload: p, Project: project, CorrelationID: p.AssemblyID, ActiveKey: "assembly.finalize:" + p.AssemblyID})
	if err != nil {
		return nil, fmt.Errorf("enqueue assembly.finalize: %w", err)
	}
	if s, getErr := c.sessions.Get(ctx, p.AssemblyID); getErr == nil {
		s.Status = StatusFinalizeQueued
		s.Revision = p.Revision
		s.UpdatedAt = time.Now().UTC()
		_ = c.sessions.Put(ctx, s)
	}
	return j, nil
}

// FinalizeWhenReady is the only runtime-assets gate. Producers call this
// after publishing their immutable artifact references; an incomplete call
// returns without enqueueing a leased worker job.
func (c *Coordinator) FinalizeWhenReady(ctx context.Context, assemblyID, preparationID, project string, timeline []assembly.TimelineEntry, runtime []assembly.AssetRequirement) (*job.Job, bool, error) {
	if len(timeline) == 0 || len(runtime) == 0 {
		return nil, false, nil
	}
	p := assembly.FinalizeV1{ContractVersion: assembly.ContractVersion, AssemblyID: assemblyID, PreparationID: preparationID, Revision: 2, OutputContract: assembly.OutputContract, Timeline: timeline, RuntimeAssets: runtime}
	for _, a := range runtime {
		if err := c.RegisterArtifact(ctx, RegisterArtifactCommand{AssemblyID: assemblyID, Asset: a}); err != nil {
			return nil, false, err
		}
	}
	if err := c.RegisterFinalizePlan(ctx, FinalizePlanCommand{Project: project, Plan: p}); err != nil {
		return nil, false, err
	}
	s, err := c.sessions.Get(ctx, assemblyID)
	if err != nil {
		return nil, false, err
	}
	return nil, s.Status == StatusFinalizeQueued, nil
}

// RegisterFinalizePlan records the immutable timeline/runtime requirements.
// It does not lease a worker. The enqueue happens only in
// RegisterArtifact->tryFinalize once every required runtime asset is known.
func (c *Coordinator) RegisterFinalizePlan(ctx context.Context, cmd FinalizePlanCommand) error {
	if err := cmd.Plan.Validate(); err != nil {
		return err
	}
	s, err := c.sessions.Get(ctx, cmd.Plan.AssemblyID)
	if err != nil {
		return err
	}
	s.FinalizePlan = &cmd.Plan
	s.Project = cmd.Project
	s.Status = StatusWaitingRuntime
	s.UpdatedAt = time.Now().UTC()
	if err := c.sessions.Put(ctx, s); err != nil {
		return err
	}
	_, err = c.tryFinalize(ctx, s)
	return err
}

func (c *Coordinator) RegisterArtifact(ctx context.Context, cmd RegisterArtifactCommand) error {
	s, err := c.sessions.Get(ctx, cmd.AssemblyID)
	if err != nil {
		return err
	}
	for _, a := range s.RuntimeAssets {
		if a.AssetID == cmd.Asset.AssetID && a.SHA256 == cmd.Asset.SHA256 {
			return nil
		}
	}
	s.RuntimeAssets = append(s.RuntimeAssets, cmd.Asset)
	s.Status = StatusWaitingRuntime
	s.UpdatedAt = time.Now().UTC()
	if err := c.sessions.Put(ctx, s); err != nil {
		return err
	}
	_, err = c.tryFinalize(ctx, s)
	return err
}

func (c *Coordinator) tryFinalize(ctx context.Context, s *Session) (*job.Job, error) {
	if s == nil || s.FinalizePlan == nil || s.Status == StatusFinalizeQueued || s.Status == StatusCompleted {
		return nil, nil
	}
	provided := make(map[string]assembly.AssetRequirement, len(s.RuntimeAssets))
	for _, a := range s.RuntimeAssets {
		provided[a.AssetID] = a
	}
	for _, required := range s.FinalizePlan.RuntimeAssets {
		if !required.Required {
			continue
		}
		got, ok := provided[required.AssetID]
		if !ok || got.Availability != assembly.AvailabilityKnown || got.SHA256 == "" {
			return nil, nil
		}
	}
	return c.TryFinalize(ctx, *s.FinalizePlan, s.Project)
}

type SessionStatus string

const (
	StatusPrefetchQueued SessionStatus = "PREFETCH_QUEUED"
	StatusPrepared       SessionStatus = "PREPARED"
	StatusWaitingRuntime SessionStatus = "WAITING_RUNTIME_ASSETS"
	StatusFinalizeQueued SessionStatus = "FINALIZE_QUEUED"
	StatusCompleted      SessionStatus = "COMPLETED"
	StatusFailed         SessionStatus = "FAILED"
)

type Session struct {
	AssemblyID       string
	ParentJobID      string
	PreparationJobID string
	PreparationID    string
	PreparationHash  string
	Status           SessionStatus
	Revision         uint64
	UpdatedAt        time.Time
	RuntimeAssets    []assembly.AssetRequirement
	FinalizePlan     *assembly.FinalizeV1
	Project          string
}
type SessionRepository interface {
	Put(context.Context, *Session) error
	Get(context.Context, string) (*Session, error)
}

type MemorySessionRepository struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewMemorySessionRepository() *MemorySessionRepository {
	return &MemorySessionRepository{sessions: make(map[string]*Session)}
}
func (r *MemorySessionRepository) Put(_ context.Context, s *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.sessions[s.AssemblyID] = &cp
	return nil
}
func (r *MemorySessionRepository) Get(_ context.Context, id string) (*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("assembly session %q not found", id)
	}
	cp := *s
	return &cp, nil
}
