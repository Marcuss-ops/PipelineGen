// Package clips — folder query sub-handler (Fase 2 split, June 2026).
//
// Extracted from ops.go: read-only folder operations.
// Depends on: ClipsRepository (folder queries), AssetTreeSvc (tree/breadcrumb).
package clips

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strconv"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"

	"net/http"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// repoForSource resolves a clip source to its canonical repository.
// All clip-type sources share the same concrete repo in production.
func (oh *OpsHandler) repoForSource(source string) appclips.ClipRepositoryPort {
	if oh.clipsRepo == nil {
		return nil
	}
	if !artifacts.IsClipsSource(source) {
		return nil
	}
	return oh.clipsRepo
}

// ListFolders lists all folders for a source.
func (oh *OpsHandler) ListFolders(c *gin.Context) {
	if oh.clipsRepo == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clips query port not wired")
		return
	}

	source := c.Param("source")

	if oh.clipsRepo == nil {
		apiutil.Error(c, 503, "clips repository not wired")
		return
	}

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	folders, err := repo.ListFolders(c.Request.Context(), "")
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	if limit > 0 && limit < len(folders) {
		folders = folders[:limit]
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"count":   len(folders),
		"folders": folders,
	})
}

// FolderStatus returns the status of a folder.
func (oh *OpsHandler) FolderStatus(c *gin.Context) {
	if oh.clipsRepo == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clips query port not wired")
		return
	}

	source := c.Param("source")
	folderID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	folder, err := repo.GetFolder(ctx, folderID)
	if err != nil {
		folders, err2 := repo.ListFolders(ctx, "")
		if err2 != nil {
			apiutil.InternalError(c, err2)
			return
		}
		found := false
		for _, f := range folders {
			if f.FolderID == folderID {
				folder = f
				found = true
				break
			}
		}
		if !found {
			apiutil.NotFound(c, "folder not found")
			return
		}
	}

	clipList, _ := repo.ListByFolderID(ctx, folder.FolderID)
	if len(clipList) == 0 {
		clipList, _ = repo.ListByFolderPath(ctx, folder.FolderPath)
	}

	stats := detail.ClipFolderStats{}
	for _, clip := range clipList {
		stats.ClipCount++
		if clip.DriveLink() != "" || clip.DownloadLink() != "" {
			stats.ProcessedCount++
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"folder":     folder,
		"stats":      stats,
		"clip_count": len(clipList),
	})
}

// GetFolderChildren returns the children of a specific folder.
func (oh *OpsHandler) GetFolderChildren(c *gin.Context) {
	if oh.clipsRepo == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clips query port not wired")
		return
	}

	source := c.Param("source")
	folderID := c.Param("id")

	if folderID == "root" {
		folderID = ""
	}

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var children []*asset.AssetNode
	var err error
	var clipChildren []*asset.Asset
	var clipErr error

	if repo != nil {
		clipChildren, clipErr = repo.ListByFolderID(ctx, folderID)
		if clipErr == nil {
			for _, clip := range clipChildren {
				children = append(children, appclips.TreeNodeToAssetNode(appclips.ClipToAssetNode(clip)))
			}
			if len(children) > 0 {
				if limit > 0 && limit < len(children) {
					children = children[:limit]
				}
				if offset > 0 {
					if offset >= len(children) {
						children = []*asset.AssetNode{}
					} else {
						children = children[offset:]
					}
				}
				apiutil.OK(c, gin.H{
					"ok":       true,
					"source":   source,
					"count":    len(children),
					"children": children,
				})
				return
			}
		} else {
			err = clipErr
		}
	}

	if oh.assetTreeSvc != nil {
		treeNodes, treeErr := oh.assetTreeSvc.ListChildrenPaged(ctx, source, folderID, limit, offset)
		if treeErr == nil {
			for _, tn := range treeNodes {
				children = append(children, appclips.TreeNodeToAssetNode(tn))
			}
			apiutil.OK(c, gin.H{
				"ok":       true,
				"source":   source,
				"count":    len(children),
				"children": children,
			})
			return
		}
		err = treeErr
	}

	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"source":   source,
		"count":    len(children),
		"children": children,
	})
}

// GetTree returns the direct children of a given parent folder.
func (oh *OpsHandler) GetTree(c *gin.Context) {
	if oh.clipsRepo == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clips query port not wired")
		return
	}

	source := c.Param("source")
	parentID := c.Query("parent_id")

	if parentID == "root" {
		parentID = ""
	}

	if oh.assetTreeSvc == nil {
		apiutil.InternalError(c, nil)
		return
	}

	treeNodes, err := oh.assetTreeSvc.ListChildren(c.Request.Context(), source, parentID)
	if err != nil {
		oh.log.Error("failed to list children", zap.Error(err), zap.String("source", source), zap.String("parent_id", parentID))
		apiutil.InternalError(c, err)
		return
	}

	var children []*asset.AssetNode
	for _, tn := range treeNodes {
		children = append(children, appclips.TreeNodeToAssetNode(tn))
	}

	if len(children) == 0 {
		if repo := oh.repoForSource(source); repo != nil {
			clipChildren, clipErr := repo.GetFolderChildren(c.Request.Context(), parentID)
			if clipErr == nil {
				for _, clip := range clipChildren {
					children = append(children, appclips.TreeNodeToAssetNode(appclips.ClipToAssetNode(clip)))
				}
			}
		}
	}
	apiutil.OK(c, gin.H{
		"ok":       true,
		"source":   source,
		"children": children,
	})
}

// GetBreadcrumb returns the path from root down to the specified node ID.
func (oh *OpsHandler) GetBreadcrumb(c *gin.Context) {
	if oh.clipsRepo == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clips query port not wired")
		return
	}

	source := c.Param("source")
	id := c.Query("id")

	if id == "" {
		apiutil.BadRequest(c, "missing id parameter")
		return
	}

	if oh.assetTreeSvc == nil {
		apiutil.InternalError(c, nil)
		return
	}

	breadcrumb, err := oh.assetTreeSvc.GetBreadcrumb(c.Request.Context(), id)
	if err != nil {
		oh.log.Error("failed to get breadcrumb", zap.Error(err), zap.String("source", source), zap.String("id", id))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"breadcrumb": breadcrumb,
	})
}
