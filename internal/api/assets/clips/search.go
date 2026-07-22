// Package clips — Search sub-handler (Step 5 Split 1, June 2026).
//
// OVERRIDE ADR 0009 (clips.Handler capability-split) — user override
// recorded in commit message; this commit extracts the 4 search routes
// (ListClips + GetClip + ClipStatus + AdvancedSearch) into a dedicated
// *SearchHandler receiver. SearchDeps carries only the 5 deps these
// routes actually consume (cluster × deps matrix §4):
//
//   - ClipsRepo (ListClips text-search branch via repoForSource)
//   - AssetRepo      (GetClip / ClipStatus / ListClips default branch)
//   - VoiceoverRepo  (GetClip + ListClips voiceover source branch; nil-tolerated)
//   - ImagesRepo     (ListClips images source branch; nil-tolerated)
//   - SearchSvc      (AdvancedSearch via canonical search.Aggregator)
//
// Pattern B (per-cluster RegisterRoutes with idem fn as parameter):
// the orchestrator Handler.RegisterRoutes single-calls
// sh.RegisterRoutes(r, h.idemWriter()). Search routes are co-located
// here so cluster reads + writes are atomic in one file. Read routes
// (GET) install no idem; write routes (POST) install idem before the
// handler per AGENTS.md Pattern 8.
package clips

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// SearchDeps is the constructor bag for SearchHandler. The 5 fields
// below are exactly the deps the 7 moved methods touch — no more, no
// less. Cluster ownership follows the matrix in the Step 5 discovery
// report (June 2026, §4 Search cluster).
type SearchDeps struct {
	ClipsRepo     *assets.ClipsRepository
	AssetRepo     asset.Repository
	VoiceoverRepo *assets.VoiceoversRepository
	ImagesRepo    *imagesrepo.ImagesRepository
	SearchSvc     *search.Aggregator
}

// SearchHandler owns the 4 clip-search routes. Receiver-on-pattern-B:
// constructed in NewHandler from a SearchDeps shape extracted from
// the orchestrator Deps.
type SearchHandler struct {
	clipsRepo     *assets.ClipsRepository
	assetRepo     asset.Repository
	voiceoverRepo *assets.VoiceoversRepository
	imagesRepo    *imagesrepo.ImagesRepository
	searchSvc     *search.Aggregator
}

// NewSearchHandler constructs a SearchHandler with the supplied
// SearchDeps. Nil fields are tolerated for test fixtures; production
// wiring supplies all 5 via the SearchDeps shape.
func NewSearchHandler(d SearchDeps) *SearchHandler {
	return &SearchHandler{
		clipsRepo:     d.ClipsRepo,
		assetRepo:     d.AssetRepo,
		voiceoverRepo: d.VoiceoverRepo,
		imagesRepo:    d.ImagesRepo,
		searchSvc:     d.SearchSvc,
	}
}

// repoForSource resolves a clip source to its canonical repository
// via the shared ClipsRepository. All clip-type sources share the same
// concrete repo in production. Returns nil for voiceover/images.
// Authoritative implementation; orchestrator *Handler.repoForSource delegates here.
func (sh *SearchHandler) repoForSource(source string) *assets.ClipsRepository {
	if sh.clipsRepo == nil {
		return nil
	}
	if !artifacts.IsClipsSource(source) {
		return nil
	}
	return sh.clipsRepo
}

// RegisterRoutes installs the 4 Search routes on the supplied gin
// router group. Read routes install no idem middleware; write routes
// install it before the handler per AGENTS.md Pattern 8.
//
// Route table:
//
//	GET  /:source/clips                  -> ListClips       (read)
//	GET  /:source/clips/:id              -> GetClip         (read)
//	POST /:source/clips/:id/status       -> ClipStatus      (write+idem)
//	POST /search/advanced                -> AdvancedSearch  (write+idem)
func (sh *SearchHandler) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {
	// Read-only routes (no idempotency)
	r.GET("/:source/clips", sh.ListClips)
	r.GET("/:source/clips/:id", sh.GetClip)

	// Write routes (idempotency-protected per PR8, June 2026)
	r.POST("/:source/clips/:id/status", idem, sh.ClipStatus)
	// POST /search/advanced removed — Blocco A2 consolidation (June 2026).
	// Unified search is now at POST /api/media/search.
}

// ─── MOVED FROM clip_search.go (deleted in this commit) ───

// AdvancedSearch performs a multi-source clip search with structured filters.
//
//	@Summary		Advanced clip search with filters
//	@Description	Search media assets with structured filters (category, date range,
//	@Description	duration, transcript, source, Drive link).
//	@Tags			search
//	@Accept			json
//	@Produce		json
//	@Success		200  {object} object
//	@Header			200  {string}  X-Deprecation    "true (Wave 21 PR 10 cutover)"
//	@Router			/api/media/search/advanced [post]
func (sh *SearchHandler) AdvancedSearch(c *gin.Context) {
	if sh.searchSvc == nil {
		sh.setDeprecationHeader(c)
		apiutil.InternalError(c, fmt.Errorf("advanced search aggregator not available"))
		return
	}

	var req asset.AdvancedSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		sh.setDeprecationHeader(c)
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	q := translateAdvanceRequestToQuery(req)
	res, err := sh.searchSvc.Search(c.Request.Context(), q)
	if err != nil {
		sh.setDeprecationHeader(c)
		apiutil.InternalError(c, err)
		return
	}

	sh.setDeprecationHeader(c)
	apiutil.OK(c, gin.H{
		"ok":     true,
		"total":  len(res.Items),
		"count":  len(res.Items),
		"limit":  req.Limit,
		"offset": req.Offset,
		"clips":  toAssetResults(res),
	})
}

// setDeprecationHeader installs the Wave 21 PR 10 cutover sentinel
// header on every response from this route. Dashboards and migration
// tooling grep for X-Deprecation: true to find legacy consumers.
func (sh *SearchHandler) setDeprecationHeader(c *gin.Context) {
	c.Header("X-Deprecation", "true")
	c.Header("X-Deprecation-Migration", "aggregator")
	c.Header("Link", `</api/v2/search>; rel="successor-version"`)
}

// translateAdvanceRequestToQuery converts the legacy
// asset.AdvancedSearchRequest into the canonical search.Query the
// Aggregator consumes. Kept as a package-local function so the
// Wave 19 cross-capability import rule is preserved (search package
// is stdlib-only; the api layer owns the bridge).
func translateAdvanceRequestToQuery(req asset.AdvancedSearchRequest) search.Query {
	limit := req.Limit
	if limit <= 0 {
		limit = 50 // legacy default
	}
	var mediaTypes []string
	if req.Source != "" && req.Source != "all" && req.Source != "unified" {
		mediaTypes = []string{req.Source}
	}
	return search.Query{
		Text:  req.Q,
		Limit: limit,
		Mode:  search.SearchModeHybrid,
		Filters: search.Filters{
			Source:        req.Source,
			MediaType:     req.Source,
			Tags:          nil, // legacy AdvancedSearchRequest does not expose Tags
			DurationMsMin: req.MinDuration * 1000,
		},
		MediaTypes: mediaTypes,
	}
}

// toAssetResults converts the Aggregator's canonical search.Result
// back into []*asset.Asset for the legacy envelope shape
// ({ok,total,count,limit,offset,clips}). Uses typed-string conversions
// because asset.Asset's Source/MediaType fields are typed names
// (`type Source string`, `type MediaType string`) per the asset
// domain — not plain strings.
func toAssetResults(r *search.Result) []*asset.Asset {
	if r == nil {
		return nil
	}
	out := make([]*asset.Asset, 0, len(r.Items))
	for _, c := range r.Items {
		out = append(out, &asset.Asset{
			ID:        c.AssetID,
			Name:      c.Title,
			Source:    asset.Source(c.Source),
			MediaType: asset.MediaType(c.MediaType),
		})
	}
	return out
}

// ─── MOVED FROM clip_read.go (deleted in this commit) ───

// GetClip returns a single clip.
func (sh *SearchHandler) GetClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	// Handle Voiceover source — use canonical converter directly.
	if strings.ToLower(source) == "voiceover" && sh.voiceoverRepo != nil {
		rec, err := sh.voiceoverRepo.GetByID(c.Request.Context(), clipID)
		if err != nil {
			apiutil.NotFound(c, "voiceover not found")
			return
		}
		clip := artifacts.VoiceoverRecordToClip(rec)
		apiutil.OK(c, gin.H{"ok": true, "source": source, "clip": clip})
		return
	}

	if sh.assetRepo == nil {
		apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
		return
	}

	clip, err := sh.assetRepo.Get(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}
	if clip == nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"clip":   clip,
	})
}

// ClipStatus returns the status of a clip.
func (sh *SearchHandler) ClipStatus(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if sh.assetRepo == nil {
		apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
		return
	}
	clip, err := sh.assetRepo.Get(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}
	if clip == nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Determine status based on available data
	status := "unknown"
	if clip.DriveLink() != "" || clip.DownloadLink() != "" {
		status = "processed"
	} else if clip.LocalPath() != "" {
		status = "downloaded"
	} else {
		status = "pending"
	}

	apiutil.OK(c, gin.H{
		"ok":             true,
		"source":         source,
		"clip_id":        clipID,
		"exists_db":      true,
		"name":           clip.Name,
		"has_local_file": clip.LocalPath() != "",
		"local_path":     clip.LocalPath(),
		"has_drive_link": clip.DriveLink() != "" || clip.DownloadLink() != "",
		"drive_link":     clip.DriveLink(),
		"download_link":  clip.DownloadLink(),
		"file_hash":      clip.FileHash(),
		"folder_id":      clip.FolderID(),
		"folder_path":    clip.FolderPath(),
		"status":         status,
	})
}

// ListClips lists all clips for a source with pagination and search.
func (sh *SearchHandler) ListClips(c *gin.Context) {
	source := c.Param("source")
	sourceLower := strings.ToLower(source)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	q := c.Query("q")

	ctx := c.Request.Context()
	var allClips []*asset.Asset

	if sourceLower == "voiceover" {
		if sh.voiceoverRepo == nil {
			apiutil.BadRequest(c, "voiceover repo not available")
			return
		}
		records, err := sh.voiceoverRepo.ListAll(ctx)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		for _, rec := range records {
			allClips = append(allClips, artifacts.VoiceoverRecordToClip(rec))
		}
	} else if sourceLower == "images" {
		if sh.imagesRepo == nil {
			apiutil.BadRequest(c, "images repo not available")
			return
		}
		images, err := sh.imagesRepo.ListAll(ctx)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		for _, img := range images {
			allClips = append(allClips, artifacts.ImageAssetToClip(img))
		}
	} else {
		if sh.assetRepo == nil {
			apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
			return
		}
		if q == "" {
			// No search — use canonical List with pagination.
			clips, err := sh.assetRepo.List(ctx, asset.Filter{
				Source: source,
				Limit:  limit,
				Offset: offset,
			})
			if err != nil {
				apiutil.InternalError(c, err)
				return
			}
			allClips = clips
		} else {
			// Text search — fall back to legacy clipsRepo (asset.Filter has no search yet).
			repo := sh.repoForSource(source)
			if repo == nil {
				apiutil.BadRequest(c, "invalid source: "+source)
				return
			}
			legacyClips, err := repo.ListClipsPaged(ctx, source, limit, offset, q)
			if err != nil {
				apiutil.InternalError(c, err)
				return
			}
			allClips = make([]*asset.Asset, len(legacyClips))
			for i, lc := range legacyClips {
				allClips[i] = lc
			}
		}
	}

	total := 0
	if sourceLower == "voiceover" || sourceLower == "images" {
		total = len(allClips)
		if offset >= len(allClips) {
			allClips = []*asset.Asset{}
		} else {
			end := offset + limit
			if end > len(allClips) {
				end = len(allClips)
			}
			allClips = allClips[offset:end]
		}
	} else {
		repo := sh.repoForSource(source)
		if repo != nil {
			if q == "" {
				n, err := sh.assetRepo.Count(ctx, asset.Filter{Source: source})
				if err == nil {
					total = int(n)
				}
			} else {
				// For search, total is len of results for now (since SearchClips isn't paged yet)
				total = len(allClips)
			}
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"count":  len(allClips),
		"total":  total,
		"clips":  allClips,
	})
}
