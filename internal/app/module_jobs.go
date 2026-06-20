package app

import (
	"context"
	"database/sql"
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"go.uber.org/zap"
)

// JobsWiring holds the Jobs HTTP wiring (handler + module registration).
// Source of truth: api/jobs. This struct is returned by WireJobs and is the
// only thing registry.go stores on the wiring panel.
type JobsWiring struct {
	Handler *jobs.JobsHandler
	Module  module.Module
}

// JobsBundle is the Job module's *owned* runtime surface.
//
// Phase-B ownership inversion (June 2026): these four objects used to be
// constructed inline inside composeIntegration (≈40 lines of imperative wiring)
// and then projected onto services{} via flat pointer fields. After Phase B,
// they are constructed once by BuildJobsBundle in this file, returned as a
// typed bundle, and consumed by:
//   - composeIntegration / late-binding cross-injections
//     (CatalogSync.RegisterHandler, YouTubeClip/Voiceover/Books/Lessons
//     RegisterHandler, Realtime adapter, ...)
//
//	- the surrounding HTTP wiring (WireJobs, via CoreDeps.JobsService)
//
// Each module MUST consume only the values it needs; never bundle-as-API.
// Follow-up PRs will turn WireJobs itself into a bundle-only consumer and
// drain CoreDeps.JobsService.
type JobsBundle struct {
	Repo       *appjobs.SQLiteStore
	Dispatcher *appjobs.Dispatcher
	Service    *appjobs.Service
	Facade     *job.Service // domain facade wrapping Service (delegate-fn pattern)
}

// BuildJobsBundle constructs the Job runtime pieces in the canonical order:
//
//  1. SQLite-backed job Store (PR4 contract — replaces the legacy Worker pool).
//  2. In-process Dispatcher (handler registry; kept nil-free until Freeze).
//  3. The application Service that orchestrate enqueue / list / cancel /
//     status propagation.
//  4. The domain job.Service facade: exposes a RegisterHandler hook usable
//     from cross-cutting code without depending on appjobs.Service directly.
//
// Returns a JobsBundle. The bundle is fully constructed but its Dispatcher
// is NOT frozen yet — freezing is performed by WireServices in bootstrap.go
// *after* WireRegistry, so that no new module can register a handler while
// workers are claiming jobs.
//
// Returning `(nil, error)` is reserved for unrecoverable construction errors
// (nil db / nil logger). All four fields are required to be non-nil on success.
func BuildJobsBundle(db *sql.DB, log *zap.Logger) (*JobsBundle, error) {
	if db == nil {
		return nil, fmt.Errorf("build jobs bundle: db is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("build jobs bundle: log is nil")
	}

	repo := appjobs.NewSQLiteStore(db, log)
	dispatcher := appjobs.NewDispatcher()
	svc := appjobs.NewService(repo, dispatcher, log)

	// Domain facade wrapping appjobs.Service so consumers expecting *job.Service
	// (realtime, scriptpkg, scheduler, artlist) can wire through the
	// delegate-fn struct pattern without taking a hard dependency on
	// internal/application/jobs.
	facade := job.NewUnwiredService()
	facade.EnqueueFn = func(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
		return svc.Enqueue(ctx, &appjobs.EnqueueRequest{
			Type:          req.Type,
			Project:       req.Project,
			VideoName:     req.VideoName,
			Payload:       req.Payload,
			Priority:      req.Priority,
			MaxRetries:    req.MaxRetries,
			ActiveKey:     req.ActiveKey,
			CorrelationID: req.CorrelationID,
		})
	}
	facade.GetFn = svc.Get
	facade.CancelFn = svc.Cancel
	facade.ListFn = func(ctx context.Context, filter job.Filter) ([]*job.Job, error) {
		js, err := svc.List(ctx, filter)
		if err != nil {
			return nil, err
		}
		out := make([]*job.Job, len(js))
		for i := range js {
			out[i] = &js[i]
		}
		return out, nil
	}
	facade.IsTerminalFn = func(status job.Status) bool { return status.IsTerminal() }
	facade.SetRegisterHandler(func(jobType string, handler any) error {
		h, ok := handler.(appjobs.HandlerFunc)
		if !ok {
			return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc, got %T", handler)
		}
		return svc.RegisterHandler(jobType, h)
	})

	return &JobsBundle{
		Repo:       repo,
		Dispatcher: dispatcher,
		Service:    svc,
		Facade:     facade,
	}, nil
}

// WireJobs creates the Jobs HTTP handler and registers the module.
//
// Phase-B pilot: still takes *CoreDeps (back-compat). The underlying service
// is now built by BuildJobsBundle → CoreDeps.JobsService, so the only thing
// this function does is mount the HTTP handler on top of an already-wired
// bundle. Once all sibling modules invert, WireJobs will take `(cfg, log,
// *JobsBundle, ctx)` directly and CoreDeps will shed its JobsService field.
func WireJobs(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*JobsWiring, error) {
	handler := jobs.NewJobsHandler(coreDeps.JobsService, log)
	mod := jobs.NewModule(cfg, log, handler)
	log.Info("created Jobs module using api/jobs")
	return &JobsWiring{Handler: handler, Module: mod}, nil
}
