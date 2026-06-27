// Package handlerutil provides shared utilities for HTTP handlers to reduce
// duplicated patterns across endpoints — service nil-checks, async job
// enqueuing, pagination, and source validation.
//
// Migration note (PR 1, June 2026): this package replaces the drifted
// `package api` block previously sitting in `internal/api/service.go` whose
// doc comment claimed "Package handlerutil" while the actual package
// declaration was `api`. Utilities that depend on `internal/domain/*` types
// (Pagination/JobSummary/BuildJobSummaries) live in jobs.go inside this same
// package and accept the documented pkg/-leaf policy exception for
// `internal/domain/job` (see architecture/deprecations.yaml and
// `Wave 14 transport consolidation in_progress` for the historical context).
//
// Usage:
//
//	if !handlerutil.RequireService(c, h.svc, "my service") {
//	    return
//	}
package handlerutil

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// RequireService checks that a service is non-nil and returns false (having
// written an error response) if it is nil. Use it as a guard at the top of
// any handler method that depends on an optional service.
//
//	func (h *Handler) DoThing(c *gin.Context) {
//	    if !handlerutil.RequireService(c, h.svc, "thing service") {
//	        return
//	    }
//	    // … h.svc is guaranteed non-nil …
//	}
func RequireService(c *gin.Context, svc any, serviceName string) bool {
	if svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, serviceName+" not initialized")
		return false
	}
	return true
}

// RequireJobs checks that the job service is non-nil and returns false if it is
// nil so the caller can return early. Equivalent to RequireService with the
// name set to "job system".
func RequireJobs(c *gin.Context, svc any) bool {
	return RequireService(c, svc, "job system")
}
