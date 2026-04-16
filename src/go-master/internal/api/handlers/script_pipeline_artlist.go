package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/scriptdocs"
)

func (h *ScriptPipelineHandler) AssociateArtlist(c *gin.Context) {
	var req AssociateArtlistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if h.artlistDB == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Artlist database not available"})
		return
	}

	usedClipIDs := make(map[string]bool)
	var segmentData []SegmentArtlistRef
	var allClips []ArtlistClipRef

	for _, seg := range req.Segments {
		var clips []ArtlistClipRef

		// Collect search terms from segment text and entities
		searchTerms := scriptdocs.ExtractKeywords(seg.Text)
		if len(searchTerms) == 0 {
			searchTerms = append(searchTerms, seg.Text)
		}

		// 1. Try local ArtlistDB ONLY (Pure source)
		results, err := h.artlistDB.FindDownloadedClipsWithSimilarTags(searchTerms, 1)
		if err == nil && len(results) > 0 {
			for _, r := range results {
				if len(clips) >= 3 {
					break
				}
				if usedClipIDs[r.URL] {
					continue
				}
				clips = append(clips, ArtlistClipRef{
					ClipID:    r.URL,
					Name:      r.Name,
					Term:      seg.Text,
					URL:       r.DriveURL,
					Folder:    r.FolderID,
					Timestamp: fmt.Sprintf("%d-%d sec", seg.StartTime, seg.EndTime),
				})
				usedClipIDs[r.URL] = true
			}
		}

		// 2. Fallback to ArtlistIndex (Stock/Artlist folders)
		if len(clips) == 0 && h.artlistIndex != nil {
			idxResults := h.artlistIndex.Search(searchTerms, 2)
			for _, r := range idxResults {
				if len(clips) >= 3 {
					break
				}
				if usedClipIDs[r.URL] {
					continue
				}
				clips = append(clips, ArtlistClipRef{
					ClipID:    r.URL,
					Name:      r.Name,
					Term:      seg.Text,
					URL:       r.URL,
					Folder:    "Stock/Artlist (Indexed)",
					Timestamp: fmt.Sprintf("%d-%d sec", seg.StartTime, seg.EndTime),
				})
				usedClipIDs[r.URL] = true
			}
		}

		segmentData = append(segmentData, SegmentArtlistRef{
			SegmentIndex: seg.Index,
			Clips:        clips,
			SearchTerms:  searchTerms,
		})
		allClips = append(allClips, clips...)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"segment_data": segmentData,
		"all_clips":    allClips,
	})
}
