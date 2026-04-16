package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/stockdb"
)

func (h *ScriptPipelineHandler) AssociateStock(c *gin.Context) {
	var req AssociateStockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if h.stockDB == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Stock database not available"})
		return
	}

	// 1. Find Topic Folder for Prioritization
	var topicClips []stockdb.StockClipEntry
	if req.Topic != "" {
		// Strictly search in 'stock' section
		folder, _ := h.stockDB.FindFolderByTopicInSection(req.Topic, "stock")
		if folder != nil {
			topicClips, _ = h.stockDB.GetClipsForFolder(folder.DriveID)
		}
	}

	usedClipIDs := make(map[string]bool)
	var segmentData []SegmentStock
	var allClips []StockClip
	var driveAssocs []DriveFolderAssoc

	for _, seg := range req.Segments {
		var clips []StockClip
		initial, final := extractPhrases(seg.Text)

		// Collect search terms
		searchTerms := scriptdocs.ExtractKeywords(seg.Text)
		if len(searchTerms) == 0 {
			searchTerms = append(searchTerms, seg.Text)
		}

		// 1. Search in topic-specific clips first
		for _, clip := range topicClips {
			if len(clips) >= 3 {
				break
			}
			if usedClipIDs[clip.ClipID] {
				continue
			}
			
			matched := false
			for _, term := range searchTerms {
				termLower := strings.ToLower(term)
				// Check tags (clip.Tags is []string)
				for _, tag := range clip.Tags {
					if strings.Contains(strings.ToLower(tag), termLower) {
						matched = true
						break
					}
				}
				if matched {
					break
				}
				if strings.Contains(strings.ToLower(clip.Filename), termLower) {
					matched = true
					break
				}
			}
			
			if matched {
				clipRef := StockClip{
					ClipID:     clip.ClipID,
					Filename:   clip.Filename,
					FolderPath: clip.FolderID,
					DriveLink:  "https://drive.google.com/file/d/" + clip.ClipID + "/view",
					Confidence: 0.9,
				}
				clips = append(clips, clipRef)
				usedClipIDs[clip.ClipID] = true
			}
		}

		// 2. Global Stock Search if needed (STRICTLY STOCK SECTION)
		if len(clips) < 2 {
			results, _ := h.stockDB.SearchClipsByTagsInSection(searchTerms, "stock")
			for _, r := range results {
				if len(clips) >= 5 {
					break
				}
				if usedClipIDs[r.ClipID] {
					continue
				}
				clipRef := StockClip{
					ClipID:     r.ClipID,
					Filename:   r.Filename,
					FolderPath: r.FolderID,
					DriveLink:  "https://drive.google.com/file/d/" + r.ClipID + "/view",
					Confidence: 0.8,
				}
				clips = append(clips, clipRef)
				usedClipIDs[r.ClipID] = true
			}
		}

		// 3. Folder Search (Drive Clips)
		if h.clipIndexer != nil {
			folders := h.clipIndexer.SearchFolders(req.Topic)
			if len(folders) == 0 {
				for _, q := range searchTerms {
					folders = h.clipIndexer.SearchFolders(q)
					if len(folders) > 0 {
						break
					}
				}
			}
			if len(folders) > 0 {
				driveAssocs = append(driveAssocs, DriveFolderAssoc{
					Phrase:     seg.Text,
					FolderName: folders[0].Name,
					FolderURL:  "https://drive.google.com/drive/folders/" + folders[0].ID,
				})
			}
		}

		segmentData = append(segmentData, SegmentStock{
			SegmentIndex:  seg.Index,
			InitialPhrase: initial,
			FinalPhrase:   final,
			Clips:         clips,
		})
		allClips = append(allClips, clips...)
	}

	stockFolderURL := ""
	if h.driveClient != nil {
		stockFolderURL = "https://drive.google.com/drive/folders/" + h.stockRootFolder
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"segment_data":     segmentData,
		"all_clips":        allClips,
		"drive_assocs":     driveAssocs,
		"stock_folder":     "Local Stock (Prioritized)",
		"stock_folder_url": stockFolderURL,
	})
}

func (h *ScriptPipelineHandler) DownloadClips(c *gin.Context) {
	var req DownloadClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	var downloaded []string
	var failed []string

	for _, clip := range req.Clips {
		if clip.DriveLink != "" {
			downloaded = append(downloaded, clip.DriveLink)
		} else {
			failed = append(failed, clip.ClipID)
		}
	}

	for _, clip := range req.ArtlistClips {
		if clip.URL != "" {
			downloaded = append(downloaded, clip.URL)
		} else {
			failed = append(failed, clip.ClipID)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"downloaded":   downloaded,
		"failed":       failed,
		"download_url": "https://drive.google.com/drive/folders/" + h.stockRootFolder,
	})
}
