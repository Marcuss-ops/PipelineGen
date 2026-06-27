// Package handlerutil provides shared utilities for HTTP handlers to reduce
// duplicated patterns across endpoints — service nil-checks, async job enqueuing,
// pagination, and source validation.
//
// Usage:
//
//	if !handlerutil.RequireService(c, h.svc, "my service") {
//	    return
//	}
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
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
		Error(c, http.StatusServiceUnavailable, serviceName+" not initialized")
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
