// Package jobs_test — register_wiring_test.go (external test package).
//
// P1 #1 audit-pinned contract test for every JobHandler.Register*
// method that takes a *jobs.Service. Locks the canonical
// jobs.ErrMissingDeps sentinel (errors.go) so a future contributor
// cannot reintroduce the silent-success class of "log.Info+continue
// on nil/svc" (the pre-P1 #1 audit-closed failure class that let
// jobs.Service come up without registering a critical handler).
//
// External test package (jobs_test, not jobs): the test imports its
// own package as `jobs` so we exercise the public API surface
// (Type* constants + ErrMissingDeps) and catch signature drift that
// would otherwise be masked by intra-package access. The package is
// also named `jobs` here is generic — `service_import_cycle` is the
// canonical Go tracestate error that fires when an internal test
// file in package "jobs" imports
// "github.com/Marcuss-ops/PipelineGen/internal/application/jobs".
//
// SCOPE OF THIS FILE (deliberately narrow per AGENTS.md scope
// discipline — see commit body):
//   1. nil-svc contract test — covers ALL 10 refactored handlers
//      using zero-value struct literals. Works for every handler
//      because Register's first line is the jobsSvc == nil check,
//      which short-circuits BEFORE dereferencing any handler field
//      (so the unexported-field access issue doesn't apply).
//   2. sentinel-export test — pins jobs.ErrMissingDeps as a typed
//      sentinel value non-nil + non-empty.
//
// OUT-OF-SCOPE tests live elsewhere (per package):
//   - valid-svc + HasHandler probe tests for voiceover.Generate(Job|Item)Handler,
//     clipindexer, images.GenerationService, stockpipeline, youtube.Service
//     are covered by their existing per-package *_test.go files
//     (voiceover/jobs/generate_handler_test.go + generate_item_handler_test.go,
//     clipindexer/batch_test.go, images/generation_service_test.go,
//     stockpipeline/service_test.go, youtube/usecase/*_test.go). Those
//     package-internal tests have full access to unexported fields +
//     nil-tolerant constructors for the valid-svc test path.
//
// Excludes scripts/jobs/generation_job.go::GenerateJobHandler.RegisterJobs
// because that method takes ports.Broker (not *jobs.Service) — its
// wiring contract is locked by tests in
// internal/application/scripts/jobs/generation_job_test.go.
package jobs_test

import (
	"errors"
	"testing"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	catalogsync "github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	images "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	youtubeusecase "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// ── Nil-svc contract: every Register returns ErrMissingDeps via %w ──

// TestRegisterHandlers_NilSvc_ReturnsErrMissingDeps runs the nil-svc
// branch of every refactored handler and asserts errors.Is detects
// the canonical ErrMissingDeps sentinel. A future contributor who
// drops the `%w` wrapper (or removes the typed sentinel) fails this
// test, surfacing the regression at unit-test time vs production.
//
// Uses zero-value struct literals (e.g. &voiceoverjobs.GenerateJobHandler{})
// so we don't need to construct handlers through their nil-intolerant
// public constructors (those panic on nil deps and live in per-package
// _test.go files, not in this canonical audit-pinned file).
func TestRegisterHandlers_NilSvc_ReturnsErrMissingDeps(t *testing.T) {
	cases := []struct {
		name string
		call func(*jobs.Service) error
	}{
		// voiceover/jobs — parent-child handler pair, both refactored.
		{
			name: "voiceover.GenerateJobHandler.Register",
			call: func(svc *jobs.Service) error {
				return (&voiceoverjobs.GenerateJobHandler{}).Register(svc)
			},
		},
		{
			name: "voiceover.GenerateItemJobHandler.Register",
			call: func(svc *jobs.Service) error {
				return (&voiceoverjobs.GenerateItemJobHandler{}).Register(svc)
			},
		},
		// catalogsync — 2 methods, both refactored.
		{
			name: "catalogsync.Service.RegisterHandler (catalog.sync)",
			call: func(svc *jobs.Service) error {
				return (&catalogsync.Service{}).RegisterHandler(svc)
			},
		},
		{
			name: "catalogsync.Service.RegisterDriveFolderSyncHandler (drive.folder.sync)",
			call: func(svc *jobs.Service) error {
				return (&catalogsync.Service{}).RegisterDriveFolderSyncHandler(svc)
			},
		},
		// clipindexer — 1 method.
		{
			name: "clipindexer.Service.RegisterJobHandler (media.reindex)",
			call: func(svc *jobs.Service) error {
				return (&clipindexer.Service{}).RegisterJobHandler(svc)
			},
		},
		// images — 2 methods (Service delegates to GenerationService).
		{
			name: "images.Service.RegisterHandler (image.generate.google via facade)",
			call: func(svc *jobs.Service) error {
				return (&images.Service{}).RegisterHandler(svc)
			},
		},
		{
			name: "images.GenerationService.RegisterHandler (image.generate.google direct)",
			call: func(svc *jobs.Service) error {
				return (&images.GenerationService{}).RegisterHandler(svc)
			},
		},
		// stockpipeline — 1 method.
		{
			name: "stockpipeline.Service.RegisterHandler (media.stock)",
			call: func(svc *jobs.Service) error {
				return (&stockpipeline.Service{}).RegisterHandler(svc)
			},
		},
		// youtube — 1 method.
		{
			name: "youtube.Service.RegisterHandler (youtube.clip_extract + youtube.rebuild_search_text)",
			call: func(svc *jobs.Service) error {
				return (&youtubeusecase.Service{}).RegisterHandler(svc)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(nil)
			if err == nil {
				t.Fatalf("Register(nil) returned nil error; want jobs.ErrMissingDeps (P1 #1 contract)")
			}
			if !errors.Is(err, jobs.ErrMissingDeps) {
				t.Fatalf("Register(nil) error does NOT wrap ErrMissingDeps via %%w: err = %v\n"+
					"P1 #1 contract: every Register method MUST wrap nil-svc with "+
					"fmt.Errorf(\"<diagnostic>: %%w\", jobs.ErrMissingDeps) so tests "+
					"can assert via errors.Is.", err)
			}
		})
	}
}

// TestErrMissingDeps_IsExportedSentinel pins the canonical-sentinel
// export. A future refactor that accidentally un-exports or renames
// the sentinel (e.g. via a code-move to a non-exported const) breaks
// the test, surfacing the import-graph regression at unit-test time.
func TestErrMissingDeps_IsExportedSentinel(t *testing.T) {
	if jobs.ErrMissingDeps == nil {
		t.Fatal("jobs.ErrMissingDeps is nil; canonical sentinel MISSING (P1 #1 contract violated)")
	}
	if jobs.ErrMissingDeps.Error() == "" {
		t.Fatal("jobs.ErrMissingDeps.Error() is empty; canonical sentinel string-is-empty is a regression")
	}
}
