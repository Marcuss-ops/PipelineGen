package api

import (
	"github.com/gin-gonic/gin"
	sources "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/jobs"
)

// Handler is a migration alias for SourcesHandler.
type Handler = sources.SourcesHandler

// Package-level compatibility structs so that collapsed handler/middleware files
// using package prefixes (apiutil.X, handlerutil.Y) compile without refactoring all method bodies.
var apiutil = struct {
	OK            func(c *gin.Context, data any)
	Error         func(c *gin.Context, status int, msg string)
	BadRequest    func(c *gin.Context, msg string)
	InternalError func(c *gin.Context, err error)
	NotFound      func(c *gin.Context, msg string)
	Accepted      func(c *gin.Context, data any)
	ClampLimit    func(v, def, max int) int
}{
	OK:            OK,
	Error:         Error,
	BadRequest:    BadRequest,
	InternalError: InternalError,
	NotFound:      NotFound,
	Accepted:      Accepted,
	ClampLimit:    ClampLimit,
}

var handlerutil = struct {
	RequireService       func(c *gin.Context, svc any, serviceName string) bool
	RequireJobs          func(c *gin.Context, svc any) bool
	AsyncJobResponse     func(c *gin.Context, j *jobs.Job, message string)
	ListJobsResponse     func(c *gin.Context, summaries []JobSummary)
	BuildJobSummaries    func(jobsList []jobs.Job) []JobSummary
	EnqueueAsync         func(c *gin.Context, enqueuer Enqueuer, in *EnqueueInput, message string) bool
	ParseJobStatusFilter func(c *gin.Context) *jobs.Status
	ParsePagination      func(c *gin.Context, defaultLimit, maxLimit int) Pagination
}{
	RequireService:       RequireService,
	RequireJobs:          RequireJobs,
	AsyncJobResponse:     AsyncJobResponse,
	ListJobsResponse:     ListJobsResponse,
	BuildJobSummaries:    BuildJobSummaries,
	EnqueueAsync:         EnqueueAsync,
	ParseJobStatusFilter: ParseJobStatusFilter,
	ParsePagination:      ParsePagination,
}
