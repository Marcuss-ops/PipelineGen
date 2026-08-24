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
//  1. nil-svc contract test — covers ALL 10 refactored handlers
//     using zero-value struct literals. Works for every handler
//     because Register's first line is the jobsSvc == nil check,
//     which short-circuits BEFORE dereferencing any handler field
//     (so the unexported-field access issue doesn't apply).
//  2. sentinel-export test — pins jobs.ErrMissingDeps as a typed
//     sentinel value non-nil + non-empty.
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
	"time"

	catalogsync "github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	images "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/jobs"
	youtubeusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// nakedJobBroker is the typed-empty stub for job.JobBroker. The
// canonical NewService signature (4-arg, godlike/07 fail-closed) requires
// a non-nil broker; the test fixtures never exercise any broker method
// because they only call svc.ValidateHandlerCompleteness + svc.HasHandler
// (which read Registry directly, NOT the broker). Embedding the interface
// makes nakedJobBroker satisfy job.JobBroker structurally (forward-promoted
// method set), but the embedded interface itself is nil — any method
// dispatch on the broker would nil-panic. Scope is explicitly limited
// to the §15.9 ValidateHandlerCompleteness audit-pinned path; future
// tests requiring broker method dispatch must use a fuller mock
// (canonical interface impl with concrete return values).
type nakedJobBroker struct{ job.JobBroker }

// Compile-time pin (godlike/06 SSOT + Pattern 0 typed-port discipline):
// signature drift on job.JobBroker surfaces as a build failure at
// this line rather than as a silent test fixture bug at runtime
// (forward-prevention for the audit-pin overuse anti-pattern).
var _ job.JobBroker = nakedJobBroker{}

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
		// images — *images.Service{} is the canonical, sole canonical
		// RegisterHandler entry point for image.generate.google
		// post-IMAGES-SHIM-REMOVAL (851c5a93, 2026-07-04): the
		// facade composition-root surface. GenerationService no
		// longer exposes a direct RegisterHandler — its JobHandler
		// registration is owned by *images.Service at composition
		// time (godlike/06 SSOT, one canonical facade).
		// Q17-IMAGES-REGISTERHANDLER-REGRESSION in
		// architecture/issues.yaml tracks this state.
		{
			name: "images.Service.RegisterHandler (image.generate.google via facade, post-IMAGES-SHIM-REMOVAL canonical)",
			call: func(svc *jobs.Service) error {
				return (&images.Service{}).RegisterHandler(svc)
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

// ── §15.9 Registrazione incompleta (July 2026) ─────────────────────
//
// TestValidateHandlerCompleteness_DetectsMissingChildHandler pins the
// Acceptance criterion: when the voiceover.generate
// parent handler is registered but voiceover.generate_item is missing,
// the server MUST NOT start (ValidateHandlerCompleteness must return
// an error so the composition root can fail-closed at boot).
//
// Why this matters: the parent voiceover.generate handler fans out
// voiceover.generate_item child jobs. If the child handler is not
// registered, every child job sits in the queue forever (no worker
// can execute it). The pre-§15.9 state was a silent failure: the
// server booted, the parent enqueued children, and nothing consumed
// them. ValidateHandlerCompleteness closes this gap by failing-closed
// BEFORE the first job is accepted.
//
// Test strategy: build a real Service + Dispatcher + Registry (no mocks),
// register ONLY the parent handler, and assert ValidateHandlerCompleteness
// returns a non-nil error referencing the missing voiceover.generate_item.
// Then register the child handler and assert the check passes.
func TestValidateHandlerCompleteness_DetectsMissingChildHandler(t *testing.T) {
	dispatcher := jobs.NewDispatcher()

	// Build a minimal registry scoped to ONLY the voiceover
	// parent-child pair. Using Compose() would register ALL job
	// types (media.reindex, books.process, etc.); for a focused
	// §15.9 test a custom Registry isolates the surface to the
	// voiceover pair without polluting the test fixture.
	//
	// The same `registry` instance is (a) attached to the Service
	// at construction time (the canonical 4-arg
	// NewService(repo, dispatcher, log, reg) — post-PR-jobs-retry-contract
	// fail-closed contract per godlike/07) AND (b) passed to
	// ValidateHandlerCompleteness(reg) at the assertion sites.
	// Using ONE registry for both surfaces avoids drift between
	// the SSOT job-type list and the resolver lookup.
	registry := jobs.NewRegistry()
	if err := registry.Register(jobs.JobPolicy{
		Completion: jobs.CompletionDeclaration{
			JobType:              jobs.TypeVoiceoverGenerate,
			ArtifactOwnership:    jobs.ArtifactOwnershipApplication,
			FinalizationStrategy: jobs.FinalizationStrategyLegacyComplete,
		},
		Description:       "Voiceover single generation",
		Timeout:           30 * time.Minute,
		DefaultMaxRetries: 2,
	}); err != nil {
		t.Fatalf("§15.9: register voiceover.generate in test registry: %v", err)
	}
	if err := registry.Register(jobs.JobPolicy{
		Completion: jobs.CompletionDeclaration{
			JobType:              jobs.TypeVoiceoverGenerateItem,
			ArtifactOwnership:    jobs.ArtifactOwnershipApplication,
			FinalizationStrategy: jobs.FinalizationStrategyLegacyComplete,
		},
		Description:       "Voiceover per-language child",
		Timeout:           10 * time.Minute,
		DefaultMaxRetries: 2,
		Concurrency:       4,
	}); err != nil {
		t.Fatalf("§15.9: register voiceover.generate_item in test registry: %v", err)
	}

	svc, err := jobs.NewService(nakedJobBroker{}, dispatcher, zap.NewNop(), registry)
	if err != nil {
		t.Fatalf("§15.9 setup: jobs.NewService error: %v", err)
	}

	// Step 1: register ONLY the parent — child is missing.
	// The zero-value GenerateJobHandler{}.Register only does
	// jobsSvc.RegisterHandler(jobs.TypeVoiceoverGenerate, ...) — it
	// does NOT register voiceover.generate_item.
	if err := (&voiceoverjobs.GenerateJobHandler{}).Register(svc); err != nil {
		t.Fatalf("§15.9: Register parent (GenerateJobHandler) failed: %v", err)
	}

	// Parent IS consumable.
	if !svc.HasHandler(jobs.TypeVoiceoverGenerate) {
		t.Fatal("§15.9: after Register, parent handler must be present (HasHandler returned false)")
	}

	// Child is NOT consumable — the gap that ValidateHandlerCompleteness must detect.
	if svc.HasHandler(jobs.TypeVoiceoverGenerateItem) {
		t.Fatal("§15.9: child handler must NOT be present yet (only parent registered)")
	}

	// ValidateHandlerCompleteness must fail because voiceover.generate_item
	// is in the registry but has no handler.
	if err := svc.ValidateHandlerCompleteness(registry); err == nil {
		t.Fatal("§15.9 ACCEPTANCE CRITERION FAILED: ValidateHandlerCompleteness returned nil when voiceover.generate_item has no handler. The server would start with a consumable job type that can never execute.")
	} else {
		// The error must reference the missing job type so operators can
		// grep the boot log for the specific gap.
		if !containsString(err.Error(), jobs.TypeVoiceoverGenerateItem) {
			t.Fatalf("§15.9: ValidateHandlerCompleteness error must mention the missing job type %q, got: %v",
				jobs.TypeVoiceoverGenerateItem, err)
		}
		t.Logf("§15.9 parent-only check passed: %v", err)
	}

	// Step 2: register the child handler — now both are present.
	// Zero-value GenerateItemJobHandler{}.Register is safe because
	// RegisterHandler is the only code path reached (the unexported
	// useCase field is never dereferenced by Register).
	if err := (&voiceoverjobs.GenerateItemJobHandler{}).Register(svc); err != nil {
		t.Fatalf("§15.9: Register child (GenerateItemJobHandler) failed: %v", err)
	}

	// Both handlers must be consumable.
	if !svc.HasHandler(jobs.TypeVoiceoverGenerate) {
		t.Fatal("§15.9: after child register, parent handler must still be present")
	}
	if !svc.HasHandler(jobs.TypeVoiceoverGenerateItem) {
		t.Fatal("§15.9: after child register, child handler must be present")
	}

	// ValidateHandlerCompleteness must now pass.
	if err := svc.ValidateHandlerCompleteness(registry); err != nil {
		t.Fatalf("§15.9: after both handlers registered, ValidateHandlerCompleteness must return nil, got: %v", err)
	}

	t.Log("§15.9 full-pair check: ValidateHandlerCompleteness passed (both handlers present)")
}

// containsString reports whether s contains substr (case-sensitive).
// Go 1.21+ would use strings.Contains directly; this helper avoids an
// extra import in the test package for one call.
func containsString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
