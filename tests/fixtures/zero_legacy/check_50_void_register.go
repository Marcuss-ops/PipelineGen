//go:build ignore

// Package fixtures (zero_legacy/check_50_void_register.go):
// Self-check fixture for scripts/ci-architectural-checks.sh::Check 50.
// The forbidden pattern is a void-returning Register* method whose
// argument list contains a `jobs.Service` reference:
//
//	func (h *X) Register(svc *jobs.Service) {     /* VOID return */
//	    log.Warn("not implemented yet, silently doing nothing")   // FAILS THE GATE
//	}
//
// The canonical P1 #1 contract is:
//
//	func (h *X) Register(svc *jobs.Service) error {  /* error return */
//	    if svc == nil {
//	        return fmt.Errorf("...: %w", appjobs.ErrMissingDeps)
//	    }
//	    ...
//	}
//
// This fixture is intentionally built to trip the Check 50 regex
// (single-line signature, jobs.Service arg, void return). The
// self-check mode (`bash scripts/ci-architectural-checks.sh --self-check`)
// verifies the regex catches this pattern. Operators running the
// gate against the production tree cannot introduce this forbidden
// pattern before the next self-check run catches the regression.
//
// NOTE: this file lives under tests/fixtures/zero_legacy/ — the
// canonical convention per AGENTS.md Pinned-Instance Self-Check.
// Tests + production paths exclude this directory via
// --glob 'tests/fixtures/zero_legacy/**' (the existing global
// matchers already gate the tree); a maintainer adding a NEW void
// Register* signature would surface at PR time AND the
// self-check fixture would trip the post-CI run.
package fixture

import "log"

type BrokenJobHandler struct{}

// Register is a deliberately void-returning P1 #1 audit violation.
// The CI Check 50 regex MUST catch this line via the
// `job_service_arg + void_return` shape; the self-check
// (bash scripts/ci-architectural-checks.sh --self-check) verifies
// the regex against this fixture file. Operators iterating on the
// regex are expected to keep this fixture's signature intact (the
// `Register` method name + `*jobs.Service` argument + `{` opener).
func (h *BrokenJobHandler) Register(svc *Service) {
	log.Println("this is a deliberate P1 #1 audit violation fixture (forbidden void-return on Register* taking jobs.Service)")
}

type jobs struct{}

// Service is the fixture-side marker type that satisfies the
// `*jobs.Service` reference in the BrokenJobHandler.Register
// signature above. The actual `*jobs.Service` lives in
// `internal/capabilities/jobs/queue/service.go` and is imported by the
// canonical handlers; this fixture-side shadow type lets the
// compilation succeed without creating a circular import with
// the test file's `_test.go` package.
type Service = jobs

// reset imports so go vet doesn't complain about unused symbols.
var _ = Service{}
