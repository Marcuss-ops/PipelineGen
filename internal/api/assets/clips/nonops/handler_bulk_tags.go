// Package nonops — handler_bulk_tags.go: BulkAddTags + BulkRemoveTags
// endpoints extracted from clips/handler_delegators.go per
// PR-CLIPS-NONOPS-EXTRACT (July 2026).
//
// Both methods call the canonical applyBulkTagsDefaults helper to
// extract the duplicated source/IDs/Tags binding + validation
// block — the DRY refactor was the load-bearing motivation for
// the extraction (godlike/07 minimum-blast-radius: a 3-line
// improvement to a 2-method pair is the natural carrier for a
// sub-package move).
package nonops

import (
	"github.com/gin-gonic/gin"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// bulkTagsInput is the canonical flat shape returned by
// applyBulkTagsDefaults. Keeps the helper purely functional — no
// gin.Context coupling leaks into the return signature (the
// helper does READ from c.Param + c.ShouldBindJSON, but the
// return is a typed value).
type bulkTagsInput struct {
	Source string
	IDs    []string
	Tags   []string
}

// bulkTagsValidationError is the typed sentinel returned by
// applyBulkTagsDefaults on binding failure. Callers translate it
// to HTTP 400 via apiutil.BadRequest. Implements the
// godlike/07 typed-error contract (errors.As + errors.Is
// recoverable on the chain).
type bulkTagsValidationError struct{ msg string }

// Error implements the error interface (godlike/07 typed-error contract).
func (e *bulkTagsValidationError) Error() string { return e.msg }

// applyBulkTagsDefaults extracts the canonical 3-step pattern shared
// by BulkAddTags and BulkRemoveTags: read source from path param,
// bind request body, return typed error on binding failure.
//
// godlike/07 NO-FAKE-AVAILABILITY: the helper returns the
// well-formed input exactly as the request expresses it. Empty
// IDs or Tags are NOT auto-rejected here — each handler forwards
// the empty input to the use case which has its own empty-handling
// logic (e.g. AddTags returns an error on empty IDs at the
// use-case layer, not at the transport layer).
//
// godlike/06 SSOT (one canonical owner per fact): this helper is
// the SOLE source of the source-from-path-param + body-bind pattern
// for the 2 BulkTag methods. The 5 hermetic TDD tests in
// handler_test.go pin the contract.
func applyBulkTagsDefaults(c *gin.Context) (bulkTagsInput, error) {
	source := c.Param("source")
	var req struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		return bulkTagsInput{}, &bulkTagsValidationError{msg: err.Error()}
	}
	return bulkTagsInput{Source: source, IDs: req.IDs, Tags: req.Tags}, nil
}

// BulkAddTags adds tags to multiple clips in one request.
// Translated from the 8-line inline implementation in
// clips/handler_delegators.go::BulkAddTags (pre-PR-CLIPS-NONOPS-EXTRACT).
func (h *NonOpsHandler) BulkAddTags(c *gin.Context) {
	input, err := applyBulkTagsDefaults(c)
	if err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	result, err := h.bulkTagsUC.AddTags(c.Request.Context(), appclips.BulkTagsRequest{
		Source: input.Source,
		IDs:    input.IDs,
		Tags:   input.Tags,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  result.Source,
		"count":   result.Count,
		"message": result.Message,
	})
}

// BulkRemoveTags removes tags from multiple clips.
// Translated from the 8-line inline implementation in
// clips/handler_delegators.go::BulkRemoveTags (pre-PR-CLIPS-NONOPS-EXTRACT).
func (h *NonOpsHandler) BulkRemoveTags(c *gin.Context) {
	input, err := applyBulkTagsDefaults(c)
	if err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	result, err := h.bulkTagsUC.RemoveTags(c.Request.Context(), appclips.BulkTagsRequest{
		Source: input.Source,
		IDs:    input.IDs,
		Tags:   input.Tags,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  result.Source,
		"count":   result.Count,
		"message": result.Message,
	})
}
