package content

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	booksApp "github.com/Marcuss-ops/PipelineGen/internal/application/books"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	booksService "github.com/Marcuss-ops/PipelineGen/internal/application/books"
)

// BooksHandler is a thin HTTP shim for book generation. JSON-body
// endpoints run through transport.JSON(I, O) using use-cases defined
// in internal/application/books. The single direct handler method
// (ListJobs) reads query parameters and cannot use the body-only
// transport.JSON contract.
type BooksHandler struct {
	processBookUC          *booksApp.ProcessBookUseCase
	processBookFromDriveUC *booksApp.ProcessBookFromDriveUseCase
	jobsSvc                *jobs.Service
	log                    *zap.Logger
}

// NewBooksHandler constructs a BooksHandler. The books service is
// shared with the use-case instances; jobsSvc is preserved for the
// query-param ListJobs endpoint. The constructor signature is
// unchanged from the pre-transport migration so the app composition
// root (internal/app/registry.go) needs no edits.
func NewBooksHandler(svc *booksService.Service, jobsSvc *jobs.Service, log *zap.Logger) *BooksHandler {
	return &BooksHandler{
		processBookUC:          booksApp.NewProcessBookUseCase(svc, jobsSvc, log),
		processBookFromDriveUC: booksApp.NewProcessBookFromDriveUseCase(svc, log),
		jobsSvc:                jobsSvc,
		log:                    log,
	}
}

// RegisterRoutes registers /api/books routes.
//
//	POST /api/books/process
//	POST /api/books/generate       (alias for /process)
//	POST /api/books/process-from-drive
//	GET  /api/books/jobs
func (h *BooksHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/process", h.ProcessBook)
	r.POST("/generate", h.ProcessBook) // alias kept for backward compat
	r.POST("/process-from-drive", h.ProcessBookFromDrive)
	r.GET("/jobs", h.ListJobs)
}

// ProcessBook is a one-liner through transport.JSON. Request and
// Response types live in internal/application/books so this handler
// stays free of business types, timeouts, and database imports.
func (h *BooksHandler) ProcessBook(c *gin.Context) {
	transport.JSON[booksApp.ProcessBookRequest, booksApp.ProcessBookResponse](
		c,
		h.processBookUC,
		booksApp.ProcessBookErrMapper,
	)
}

// ProcessBookFromDrive is a one-liner through transport.JSON.
func (h *BooksHandler) ProcessBookFromDrive(c *gin.Context) {
	transport.JSON[booksApp.ProcessBookFromDriveRequest, booksApp.ProcessBookFromDriveResponse](
		c,
		h.processBookFromDriveUC,
		booksApp.ProcessBookFromDriveErrMapper,
	)
}

// ListJobs returns all book processing jobs with status, progress, and results.
// Stays handler-direct because GET /api/books/jobs reads query
// parameters (status, limit, offset) — outside the transport.JSON
// body-only contract. Future work: introduce transport.Query[I, O].
//
//	GET /api/books/jobs?status=queued&limit=20&offset=0
func (h *BooksHandler) ListJobs(c *gin.Context) {
	if !api.RequireService(c, h.jobsSvc, "job system") {
		return
	}

	pag := api.ParsePagination(c, 20, 1000)
	jobType := jobs.TypeBooksProcess

	filter := jobs.Filter{
		Type:   &jobType,
		Status: (*jobs.Status)(api.ParseJobStatusFilter(c)),
		Limit:  pag.Limit,
		Offset: pag.Offset,
	}

	jobsList, err := h.jobsSvc.List(c.Request.Context(), filter)
	if err != nil {
		h.log.Error("failed to list book jobs", zap.Error(err))
		api.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	// Dereference []*job.Job → []job.Job for BuildJobSummaries.
	jobVals := make([]jobs.Job, len(jobsList))
	for i, j := range jobsList {
		if j != nil {
			jobVals[i] = *j
		}
	}
	api.ListJobsResponse(c, api.BuildJobSummaries(jobVals))
}
