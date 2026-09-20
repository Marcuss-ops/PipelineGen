// Package clips — Search sub-handler (Step 5 Split 1, June 2026).
//
// OVERRIDE ADR 0009 (clips.Handler capability-split) — user override
// recorded in commit message; this commit extracts the 3 search routes
// (ListClips + GetClip + ClipStatus) into a dedicated *SearchHandler
// receiver. SearchDeps carries only the 4 deps these
// routes actually consume (cluster × deps matrix §4):
//
//   - ClipsRepo (ListClips text-search branch via repoForSource)
//   - AssetRepo      (GetClip / ClipStatus / ListClips default branch)
//   - VoiceoverRepo  (GetClip + ListClips voiceover source branch; nil-tolerated)
//   - ImagesRepo     (ListClips images source branch; nil-tolerated)
//
// Pattern B (per-cluster RegisterRoutes with idem fn as parameter):
// the canonical catalog sub-descriptor calls
// sh.RegisterRoutes(r, idem). Search routes are co-located
// here so cluster reads + writes are atomic in one file. Read routes
// (GET) install no idem; write routes (POST) install idem before the
// handler per AGENTS.md Pattern 8.
package clips

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// AssetReader is the consumer-owned read contract for the clips API handlers:
// asset by id, filtered listing and count.
//
// It is an interface rather than the concrete SQLite detail.Repository
// type-switch bridge because PostgreSQL is the media SSOT: the production
// concrete is the PostgreSQL media read store, and the SQLite adapter is
// selected only in the documented media-disabled degrade mode.
type AssetReader interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error)
	Count(ctx context.Context, filter asset.Filter) (int64, error)
}

// MediaClipSearcher is the consumer-owned read contract for the ListClips text
// search branch (`GET /:source/clips?q=...`).
//
// P2-9 (September 2026): PostgreSQL + pgvector is the sole authority for the
// media domain, so this branch MUST resolve against the PostgreSQL media read
// authority (pgmedia.MediaSearcher.SearchLocal). It previously reached
// AssetStoreSQLite.SearchClips, whose fast path queried the operational
// clip_search_terms inverted index and re-hydrated the operational SQLite
// media_assets mirror — which the canonical PostgreSQL committer never
// populates. The handler could therefore only ever grade a stale pre-cutover
// catalog. The port is narrow on purpose: ids + ranking stay canonical.
type MediaClipSearcher interface {
	// SearchClipsByTerms returns the clips of source matching EVERY term
	// (the canonical AND semantics), ranked by the canonical scorer and
	// capped at limit.
	SearchClipsByTerms(ctx context.Context, source string, terms []string, limit int) ([]*asset.Asset, error)
}

// SearchDeps is the constructor bag for SearchHandler. The 4 fields
// below are exactly the deps the 3 routes touch — no more, no
// less. Cluster ownership follows the matrix in the Step 5 discovery
// report (June 2026, §4 Search cluster).
type SearchDeps struct {
	ClipsRepo     appclips.ClipRepositoryPort
	AssetRepo     AssetReader
	VoiceoverRepo appclips.VoiceoverRepositoryPort
	ImagesRepo    appclips.ImageRepositoryPort
	// MediaSearch answers the ListClips text-search branch from the media
	// SSOT. Nil means the media plane is closed: the branch fails closed
	// rather than degrading onto the retired operational index.
	MediaSearch MediaClipSearcher
}

// SearchHandler owns the 3 clip-search routes. Receiver-on-pattern-B:
// constructed in NewHandler from a SearchDeps shape extracted from
// the orchestrator Deps.
type SearchHandler struct {
	clipsRepo     appclips.ClipRepositoryPort
	assetRepo     AssetReader
	voiceoverRepo appclips.VoiceoverRepositoryPort
	imagesRepo    appclips.ImageRepositoryPort
	mediaSearch   MediaClipSearcher
}

// NewSearchHandler constructs a SearchHandler with the supplied
// SearchDeps. Nil fields are tolerated for test fixtures.
func NewSearchHandler(d SearchDeps) *SearchHandler {
	return &SearchHandler{
		clipsRepo:     d.ClipsRepo,
		assetRepo:     d.AssetRepo,
		voiceoverRepo: d.VoiceoverRepo,
		imagesRepo:    d.ImagesRepo,
		mediaSearch:   d.MediaSearch,
	}
}

// repoForSource resolves a clip source to its canonical repository
// via the shared ClipsRepository. All clip-type sources share the same
// concrete repo in production. Returns nil for voiceover/images.
// Authoritative implementation; orchestrator *Handler.repoForSource delegates here.

// RegisterRoutes installs the 3 Search routes on the supplied gin
// router group. Read routes install no idem middleware; write routes
// install it before the handler per AGENTS.md Pattern 8.
//
// Route table:
//
//	GET  /:source/clips                  -> ListClips       (read)
//	GET  /:source/clips/:id              -> GetClip         (read)
//	POST /:source/clips/:id/status       -> ClipStatus      (write+idem)
func (sh *SearchHandler) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {
	// Read-only routes (no idempotency)
	r.GET("/:source/clips", sh.ListClips)
	r.GET("/:source/clips/:id", sh.GetClip)

	// Write routes (idempotency-protected per PR8, June 2026)
	r.POST("/:source/clips/:id/status", idem, sh.ClipStatus)
}

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
		clip := voiceoverDTOToClip(rec)
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
		"ok":              true,
		"source":          source,
		"clip_id":         clipID,
		"exists_db":       true,
		"name":            clip.Name,
		"has_local_file":  clip.LocalPath() != "",
		"local_path":      clip.LocalPath(),
		"has_drive_link":  clip.DriveLink() != "" || clip.DownloadLink() != "",
		"drive_link":      clip.DriveLink(),
		"download_link":   clip.DownloadLink(),
		"legacy_file_md5": clip.LegacyFileMD5(),
		"folder_id":       clip.FolderID(),
		"folder_path":     clip.FolderPath(),
		"status":          status,
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
			allClips = append(allClips, voiceoverDTOToClip(rec))
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
			allClips = append(allClips, appclips.ImageAssetToAsset(img))
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
			// Text search — resolved against the canonical media read authority
			// (P2-9). The legacy clipsRepo.ListClipsPaged branch read the
			// operational clip_search_terms inverted index and re-hydrated the
			// SQLite media_assets mirror, so a clip committed by the canonical
			// PostgreSQL committer was invisible here. Fail closed when the
			// media plane is closed instead of degrading onto that index.
			if !artifacts.IsClipsSource(source) {
				apiutil.BadRequest(c, "invalid source: "+source)
				return
			}
			if sh.mediaSearch == nil {
				apiutil.InternalError(c, fmt.Errorf("clips search unavailable: PostgreSQL media SSOT is not wired"))
				return
			}
			terms := strings.Fields(q)
			if len(terms) == 0 {
				terms = []string{q}
			}
			clips, err := sh.mediaSearch.SearchClipsByTerms(ctx, source, terms, limit)
			if err != nil {
				apiutil.InternalError(c, err)
				return
			}
			allClips = clips
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
		// The search branch is answered by the media SSOT and is not paged
		// upstream, so its result set IS the total. Only the unfiltered branch
		// asks assetRepo for a canonical count. The retired clipsRepo gate is
		// gone with the retired clip_search_terms read path.
		if q == "" {
			if n, err := sh.assetRepo.Count(ctx, asset.Filter{Source: source}); err == nil {
				total = int(n)
			}
		} else {
			total = len(allClips)
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

// voiceoverDTOToClip converts a ClipVoiceoverRecordDTO to an *asset.Asset.
// Replacement for artifacts.VoiceoverRecordToClip when the source is a
// ClipVoiceoverRecordDTO (from the VoiceoverRepositoryPort) instead of the
// concrete *assets.Record.
func voiceoverDTOToClip(rec *appclips.ClipVoiceoverRecordDTO) *asset.Asset {
	if rec == nil {
		return nil
	}
	name := rec.Filename
	if name == "" {
		name = rec.TextPreview
		if len(name) > 50 {
			name = name[:50]
		}
	}
	createdAt, _ := time.Parse(time.RFC3339, rec.CreatedAtRFC)
	updatedAt, _ := time.Parse(time.RFC3339, rec.UpdatedAtRFC)
	clip := &asset.Asset{
		ID:          rec.ID,
		Name:        name,
		Filename:    rec.Filename,
		Source:      "voiceover",
		MediaType:   "audio",
		SearchTerms: []string{rec.TextPreview},
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
	clip.SetFolderID(rec.FolderID)
	clip.SetFolderPath(rec.FolderPath)
	clip.SetDriveLink(rec.DriveLink)
	clip.SetDriveFileID(rec.DriveFileID)
	clip.SetDownloadLink(rec.DownloadLink)
	clip.SetLegacyFileMD5(rec.LegacyFileMD5)
	clip.SetLocalPath(rec.LocalPath)
	clip.SetMetadataJSON(rec.Metadata)
	return clip
}
