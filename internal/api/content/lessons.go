package content

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	lessonsApp "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	lessonsService "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
)

// LessonsHandler is a thin HTTP shim for lesson generation. JSON-body
// endpoints run through transport.JSON(I, O) using use-cases defined
// in internal/application/lessons. ListJobs stays handler-direct.
type LessonsHandler struct {
	generateLessonUC *lessonsApp.GenerateLessonUseCase
	jobsSvc          jobs.Service
	log              *zap.Logger
}

// NewLessonsHandler constructs a LessonsHandler. Same signature as
// the pre-transport migration so internal/app/registry.go needs no
// edits.
func NewLessonsHandler(svc *lessonsService.Service, jobsSvc jobs.Service, log *zap.Logger) *LessonsHandler {
	return &LessonsHandler{
		generateLessonUC: lessonsApp.NewGenerateLessonUseCase(svc, jobsSvc, log),
		jobsSvc:          jobsSvc,
		log:              log,
	}
}

// RegisterRoutes registers /api/lessons routes.
//
//	POST /api/lessons/generate
//	GET  /api/lessons/jobs
func (h *LessonsHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.GenerateLesson)
	r.GET("/jobs", h.ListJobs)
}

// GenerateLesson is a one-liner through transport.JSON. Request and
// Response types live in internal/application/lessons.
func (h *LessonsHandler) GenerateLesson(c *gin.Context) {
	transport.JSON[lessonsApp.GenerateLessonRequest, lessonsApp.GenerateLessonResponse](
		c,
		h.generateLessonUC,
		lessonsApp.GenerateLessonErrMapper,
	)
}

// ListJobs returns all lesson generation jobs.
// Stays handler-direct because GET /api/lessons/jobs reads query
// parameters (status, limit, offset) — outside the transport.JSON
// body-only contract. Future work: introduce transport.Query[I, O].
//
//	GET /api/lessons/jobs?status=queued&limit=20&offset=0
func (h *LessonsHandler) ListJobs(c *gin.Context) {
	if !api.RequireService(c, h.jobsSvc, "job system") {
		return
	}

	pag := api.ParsePagination(c, 20, 1000)
	jobType := jobs.TypeLessonsProcess

	filter := jobs.Filter{
		Type:   &jobType,
		Status: (*jobs.Status)(api.ParseJobStatusFilter(c)),
		Limit:  pag.Limit,
		Offset: pag.Offset,
	}

	jobsList, err := h.jobsSvc.List(c.Request.Context(), filter)
	if err != nil {
		h.log.Error("failed to list lesson jobs", zap.Error(err))
		api.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	// jobsList is already []job.Job (value type) from the domain interface.
	jobVals := jobsList
	api.ListJobsResponse(c, api.BuildJobSummaries(jobVals))
}
