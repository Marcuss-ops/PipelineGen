package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/stockdb"
)

func (h *ScriptPipelineHandler) CreateDocument(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if h.docClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Docs client not initialized"})
		return
	}

	topic := req.Topic
	if topic == "" {
		topic = req.Title
	}

	content := h.BuildDocumentContent(
		req.Title,
		topic,
		req.Duration,
		req.Language,
		req.Script,
		req.Segments,
		req.ArtlistAssocs,
		req.StockFolderURL, // If URL is passed as ID/URL
		req.StockFolder,
		req.DriveAssocs,
		req.FrasiImportanti,
		req.NomiSpeciali,
		req.ParoleImportanti,
		req.EntitaConImmagine,
		req.Translations,
	)

	doc, err := h.docClient.CreateDoc(c.Request.Context(), req.Title, content, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"doc_id":  doc.ID,
		"doc_url": doc.URL,
	})
}

func (h *ScriptPipelineHandler) GenerateFullPipeline(c *gin.Context) {
	var req FullPipelineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	reqContext := c.Request.Context()

	text := req.Text
	if text == "" {
		genReq := &ollama.TextGenerationRequest{
			SourceText: req.Topic,
			Title:      req.Topic,
			Language:   req.Language,
			Duration:   req.Duration,
			Tone:       "professional",
			Model:      "gemma3:4b",
		}
		result, err := h.generator.GenerateFromText(reqContext, genReq)
		if err != nil {
			text = req.Topic + " è un personaggio importante con una storia incredibile."
		} else {
			text = result.Script
		}
	}

	sentences := scriptdocs.ExtractSentences(text)
	var segments []Segment
	avgDuration := 20
	for i, sentence := range sentences {
		segments = append(segments, Segment{
			Index:     i,
			Text:      sentence,
			StartTime: i * avgDuration,
			EndTime:   (i + 1) * avgDuration,
		})
	}

	// --- NEW: ENTITY EXTRACTION LOGIC ---
	var allEntities []string
	seenEntity := make(map[string]bool)
	var frasiImportanti []string
	var nomiSpecialiAll []string
	var paroleImportantiAll []string
	var entitaConImmagine []EntityImage

	for _, seg := range segments {
		if len(seg.Text) > 20 {
			frasiImportanti = append(frasiImportanti, seg.Text)
		}
		nomi := scriptdocs.ExtractProperNouns([]string{seg.Text})
		parole := scriptdocs.ExtractKeywords(seg.Text)

		for _, n := range nomi {
			lower := strings.ToLower(n)
			if !seenEntity[lower] && len(n) > 2 {
				seenEntity[lower] = true
				allEntities = append(allEntities, n)
				nomiSpecialiAll = append(nomiSpecialiAll, n)
			}
		}
		for _, p := range parole {
			lower := strings.ToLower(p)
			if !seenEntity[lower] && len(p) > 2 {
				seenEntity[lower] = true
				allEntities = append(allEntities, p)
				paroleImportantiAll = append(paroleImportantiAll, p)
			}
		}
	}

	allSentences := make([]string, 0)
	for _, seg := range segments {
		allSentences = append(allSentences, seg.Text)
	}
	entityImagesMap := scriptdocs.ExtractEntitiesWithImages(allSentences)
	for entity, imageURL := range entityImagesMap {
		if imageURL != "" {
			entitaConImmagine = append(entitaConImmagine, EntityImage{
				Entity:   entity,
				ImageURL: imageURL,
			})
		}
	}

	// --- END ENTITY EXTRACTION ---

	// 1. Find Topic Folder for Prioritization
	var topicFolderID string
	var topicClips []stockdb.StockClipEntry
	if h.stockDB != nil && req.Topic != "" {
		folder, _ := h.stockDB.FindFolderByTopicInSection(req.Topic, "stock")
		if folder != nil {
			topicFolderID = folder.DriveID
			topicClips, _ = h.stockDB.GetClipsForFolder(topicFolderID)
		}
	}

	usedClipIDs := make(map[string]bool)
	var allStockClips []StockClip
	var stockAssocs []StockAssoc
	var driveAssocs []DriveFolderAssoc
	var artlistAssocs []ArtlistAssoc
	var allArtlistClips []ArtlistClipRef

	for _, seg := range segments {
		searchTerms := scriptdocs.ExtractKeywords(seg.Text)
		if len(searchTerms) == 0 {
			searchTerms = append(searchTerms, seg.Text)
		}
		
		// Add topic keywords for better matching in topic-specific folder
		topicKeywords := strings.Fields(strings.ToLower(req.Topic))
		searchTerms = append(searchTerms, topicKeywords...)
		searchTerms = append(searchTerms, strings.ToLower(req.Topic))

		// --- A. STOCK DRIVE (Prioritized Search - STOCK ONLY) ---
		var segmentStockClips []StockClip
		initial, final := extractPhrases(seg.Text)

		if h.stockDB != nil {
			// 1. Search in topic-specific clips first
			for _, clip := range topicClips {
				if len(segmentStockClips) >= 3 {
					break
				}
				if usedClipIDs[clip.ClipID] {
					continue
				}
				
				// EXCLUDE "Copy of" clips
				if strings.Contains(strings.ToLower(clip.Filename), "copy of") {
					continue
				}

				matched := false
				for _, term := range searchTerms {
					// Check in tags
					for _, clipTag := range clip.Tags {
						if strings.Contains(strings.ToLower(clipTag), strings.ToLower(term)) {
							matched = true
							break
						}
					}
					if matched {
						break
					}
					// Check in filename
					if strings.Contains(strings.ToLower(clip.Filename), strings.ToLower(term)) {
						matched = true
						break
					}
				}
				
				if matched {
					clipRef := StockClip{
						ClipID:    clip.ClipID,
						Filename:  clip.Filename,
						FolderPath: clip.FolderID,
						DriveLink: "https://drive.google.com/file/d/" + clip.ClipID + "/view",
					}
					segmentStockClips = append(segmentStockClips, clipRef)
					usedClipIDs[clip.ClipID] = true
				}
			}
			
			// FALLBACK: If no keyword matches in topic folder, take some clips anyway 
			// because they are in the SPECIFIC topic folder requested.
			if len(segmentStockClips) == 0 && len(topicClips) > 0 {
				count := 0
				for _, clip := range topicClips {
					if count >= 3 {
						break
					}
					if usedClipIDs[clip.ClipID] {
						continue
					}
					// EXCLUDE "Copy of" clips
					if strings.Contains(strings.ToLower(clip.Filename), "copy of") {
						continue
					}
					clipRef := StockClip{
						ClipID:    clip.ClipID,
						Filename:  clip.Filename,
						FolderPath: clip.FolderID,
						DriveLink: "https://drive.google.com/file/d/" + clip.ClipID + "/view",
					}
					segmentStockClips = append(segmentStockClips, clipRef)
					usedClipIDs[clip.ClipID] = true
					count++
				}
			}

			// 2. Global Stock Search if needed (Strictly 'stock' section)
			if len(segmentStockClips) < 2 {
				results, _ := h.stockDB.SearchClipsByTagsInSection(searchTerms, "stock")
				for _, r := range results {
					if len(segmentStockClips) >= 5 {
						break
					}
					if usedClipIDs[r.ClipID] {
						continue
					}

					// EXCLUDE "Copy of" clips and generic "video_..." folders
					if strings.Contains(strings.ToLower(r.Filename), "copy of") || 
					   strings.Contains(r.FolderID, "video_") {
						continue
					}

					clipRef := StockClip{
						ClipID:    r.ClipID,
						Filename:  r.Filename,
						FolderPath: r.FolderID,
						DriveLink: "https://drive.google.com/file/d/" + r.ClipID + "/view",
					}
					segmentStockClips = append(segmentStockClips, clipRef)
					usedClipIDs[r.ClipID] = true
				}
			}
		}

		if len(segmentStockClips) > 0 {
			stockAssocs = append(stockAssocs, StockAssoc{
				Phrase:        seg.Text,
				InitialPhrase: initial,
				FinalPhrase:   final,
				Clips:         segmentStockClips,
			})
			allStockClips = append(allStockClips, segmentStockClips...)
		}

		// --- B. DRIVE CLIPS (Folders) ---
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

		// --- C. ARTLIST (Pure Source) ---
		var segmentArtlistClips []ArtlistClipRef
		if h.artlistDB != nil {
			results, _ := h.artlistDB.FindDownloadedClipsWithSimilarTags(searchTerms, 1)
			for _, r := range results {
				if len(segmentArtlistClips) >= 3 {
					break
				}
				if usedClipIDs[r.URL] {
					continue
				}
				clipRef := ArtlistClipRef{
					ClipID: r.URL,
					Name:   r.Name,
					Term:   seg.Text,
					URL:    r.DriveURL,
					Folder: r.FolderID,
				}
				segmentArtlistClips = append(segmentArtlistClips, clipRef)
				allArtlistClips = append(allArtlistClips, clipRef)
				usedClipIDs[r.URL] = true
			}
		}
		
		// Fallback to ArtlistIndex (Stock/Artlist folders)
		if len(segmentArtlistClips) == 0 {
			if h.artlistIndex != nil {
				idxResults := h.artlistIndex.Search(searchTerms, 2)
				for _, r := range idxResults {
					if len(segmentArtlistClips) >= 3 {
						break
					}
					if usedClipIDs[r.URL] {
						continue
					}
					clipRef := ArtlistClipRef{
						ClipID: r.URL,
						Name:   r.Name,
						Term:   seg.Text,
						URL:    r.URL,
						Folder: "Stock/Artlist (Indexed)",
					}
					segmentArtlistClips = append(segmentArtlistClips, clipRef)
					allArtlistClips = append(allArtlistClips, clipRef)
					usedClipIDs[r.URL] = true
				}
			}
		}

		if len(segmentArtlistClips) > 0 {
			artlistAssocs = append(artlistAssocs, ArtlistAssoc{
				Phrase: seg.Text,
				Clips:  segmentArtlistClips,
			})
		}
	}

	content := h.BuildDocumentContent(
		req.Topic,
		req.Topic,
		req.Duration,
		req.Language,
		text,
		segments,
		artlistAssocs,
		topicFolderID,
		req.Topic,
		driveAssocs,
		frasiImportanti,
		nomiSpecialiAll,
		paroleImportantiAll,
		entitaConImmagine,
		nil, // No translations in full pipeline yet
	)

	doc, err := h.docClient.CreateDoc(reqContext, req.Topic, content, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":                  true,
		"doc_url":             doc.URL,
		"segments_count":      len(segments),
		"stock_clips_found":   len(allStockClips),
		"artlist_clips_found": len(allArtlistClips),
		"entities_found":      len(allEntities),
	})
}
