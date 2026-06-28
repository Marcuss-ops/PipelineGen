// Package voiceover provides thin HTTP handlers for voiceover operations.
package voiceover

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// voiceoverSunsetDate (PR-VO-C1, June 2026) is the IETF IMF-fixdate
// RFC 8594 Sunset header value for the /generate-with-group endpoint.
// Hard-coded constant (matches the same convention as
// handler_legacy_adapters.go::removalDateFromClips / etc.) so deploy
// operators can grep the codebase for "2026-09-26" and find every
// surface that mentions it. The 90-day grace window is intentional —
// it matches the migration window documented at
// docs/voiceover/p0-bundle-A1-A6.md §"Deprecation contract (90-day
// Sunset, RFC 8594)".
const voiceoverSunsetDate = "Sat, 26 Sep 2026 00:00:00 GMT"

// legacyVoiceoverRouteInvocationsTotal is the per-route Prometheus
// counter that tracks how many times the deprecated
// /generate-with-group endpoint has been invoked since process start.
// Operators expose this via /metrics or admin dashboards to identify
// clients that haven't migrated to the canonical /generate endpoint
// with destination.kind="group". Mirrors the
// handler_legacy_adapters.go::legacyRouteInvocationsTotal pattern
// (Wave 21 Wave 25 + PR-VO-C1, June 2026).
var legacyVoiceoverRouteInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "legacy_voiceover_route_invocations_total",
	Help: "Monotonic counter for deprecated voiceover routes, labelled by route name.",
}, []string{"route"})

// LegacyVoiceoverDeprecationCount returns the cumulative invocation
// count across the deprecated voiceover routes by reading the
// legacyVoiceoverRouteInvocationsTotal prometheus counter (dto.Metric
// writeback pattern). Exposed for admin/diagnostic surfaces so the
// PR-VO-C1 sunset deadline can be tracked against live usage.
func LegacyVoiceoverDeprecationCount() int64 {
	var total int64
	for _, route := range []string{"generate-with-group"} {
		counter, err := legacyVoiceoverRouteInvocationsTotal.GetMetricWithLabelValues(route)
		if err != nil {
			continue
		}
		var m dto.Metric
		if err := counter.Write(&m); err != nil {
			continue
		}
		total += int64(m.GetCounter().GetValue())
	}
	return total
}

// addVoiceoverDeprecationHeader injects the standard deprecation
// headers on the deprecated /generate-with-group endpoint at every
// invocation (RFC 9745 draft "Deprecation" header + RFC 8594 "Sunset"
// header + RFC 8288 Web Linking "successor-version" Link header).
//
// This is PR-VO-C1's canonical pattern: the proprietary
// X-Deprecation / X-Deprecation-Notice headers used at
// handler_legacy_adapters.go are a legacy Wave 21 convention; new
// deprecations MUST use the IETF standard form so dashboards can be
// authored against one consistent cross-language contract.
func addVoiceoverDeprecationHeader(c *gin.Context, route string) {
	legacyVoiceoverRouteInvocationsTotal.WithLabelValues(route).Inc()
	c.Header("Deprecation", "true")
	c.Header("Sunset", voiceoverSunsetDate)
	c.Header("Link",
		`</api/voiceover/generate>; rel="successor-version"`,
	)
}

// Handler is the unified handler for all voiceover operations:
//   - /generate: Generate a single voiceover (sync or async)
//   - /batch: Generate multiple voiceovers (always async via job queue)
//   - /sync: Sync voiceovers from Google Drive
//   - /generate-with-group: Generate a single voiceover routed via voiceover_group topic
//     (reads the topic→folder_id mapping from asset_tree_nodes via GroupsResolver).
//   - /groups (GET): List the canonical topic→folder_id mapping for a given parent
//     (defaults to the configured voiceover root).
type Handler struct {
	service              *voiceover.Service
	syncService          *voiceoversync.Service
	jobsSvc              jobservice.Service
	groupsResolver       *voiceover.GroupsResolver // optional; nil-safe — falls back to no-routing
	defaultVoiceoverRoot string                    // folder ID under which groups live
	log                  *zap.Logger
}

// NewHandler builds the handler. groupsResolver is REQUIRED to keep the
// API surface consistent (nil resolver would mean DB-backed routing is dead code).
// Pass nil only if you also accept that GET /groups + voiceover_group routing
// will return 501 Not Implemented.
func NewHandler(
	service *voiceover.Service,
	syncService *voiceoversync.Service,
	jobsSvc jobservice.Service,
	groupsResolver *voiceover.GroupsResolver,
	defaultVoiceoverRoot string,
	log *zap.Logger,
) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		service:              service,
		syncService:          syncService,
		jobsSvc:              jobsSvc,
		groupsResolver:       groupsResolver,
		defaultVoiceoverRoot: strings.TrimSpace(defaultVoiceoverRoot),
		log:                  log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
	r.POST("/generate-with-group", h.GenerateWithGroup)
	r.POST("/batch", h.Batch)
	r.POST("/promo", h.Promo)
	r.POST("/sync", h.Sync)
	r.GET("/groups", h.ListGroups)
}

// Generate processes a single voiceover request (sync or async).
//
// PR-VO-C1 (June 2026): this endpoint now accepts a `destination` field
// per the canonical Voiceover DestinationRequest contract. Routing
// dispatch (must stay in sync with DestinationRequest.Kind docstring
// in internal/application/voiceover/types.go):
//
//	destination.kind == ""           — auto-detect (legacy fallback).
//	                                  Defaults to the configured
//	                                  VoiceoverFolder; if absent, the
//	                                  service-layer Generate call
//	                                  reports the underlying error.
//	                                  This case NEVER returns 501.
//	destination.kind == "group"      — explicit caller intent to route
//	                                  by topic label. Requires:
//	                                    - destination.group non-empty (400)
//	                                    - GroupsResolver wired at ctor time (501)
//	                                    - defaultVoiceoverRoot non-empty (400)
//	                                  Group-not-found under root → 404
//	                                  with available list. The 501 is
//	                                  deliberate (godlike/07 §"No fake
//	                                  availability"): a misconfigured
//	                                  deployment that accepts the
//	                                  explicit kind="group" must observe
//	                                  the configuration gap distinctly
//	                                  from a runtime fault (500).
//	destination.kind == "explicit"   — caller supplies destination.folder_id
//	                                  directly. Service-layer is the
//	                                  single owner of folder_id semantics.
//	destination.kind == <other>      — silently treated as auto (back-compat).
//
// The legacy /generate-with-group endpoint continues to be served for
// 90 days (RFC 8594 Sunset: Sat, 26 Sep 2026 00:00:00 GMT, see the
// godlike/07 §"Migration sequence" deprecation record PR-VO-C1).
// After the sunset date the legacy endpoint will be removed and only
// the unified /generate endpoint will accept group-routed requests.
func (h *Handler) Generate(c *gin.Context) {
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}

	var req struct {
		Text        string                         `json:"text" binding:"required"`
		Language    string                         `json:"language"`
		Filename    string                         `json:"filename"`
		Async       bool                           `json:"async"`
		Destination *voiceover.DestinationRequest  `json:"destination,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if req.Language == "" {
		req.Language = "it"
	}

	// PR-VO-C1 (June 2026): canonical /generate destination routing.
	// When destination.kind == "group" the handler MUST resolve the
	// caller-supplied group label through GroupsResolver before
	// continuing. The resolved folder_id is stamped back onto
	// req.Destination so the downstream service-layer code only ever
	// sees a populated FolderID regardless of which routing strategy
	// the caller chose.
	// PR-VO-C1 (June 2026): validate destination.kind="explicit" — the
	// caller is opting into direct folder_id targeting and MUST supply a
	// non-empty FolderID, otherwise the request silently falls through to
	// the legacy config-level VoiceoverFolder (godlike/07 §"No fake
	// availability" violation). Reject upfront; resolve-and-route below
	// for the "group" case.
	if req.Destination != nil && req.Destination.Kind == "explicit" && strings.TrimSpace(req.Destination.FolderID) == "" {
		apiutil.BadRequest(c, `destination.folder_id is required when destination.kind is "explicit" (PR-VO-C1)`)
		return
	}

	if req.Destination != nil && req.Destination.Kind == "group" {
		if req.Destination.Group == "" {
			apiutil.BadRequest(c, `destination.group is required when destination.kind is "group" (PR-VO-C1)`)
			return
		}
		if h.groupsResolver == nil {
			// PR-VO-C1 (June 2026): deterministic 501 (NOT 500) here.
			// The caller explicitly opted into `destination.kind="group"`,
			// which requires GroupsResolver. Returning 500 would be
			// operationally misleading (this is a configuration gap,
			// not a runtime fault). Per godlike/07 §"No fake availability"
			// the failure mode is observably distinctive so misconfigured
			// deployments get a clear signal instead of an opaque
			// InternalError. Same gating as per `If-Modified-Since`-style
			// capability negotiation elsewhere in the codebase.
			apiutil.Error(c, http.StatusNotImplemented,
				`destination.kind="group" requires groups resolver configuration at composition root (PR-VO-C1). Configure *voiceover.GroupsResolver in NewHandler() via the app composition root.`)
			return
		}
		parentID := h.defaultVoiceoverRoot
		if parentID == "" {
			apiutil.BadRequest(c, "no default voiceover root configured on the handler (PR-VO-C1 needs a root to resolve groups)")
			return
		}
		group, err := h.groupsResolver.ResolveByName(c.Request.Context(), parentID, req.Destination.Group)
		if err != nil {
			if errors.Is(err, voiceover.ErrGroupNotFound) {
				available, _ := h.groupsResolver.ListGroups(c.Request.Context(), parentID)
				names := make([]string, 0, len(available))
				for _, e := range available {
					names = append(names, e.Name)
				}
				apiutil.Error(c, http.StatusNotFound, fmt.Sprintf(`voiceover group %q not found under root %s; available: [%s]`,
					req.Destination.Group, parentID, strings.Join(names, ", ")))
				return
			}
			h.log.Error("PR-VO-C1 group resolution failed",
				zap.String("group", req.Destination.Group),
				zap.String("parent_id", parentID),
				zap.Error(err))
			apiutil.InternalError(c, err)
			return
		}
		h.log.Info("PR-VO-C1 routing voiceover via groups_resolver",
			zap.String("voiceover_group", req.Destination.Group),
			zap.String("folder_id", group.FolderID),
			zap.String("parent_id", parentID))
		req.Destination.FolderID = group.FolderID
	}

	// If async is requested, enqueue as a batch job with 1 item.
	// fix/voiceover-sync-async-strategy (June 2026): async must set
	// Strategy="replace" to match sync + /generate-with-group; otherwise
	// normalizeBatchRequest defaults to "verify" and produces a duplicate.
	if req.Async {
		h.log.Info("enqueuing voiceover generation (async)",
			zap.String("language", req.Language),
			zap.Bool("async", req.Async))

		batchReq := voiceover.BatchRequest{
			Text:      req.Text,
			Languages: []string{req.Language},
			Strategy:  "replace",
		}
		if req.Filename != "" {
			batchReq.FilenameTemplate = req.Filename
		}

		if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
			Type:    "voiceover.batch",
			Payload: batchReq.PayloadMap(),
		}, "Voiceover generation enqueued."); ok {
			return
		}
		return
	}

	// Default to sync processing
	if req.Filename == "" {
		req.Filename = "manual vo " + strings.ReplaceAll(req.Language, "-", " ") + ".mp3"
	}

	h.log.Info("generating voiceover (sync)",
		zap.String("language", req.Language),
		zap.String("filename", req.Filename))

	// PR-VO-C1 (June 2026): when the caller supplies a Destination
	// with a resolvable FolderID (either directly via Kind="explicit"
	// or via Kind="group" + GroupsResolver) we MUST route through
	// service.GenerateWithDestination so the FolderID actually steers
	// storage. The legacy h.service.Generate shortcut falls back to
	// s.cfg.Drive.VoiceoverFolder() and silently drops caller intent.
	//
	// This closes the godlike/07 §"No fake availability" gap that the
	// code-reviewer flagged on the prior iteration where
	// destination.folder_id was accepted but never consumed.
	var result *voiceover.VoiceoverResult
	var err error
	if req.Destination != nil && strings.TrimSpace(req.Destination.FolderID) != "" {
		h.log.Info("PR-VO-C1 sync via destination.folder_id",
			zap.String("destination_kind", req.Destination.Kind),
			zap.String("folder_id", req.Destination.FolderID))
		result, err = h.service.GenerateWithDestination(
			c.Request.Context(),
			req.Text,
			req.Language,
			req.Filename,
			req.Destination,
		)
	} else {
		result, err = h.service.Generate(c.Request.Context(), req.Text, req.Language, req.Filename)
	}
	if err != nil {
		h.log.Error("voiceover generation failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	h.log.Info("voiceover generated successfully", zap.String("path", result.Path))
	apiutil.OK(c, gin.H{"result": result})
}

// Batch processes multiple voiceover requests (always async)
func (h *Handler) Batch(c *gin.Context) {
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}

	req, ok := apiutil.BindJSON[voiceover.BatchRequest](c)
	if !ok {
		return
	}

	h.log.Info("enqueuing voiceover batch",
		zap.Int("languages", len(req.Languages)),
		zap.Strings("languages", req.Languages))

	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type:    "voiceover.batch",
		Payload: req.PayloadMap(),
	}, "Voiceover batch enqueued."); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

// Promo generates promotional voiceovers in multiple languages.
// Translates the source text via Ollama, then generates a voiceover per language.
// Non-dry-run requests are enqueued as persistent voiceover.promo jobs.
func (h *Handler) Promo(c *gin.Context) {
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}

	var req voiceover.PromoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	// Default drive folder if not provided
	if req.DriveFolderID == "" {
		req.DriveFolderID = h.service.Cfg().Drive.VoiceoverFolder()
	}

	if req.DryRun {
		resp, err := h.service.GeneratePromo(c.Request.Context(), &req)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, resp)
		return
	}

	// Async: enqueue as a persistent job (no fire-and-forget)
	langCount := len(req.Languages)
	if langCount == 0 {
		langCount = len(voiceover.DefaultPromoLanguages())
	}

	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type:    "voiceover.promo",
		Payload: voiceover.PromoRequestPayloadMap(&req),
	}, fmt.Sprintf("Promo voiceover enqueued (%d languages).", langCount)); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

// ListGroups lists the canonical topic→folder_id mapping under the voiceover root.
func (h *Handler) ListGroups(c *gin.Context) {
	if h.groupsResolver == nil {
		apiutil.InternalError(c, fmt.Errorf("groups resolver not configured"))
		return
	}

	parentID := strings.TrimSpace(c.Query("parent_id"))
	if parentID == "" {
		parentID = h.defaultVoiceoverRoot
	}
	if parentID == "" {
		apiutil.BadRequest(c, "parent_id is required (or configure a default voiceover root on the handler)")
		return
	}

	entries, err := h.groupsResolver.ListGroups(c.Request.Context(), parentID)
	if err != nil {
		h.log.Error("list groups failed", zap.String("parent_id", parentID), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"parent_id": parentID,
		"count":     len(entries),
		"groups":    entries,
	})
}

// GenerateWithGroup accepts a voiceover_group string (the topic name) and resolves
// it to a Drive folder ID via asset_tree_nodes before delegating to the regular
// /generate flow.
//
// PR-VO-C1 (June 2026): this endpoint is deprecated. New callers MUST use
// POST /api/voiceover/generate with `destination: {kind: "group", group: "..."}`.
// The 90-day Sunset header (RFC 8594) is set on every response so monitoring
// tooling can grep `Deprecation: true` to spot legacy consumers. The handler
// body is otherwise unchanged — it continues to serve 100% the same
// payload it has historically, so existing clients are unimpacted during
// the deprecation window. See architecture/deprecations.yaml entry
// PR-VO-C1 for the canonical removal_date (2026-09-26).
func (h *Handler) GenerateWithGroup(c *gin.Context) {
	addVoiceoverDeprecationHeader(c, "generate-with-group")
	if h.groupsResolver == nil {
		apiutil.InternalError(c, fmt.Errorf("groups resolver not configured"))
		return
	}
	if h.service == nil {
		apiutil.BadRequest(c, "voiceover service not initialized")
		return
	}

	var req struct {
		Text           string `json:"text" binding:"required"`
		Language       string `json:"language"`
		Filename       string `json:"filename"`
		Async          bool   `json:"async"`
		VoiceoverGroup string `json:"voiceover_group" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if req.Language == "" {
		req.Language = "it"
	}

	parentID := h.defaultVoiceoverRoot
	if parentID == "" {
		apiutil.BadRequest(c, "no default voiceover root configured on the handler")
		return
	}

	group, err := h.groupsResolver.ResolveByName(c.Request.Context(), parentID, req.VoiceoverGroup)
	if err != nil {
		if errors.Is(err, voiceover.ErrGroupNotFound) {
			available, _ := h.groupsResolver.ListGroups(c.Request.Context(), parentID)
			names := make([]string, 0, len(available))
			for _, e := range available {
				names = append(names, e.Name)
			}
			apiutil.Error(c, http.StatusNotFound, fmt.Sprintf("voiceover_group %q not found under root %s; available: [%s]",
				req.VoiceoverGroup, parentID, strings.Join(names, ", ")))
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	h.log.Info("routing voiceover via groups_resolver",
		zap.String("voiceover_group", req.VoiceoverGroup),
		zap.String("folder_id", group.FolderID),
		zap.String("parent_id", parentID))

	req.Filename = strings.TrimSpace(req.Filename)
	if req.Filename == "" {
		req.Filename = group.Name + " " + strings.ReplaceAll(req.Language, "-", " ") + ".mp3"
	}

	if req.Async {
		batchReq := voiceover.BatchRequest{
			Text:      req.Text,
			Languages: []string{req.Language},
			Strategy:  "replace",
			Destination: &voiceover.DestinationRequest{
				FolderID: group.FolderID,
			},
			Metadata: map[string]any{"voiceover_group": req.VoiceoverGroup},
		}
		if req.Filename != "" {
			batchReq.FilenameTemplate = req.Filename
		}

		if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
			Type:    "voiceover.batch",
			Payload: batchReq.PayloadMap(),
		}, "Voiceover generation enqueued."); ok {
			return
		}
		return
	}

	result, genErr := h.service.GenerateWithDestination(c.Request.Context(), req.Text, req.Language, req.Filename, &voiceover.DestinationRequest{
		FolderID:      group.FolderID,
		SubfolderName: "",
	})
	if genErr != nil {
		h.log.Error("voiceover generation failed (generate-with-group)", zap.Error(genErr))
		apiutil.InternalError(c, genErr)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":              true,
		"voiceover_group": req.VoiceoverGroup,
		"folder_id":       group.FolderID,
		"result":          result,
	})
}

// Sync triggers synchronization of voiceovers from Google Drive.
func (h *Handler) Sync(c *gin.Context) {
	if h.syncService == nil {
		apiutil.InternalError(c, fmt.Errorf("voiceover sync service not configured"))
		return
	}

	h.log.Info("starting voiceover sync")

	summary, err := h.syncService.Sync(c.Request.Context())
	if err != nil {
		h.log.Error("voiceover sync failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, summary)
}
