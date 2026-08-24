// Package operator — handler_bulk.go (RESOURCE: BULK OPERATIONS, July 2026).
//
// Provides a unified admin bulk-operation surface under
// /api/assets/operator/bulk. All mutations route through the canonical
// AssetMutationDispatcher so SQLite, outbox and Qdrant stay consistent.
package operator

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// bulkAction enumerates the supported bulk actions.
type bulkAction string

const (
	bulkActionAddTags     bulkAction = "add_tags"
	bulkActionRemoveTags  bulkAction = "remove_tags"
	bulkActionSetCategory bulkAction = "set_category"
	bulkActionSetReview   bulkAction = "set_review_status"
	bulkActionReindex     bulkAction = "reindex"
	bulkActionArchive     bulkAction = "archive"
	bulkActionVerify      bulkAction = "verify"
)

func (a bulkAction) valid() bool {
	switch a {
	case bulkActionAddTags, bulkActionRemoveTags, bulkActionSetCategory,
		bulkActionSetReview, bulkActionReindex, bulkActionArchive, bulkActionVerify:
		return true
	}
	return false
}

// bulkPayload is the request body for the /bulk endpoint.
type bulkPayload struct {
	AssetIDs []string       `json:"asset_ids" binding:"required,min=1,max=500"`
	Action   string         `json:"action" binding:"required"`
	DryRun   bool           `json:"dry_run"`
	Payload  map[string]any `json:"payload"`
}

// bulkChange holds the before/after snapshot for a single asset.
type bulkChange struct {
	AssetID string         `json:"asset_id"`
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Before  map[string]any `json:"before"`
	After   map[string]any `json:"after"`
}

// registerBulkRoutes mounts the bulk operations endpoint.
func (h *Handler) registerBulkRoutes(rg *gin.RouterGroup) {
	rg.POST("/bulk", h.handleBulk)
}

// handleBulk executes or previews a bulk operation.
//
// godlike/06 SSOT: when h.mutator is nil (composition root did
// not inject the canonical AssetMutationDispatcher) every action
// — including the read-only diagnostic bulkActionVerify — is
// refused with 503 so operators cannot accidentally rely on a
// partially-wired handler.
func (h *Handler) handleBulk(c *gin.Context) {
	if h.mutator == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "bulk operations unavailable: AssetMutationDispatcher not wired")
		return
	}

	var req bulkPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	action := bulkAction(req.Action)
	if !action.valid() {
		apiutil.BadRequest(c, fmt.Sprintf("unsupported action: %s", req.Action))
		return
	}

	ctx := c.Request.Context()
	changes := make([]bulkChange, 0, len(req.AssetIDs))
	for _, id := range req.AssetIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		change := h.applyBulk(ctx, id, action, req.Payload, req.DryRun)
		changes = append(changes, change)
	}

	affected, failed := 0, []string{}
	for _, ch := range changes {
		if ch.Status == "success" {
			affected++
		} else {
			failed = append(failed, ch.AssetID)
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"action":     req.Action,
		"dry_run":    req.DryRun,
		"affected":   affected,
		"failed":     len(failed),
		"failed_ids": failed,
		"changes":    changes,
	})
}

func (h *Handler) applyBulk(ctx context.Context, id string, action bulkAction, payload map[string]any, dryRun bool) bulkChange {
	details, err := h.assetService.Get(ctx, id)
	if err != nil {
		return bulkChange{AssetID: id, Status: "error", Message: err.Error()}
	}
	if details == nil || details.Asset == nil {
		return bulkChange{AssetID: id, Status: "error", Message: "asset not found"}
	}

	a := details.Asset

	switch action {
	case bulkActionAddTags:
		tags, ok := stringSlice(payload, "tags")
		if !ok || len(tags) == 0 {
			return bulkChange{AssetID: id, Status: "error", Message: "missing tags"}
		}
		newTags := union(a.ManualTags, tags)
		before := snapshot(a)
		if dryRun {
			before["tags"] = a.Tags
			return bulkChange{AssetID: id, Status: "success", Before: before, After: withTags(before, newTags)}
		}
		a.ManualTags = newTags
		a.RebuildTags()
		return h.reindexAsset(ctx, a, before, withTags(before, a.Tags))

	case bulkActionRemoveTags:
		tags, ok := stringSlice(payload, "tags")
		if !ok || len(tags) == 0 {
			return bulkChange{AssetID: id, Status: "error", Message: "missing tags"}
		}
		newTags := difference(a.ManualTags, tags)
		before := snapshot(a)
		if dryRun {
			return bulkChange{AssetID: id, Status: "success", Before: before, After: withTags(before, newTags)}
		}
		a.ManualTags = newTags
		a.RebuildTags()
		return h.reindexAsset(ctx, a, before, withTags(before, a.Tags))

	case bulkActionSetCategory:
		category, ok := stringValue(payload, "category")
		if !ok {
			return bulkChange{AssetID: id, Status: "error", Message: "missing category"}
		}
		before := snapshot(a)
		after := cloneMap(before)
		after["category"] = category
		if dryRun {
			return bulkChange{AssetID: id, Status: "success", Before: before, After: after}
		}
		a.Category = category
		return h.reindexAsset(ctx, a, before, after)

	case bulkActionSetReview:
		rs, ok := stringValue(payload, "review_status")
		if !ok {
			return bulkChange{AssetID: id, Status: "error", Message: "missing review_status"}
		}
		review := asset.ReviewStatus(rs)
		if !review.Valid() {
			return bulkChange{AssetID: id, Status: "error", Message: fmt.Sprintf("invalid review_status: %s", rs)}
		}
		before := snapshot(a)
		after := cloneMap(before)
		after["review_status"] = rs
		if dryRun {
			return bulkChange{AssetID: id, Status: "success", Before: before, After: after}
		}
		a.ReviewStatus = review
		return h.reindexAsset(ctx, a, before, after)

	case bulkActionReindex:
		before := snapshot(a)
		after := cloneMap(before)
		after["queued"] = true
		if dryRun {
			return bulkChange{AssetID: id, Status: "success", Before: before, After: after}
		}
		return h.reindexAsset(ctx, a, before, after)

	case bulkActionArchive:
		before := snapshot(a)
		after := cloneMap(before)
		after["lifecycle_state"] = string(asset.StateDeleteRequested)
		if dryRun {
			return bulkChange{AssetID: id, Status: "success", Before: before, After: after}
		}
		if err := h.mutator.EnqueueAndDelete(ctx, a.ID); err != nil {
			return bulkChange{AssetID: id, Status: "error", Message: err.Error(), Before: before, After: after}
		}
		return bulkChange{AssetID: id, Status: "success", Before: before, After: after}

	case bulkActionVerify:
		before := snapshot(a)
		after := cloneMap(before)
		// Verify is a read-only diagnostic action: ensure the asset
		// has a local location and the file is still on disk.
		message := "asset verified"
		loc := details.LocalLocation()
		if loc == nil || loc.URI == "" {
			message = "asset has no local location"
		} else {
			if _, err := os.Stat(loc.URI); os.IsNotExist(err) {
				message = "local file missing"
			}
		}
		if a.LegacyFileMD5() == "" {
			message = "asset has no legacy_file_md5"
		}
		return bulkChange{AssetID: id, Status: "success", Message: message, Before: before, After: after}

	default:
		return bulkChange{AssetID: id, Status: "error", Message: fmt.Sprintf("unsupported action: %s", action)}
	}
}

// reindexAsset updates the asset row and enqueues an outbox reindex event.
func (h *Handler) reindexAsset(ctx context.Context, a *asset.Asset, before, after map[string]any) bulkChange {
	contentHash := a.LegacyFileMD5()
	if contentHash == "" {
		contentHash = a.ID
	}
	if h.committer == nil {
		return bulkChange{AssetID: a.ID, Status: "error", Message: "committer not wired", Before: before, After: after}
	}
	if _, err := h.committer.CommitAndIndex(ctx, persistence.AssetCommitRequest{
		AssetID: a.ID, Source: string(a.Source), Name: a.Name, Filename: a.Filename,
		MediaType: string(a.MediaType), ContentHash: contentHash, LifecycleState: string(a.LifecycleState),
		IndexState: a.GetMetadataString("index_state"), EmitIndexEvent: true,
	}); err != nil {
		return bulkChange{AssetID: a.ID, Status: "error", Message: err.Error(), Before: before, After: after}
	}
	return bulkChange{AssetID: a.ID, Status: "success", Before: before, After: after}
}

func snapshot(a *asset.Asset) map[string]any {
	tags := make([]string, len(a.Tags))
	copy(tags, a.Tags)
	return map[string]any{
		"tags":            tags,
		"category":        a.Category,
		"review_status":   string(a.ReviewStatus),
		"lifecycle_state": string(a.LifecycleState),
	}
}

func withTags(m map[string]any, tags []string) map[string]any {
	out := cloneMap(m)
	out["tags"] = tags
	return out
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func stringSlice(payload map[string]any, key string) ([]string, bool) {
	v, ok := payload[key]
	if !ok {
		return nil, false
	}
	switch vv := v.(type) {
	case []string:
		return vv, true
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}

func stringValue(payload map[string]any, key string) (string, bool) {
	v, ok := payload[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func union(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		seen[s] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func difference(a, b []string) []string {
	remove := make(map[string]bool, len(b))
	for _, s := range b {
		remove[s] = true
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if !remove[s] {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}
