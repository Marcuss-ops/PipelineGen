package artlistpipeline

import (
	"fmt"
	"net/http"
	"sync"

	"velox/go-master/internal/artlistdb"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes registers all Artlist pipeline endpoints.
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	artlist := router.Group("/artlist")
	{
		// Preview: text → clip associations (no download)
		artlist.POST("/associate", h.HandleAssociate)
		// Full pipeline: text → sentences → search → download → convert → upload → video
		artlist.POST("/generate", h.HandleGenerate)
		// Step-by-step: download associated clips
		artlist.POST("/download", h.HandleDownload)
		// Batch download: download all missing clips for terms
		artlist.POST("/batch-download", h.HandleBatchDownload)
		// NEW: Unified endpoint - segments → expand queries → search → rank → return clips
		artlist.POST("/match-script", h.HandleMatchScript)
		// Status: pipeline health + stats
		artlist.GET("/status", h.HandleStatus)
		// Video stats: per-video cache effectiveness
		artlist.GET("/video-stats", h.HandleVideoStats)
		// Manual pre-warm trigger
		artlist.POST("/prewarm", h.HandlePreWarm)
	}
}

// HandleAssociate splits text into sentences and associates each with Artlist clips.
// @Summary Associate text with Artlist clips
// @Description Splits text into sentences, finds best concept per sentence, searches Artlist DB, returns associations
// @Tags Artlist
// @Accept json
// @Produce json
// @Param request body AssociateRequest true "Association request"
// @Success 200 {object} map[string]interface{}
// @Router /api/artlist/associate [post]
func (h *Handler) HandleAssociate(c *gin.Context) {
	var req AssociateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request: " + err.Error()})
		return
	}

	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "text is required"})
		return
	}

	maxClips := req.MaxClips
	if maxClips <= 0 {
		maxClips = 3
	}

	associations := h.associateSentencesWithClips(
		extractSentences(req.Text), maxClips)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"sentences":    len(associations),
		"associations": associations,
	})
}

// HandleGenerate runs the COMPLETE dynamic pipeline.
// @Summary Generate video from text
// @Description Full pipeline: split text, find best keyword per sentence, search Artlist, download NEW clips, convert 1920x1080/7s, upload to Drive, concatenate
// @Tags Artlist
// @Accept json
// @Produce json
// @Param request body GenerateFullVideoRequest true "Video generation request"
// @Success 200 {object} map[string]interface{}
// @Router /api/artlist/generate [post]
func (h *Handler) HandleGenerate(c *gin.Context) {
	var req GenerateFullVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request: " + err.Error()})
		return
	}

	topic := req.Topic
	if topic == "" {
		topic = "artlist_video"
	}

	outputName := req.OutputName
	if outputName == "" {
		outputName = sanitizeFilename(topic) + "_artlist"
	}

	maxClips := req.MaxClipsPerPhrase
	if maxClips <= 0 {
		maxClips = 1
	}

	parallel := req.ParallelDownloads
	if parallel <= 0 {
		parallel = 3
	}

	// 1. Extract sentences
	sentences := extractSentences(req.Text)
	if len(sentences) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "no meaningful sentences found"})
		return
	}

	// 2. Associate each sentence with clips
	associations := h.associateSentencesWithClips(sentences, maxClips)
	if len(associations) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "no clips could be associated for any sentence",
		})
		return
	}

	// 3. Download missing clips (with dedup)
	results := h.downloadClipsWithDedup(associations, outputName, parallel)

	// 4. Filter successful downloads
	var successful []DownloadResult
	var failedCount int
	for _, r := range results {
		if r.Err != nil {
			failedCount++
		} else {
			successful = append(successful, r)
		}
	}

	if len(successful) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":     false,
			"error":  "no clips could be downloaded",
			"failed": failedCount,
		})
		return
	}

	// 5. Build timestamps
	timestamps := buildTimestamps(successful)

	// 5b. Write meta.json for the generation
	h.writeGenerationMeta(outputName, associations, successful)

	// 6. Concatenate clips into final video
	outputPath, err := h.concatenateClips(extractPaths(successful), outputName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "failed to concatenate: " + err.Error(),
		})
		return
	}

	// 7. Upload final video to Drive
	driveID, driveURL := h.uploadFinalVideo(outputPath, outputName+".mp4")

	// 8. Save DB
	h.artlistDB.Save()

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"output_path":      outputPath,
		"drive_id":         driveID,
		"drive_url":        driveURL,
		"total_sentences":  len(sentences),
		"clips_downloaded": len(successful),
		"clips_failed":     failedCount,
		"timestamps":       timestamps,
		"associations":     associations,
		"db_stats":         h.artlistDB.GetStats(),
	})
}

// HandleDownload downloads clips from associations.
func (h *Handler) HandleDownload(c *gin.Context) {
	var req DownloadClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request"})
		return
	}

	topic := req.Topic
	if topic == "" {
		topic = "artlist_clips"
	}

	var uploaded []map[string]interface{}
	var failed []string

	for _, assoc := range req.Associations {
		if len(assoc.Clips) == 0 {
			continue
		}

		clip := assoc.Clips[0]
		if clip.URL == "" {
			failed = append(failed, "no URL for sentence")
			continue
		}

		result, err := h.downloadSingleClip(clip, assoc.ArtlistTerm, topic)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}

		h.artlistDB.MarkClipDownloaded(clip.ID, assoc.ArtlistTerm,
			result.DriveFileID, result.DriveURL, result.LocalPath)
		h.artlistDB.MarkClipUsedInVideo(clip.ID, topic)

		uploaded = append(uploaded, map[string]interface{}{
			"clip_id":    result.ClipID,
			"drive_url":  result.DriveURL,
			"local_path": result.LocalPath,
		})
	}

	h.artlistDB.Save()

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"uploaded": uploaded,
		"failed":   failed,
		"total":    len(uploaded),
	})
}

// HandleBatchDownload searches Artlist for terms (if not indexed), then downloads missing clips.
func (h *Handler) HandleBatchDownload(c *gin.Context) {
	var req BatchDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request"})
		return
	}

	terms := req.Terms
	if len(terms) == 0 {
		terms = h.artlistDB.GetAllTerms()
	}

	clipsPerTerm := req.ClipsPerTerm
	if clipsPerTerm <= 0 {
		clipsPerTerm = 10
	}

	parallel := req.Parallel
	if parallel <= 0 {
		parallel = 3
	}

	var (
		downloaded  int
		searched    int
		totalClips  int
		mu          sync.Mutex
		wg          sync.WaitGroup
		sem         = make(chan struct{}, parallel)
		results     []map[string]interface{}
	)

	for _, term := range terms {
		wg.Add(1)
		sem <- struct{}{}

		go func(term string) {
			defer wg.Done()
			defer func() { <-sem }()

			termResult := map[string]interface{}{
				"term":      term,
				"clips":    0,
				"searched": false,
				"error":    "",
			}

			// STEP 1: If term not in DB, search Artlist and index results
			if !h.artlistDB.HasSearchedTerm(term) {
				if h.artlistSrc == nil {
					termResult["error"] = "artlist source not available"
					mu.Lock()
					results = append(results, termResult)
					mu.Unlock()
					return
				}

				searchResults, err := h.artlistSrc.SearchClips(term, clipsPerTerm*3)
				if err != nil || len(searchResults) == 0 {
					termResult["error"] = fmt.Sprintf("no clips found on Artlist: %v", err)
					mu.Lock()
					results = append(results, termResult)
					mu.Unlock()
					return
				}

				// Convert to ArtlistClip and save to DB
				var artlistClips []artlistdb.ArtlistClip
				for _, sr := range searchResults {
					if _, alreadyDL := h.artlistDB.IsClipAlreadyDownloaded(sr.ID, sr.DownloadLink); alreadyDL {
						continue
					}

					artlistClips = append(artlistClips, artlistdb.ArtlistClip{
						ID:          sr.ID,
						VideoID:     sr.Filename,
						Title:       sr.Name,
						OriginalURL: sr.DownloadLink,
						URL:         sr.DownloadLink,
						Duration:    int(sr.Duration),
						Width:       sr.Width,
						Height:      sr.Height,
						Category:    sr.FolderPath,
						Tags:        sr.Tags,
					})
				}

				if len(artlistClips) > 0 {
					h.artlistDB.AddSearchResults(term, artlistClips)
					termResult["searched"] = true
					mu.Lock()
					searched++
					totalClips += len(artlistClips)
					mu.Unlock()
				}
			}

			// STEP 2: Download clips not yet downloaded
			clips, found := h.artlistDB.GetClipsForTerm(term)
			if !found {
				termResult["error"] = "no clips in DB"
				mu.Lock()
				results = append(results, termResult)
				mu.Unlock()
				return
			}

			var toDownload []artlistdb.ArtlistClip
			for _, clip := range clips {
				if !clip.Downloaded && len(toDownload) < clipsPerTerm {
					toDownload = append(toDownload, clip)
				}
			}

			for _, clip := range toDownload {
				result, err := h.downloadSingleClip(clip, term, term)
				if err != nil {
					continue
				}
				h.artlistDB.MarkClipDownloaded(clip.ID, term,
					result.DriveFileID, result.DriveURL, result.LocalPath)
				mu.Lock()
				downloaded++
				mu.Unlock()
			}

			termResult["clips"] = len(toDownload)
			mu.Lock()
			results = append(results, termResult)
			mu.Unlock()
		}(term)
	}

	wg.Wait()

	// Save DB
	h.artlistDB.Save()

	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"downloaded":     downloaded,
		"terms_searched": searched,
		"total_clips_found": totalClips,
		"terms_processed": len(terms),
		"results":        results,
		"db_stats":       h.artlistDB.GetStats(),
	})
}
