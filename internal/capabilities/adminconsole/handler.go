package adminconsole

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// context keys used to thread request metadata down to the audit log.
type contextKey string

const (
	actorContextKey       contextKey = "admin_actor"
	requestIDContextKey   contextKey = "admin_request_id"
	idempotencyContextKey contextKey = "admin_idempotency_key"
)

// Handler is the thin HTTP transport for the admin console registry.
type Handler struct {
	service *Service
	log     *zap.Logger
}

// NewHandler creates a new admin console handler.
func NewHandler(service *Service, log *zap.Logger) *Handler {
	return &Handler{service: service, log: log}
}

// RegisterRoutes mounts the admin console endpoints under the given group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/entities", h.handleListEntities)
	rg.GET("/entities/:entity/schema", h.handleSchema)
	rg.GET("/entities/:entity", h.handleList)
	rg.GET("/entities/:entity/:id", h.handleGet)
	rg.PATCH("/entities/:entity/:id", h.handlePatch)
	rg.POST("/entities/:entity/:id/actions/:action", h.handleAction)
}

func (h *Handler) handleListEntities(c *gin.Context) {
	apiutil.OK(c, h.service.ListEntities())
}

func (h *Handler) handleSchema(c *gin.Context) {
	entity := c.Param("entity")
	schema, err := h.service.SchemaFor(entity)
	if err != nil {
		apiutil.NotFound(c, err.Error())
		return
	}
	apiutil.OK(c, schema)
}

func (h *Handler) handleList(c *gin.Context) {
	entity := c.Param("entity")
	desc, ok := h.service.Registry().Get(entity)
	if !ok {
		apiutil.NotFound(c, "unknown entity: "+entity)
		return
	}
	opts, err := parseListOptions(c, desc)
	if err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	result, err := h.service.List(c.Request.Context(), entity, opts)
	if err != nil {
		var optionErr *ListOptionError
		if errors.As(err, &optionErr) {
			apiutil.BadRequest(c, err.Error())
			return
		}
		if errors.Is(err, ErrNotSupported) {
			// The entity is advertised by the registry but has no wired reader.
			// An empty 200 would claim the dataset is empty; 501 states the truth.
			c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
			return
		}
		h.log.Error("failed to list entity", zap.String("entity", entity), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, result)
}

// parseListOptions turns the query string into ListOptions and rejects every
// parameter the entity schema does not advertise. A repeated key is ambiguous,
// so it fails closed rather than silently picking one value.
func parseListOptions(c *gin.Context, desc EntityDescriptor) (ListOptions, error) {
	opts := ListOptions{Filters: map[string]string{}}
	for key, values := range c.Request.URL.Query() {
		if len(values) != 1 {
			return opts, &ListOptionError{Parameter: key, Reason: "must be specified exactly once"}
		}
		value := values[0]
		switch {
		case key == "limit":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 500 {
				return opts, &ListOptionError{Parameter: key, Reason: "must be an integer between 1 and 500"}
			}
			opts.Limit = n
		case key == "offset":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return opts, &ListOptionError{Parameter: key, Reason: "must be a non-negative integer"}
			}
			opts.Offset = n
		case key == "order_by":
			if !listFieldSupports(desc.ListFields, value, func(f FieldDescriptor) bool { return f.Sortable }) {
				return opts, &ListOptionError{Parameter: key, Reason: fmt.Sprintf("field %q is not sortable for entity %q", value, desc.Key)}
			}
			opts.OrderBy = value
		case key == "order_dir":
			dir := strings.ToLower(value)
			if dir != "asc" && dir != "desc" {
				return opts, &ListOptionError{Parameter: key, Reason: `must be "asc" or "desc"`}
			}
			opts.OrderDir = dir
		case strings.HasPrefix(key, "filter."):
			field := strings.TrimPrefix(key, "filter.")
			if field == "" {
				return opts, &ListOptionError{Parameter: key, Reason: "filter key is empty"}
			}
			if !listFieldSupports(desc.ListFields, field, func(f FieldDescriptor) bool { return f.Filterable }) {
				return opts, &ListOptionError{Parameter: key, Reason: fmt.Sprintf("field %q is not filterable for entity %q", field, desc.Key)}
			}
			if strings.TrimSpace(value) == "" {
				return opts, &ListOptionError{Parameter: key, Reason: "requires a non-empty value"}
			}
			opts.Filters[field] = value
		default:
			return opts, &ListOptionError{Parameter: key, Reason: "unknown query parameter"}
		}
	}
	if opts.OrderDir != "" && opts.OrderBy == "" {
		return opts, &ListOptionError{Parameter: "order_dir", Reason: "requires order_by"}
	}
	return opts, nil
}

func (h *Handler) handleGet(c *gin.Context) {
	entity := c.Param("entity")
	id := c.Param("id")
	item, err := h.service.Get(c.Request.Context(), entity, id)
	if err != nil {
		h.log.Error("failed to get entity", zap.String("entity", entity), zap.String("id", id), zap.Error(err))
		apiutil.NotFound(c, err.Error())
		return
	}
	apiutil.OK(c, item)
}

func (h *Handler) handlePatch(c *gin.Context) {
	entity := c.Param("entity")
	id := c.Param("id")
	var req struct {
		ExpectedVersion int            `json:"expected_version"`
		Changes         map[string]any `json:"changes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	ctx := withRequestMetadata(c.Request.Context(), c)
	updated, err := h.service.Patch(ctx, entity, id, req.Changes, req.ExpectedVersion)
	if err != nil {
		h.log.Error("failed to patch entity", zap.String("entity", entity), zap.String("id", id), zap.Error(err))
		var verr *VersionConflictError
		if errors.As(err, &verr) {
			c.JSON(http.StatusConflict, map[string]any{
				"error_code":      "VERSION_CONFLICT",
				"message":         err.Error(),
				"current_version": verr.CurrentVersion,
			})
			return
		}
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, updated)
}

func (h *Handler) handleAction(c *gin.Context) {
	entity := c.Param("entity")
	id := c.Param("id")
	action := c.Param("action")
	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil && c.Request.ContentLength > 0 {
		apiutil.BadRequest(c, err.Error())
		return
	}
	result, err := h.service.Action(c.Request.Context(), entity, id, action, payload)
	if err != nil {
		h.log.Error("failed to run action", zap.String("entity", entity), zap.String("id", id), zap.String("action", action), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, result)
}

// withRequestMetadata injects request-scoped metadata into ctx so the
// audit logger can record actor, request_id and idempotency_key.
func withRequestMetadata(ctx context.Context, c *gin.Context) context.Context {
	actor := "admin"
	if u, ok := c.Get("user"); ok {
		if s, ok := u.(string); ok && s != "" {
			actor = s
		}
	}
	ctx = context.WithValue(ctx, actorContextKey, actor)
	ctx = context.WithValue(ctx, requestIDContextKey, c.GetHeader("X-Request-ID"))
	ctx = context.WithValue(ctx, idempotencyContextKey, c.GetHeader("Idempotency-Key"))
	return ctx
}

// RequestMetadataFromContext extracts audit metadata previously injected
// by withRequestMetadata. It is used by the application-layer mutators to
// populate the audit log without depending on Gin types.
func RequestMetadataFromContext(ctx context.Context) (actor, requestID, idempotencyKey string) {
	if v, ok := ctx.Value(actorContextKey).(string); ok {
		actor = v
	}
	if v, ok := ctx.Value(requestIDContextKey).(string); ok {
		requestID = v
	}
	if v, ok := ctx.Value(idempotencyContextKey).(string); ok {
		idempotencyKey = v
	}
	return
}
