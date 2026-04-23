package script

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/clip"
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

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *ScriptPipelineHandler) CreateDocumentPreview(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	req.PreviewOnly = true

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *ScriptPipelineHandler) CreateDocumentFromSource(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.SourceText) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "source_text is required"})
		return
	}
	if strings.TrimSpace(req.Script) == "" {
		req.Script = req.SourceText
	}

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *ScriptPipelineHandler) ReviewDraft(c *gin.Context) {
	var req ReviewDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	draft := CreateDocumentRequest{
		Title:       req.Title,
		Topic:       req.Topic,
		SourceText:  req.SourceText,
		Script:      req.SourceText,
		Language:    req.Language,
		Duration:    req.Duration,
		PreviewOnly: true,
	}
	topic := draft.Topic
	if strings.TrimSpace(topic) == "" {
		topic = draft.Title
	}
	h.enrichCreateDocumentRequest(c.Request.Context(), &draft, topic)

	c.JSON(http.StatusOK, ReviewDraftResponse{
		Ok:      true,
		Draft:   draft,
		Message: "Review the draft, edit it locally, then POST it to /script-pipeline/create-doc",
	})
}

func normalizeDriveFolderID(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "drive.google.com/drive/folders/") {
		parts := strings.Split(v, "/folders/")
		if len(parts) > 1 {
			id := parts[1]
			if i := strings.Index(id, "?"); i >= 0 {
				id = id[:i]
			}
			if i := strings.Index(id, "/"); i >= 0 {
				id = id[:i]
			}
			return strings.TrimSpace(id)
		}
	}
	return v
}

func (h *ScriptPipelineHandler) resolveStockFolderForDocument(topic string) (folderID, folderName string) {
	if h.stockDB == nil || strings.TrimSpace(topic) == "" {
		return "", ""
	}

	tryFolder := func(folder *stockdb.StockFolderEntry) (string, string, bool) {
		if folder == nil || strings.TrimSpace(folder.DriveID) == "" {
			return "", "", false
		}
		name := strings.TrimSpace(folder.FullPath)
		if name == "" {
			name = strings.TrimSpace(folder.TopicSlug)
		}
		return folder.DriveID, name, true
	}

	if folder, _ := h.stockDB.FindFolderByTopicInSection(topic, "stock"); folder != nil {
		if id, name, ok := tryFolder(folder); ok {
			return id, name
		}
	}
	if folder, _ := h.stockDB.FindFolderByTopic(topic); folder != nil {
		if id, name, ok := tryFolder(folder); ok {
			return id, name
		}
	}

	tokens := make([]string, 0, 4)
	for _, raw := range strings.FieldsFunc(strings.ToLower(topic), func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '/' || r == ':' || r == ',' || r == '.'
	}) {
		token := strings.TrimSpace(raw)
		if len(token) < 3 {
			continue
		}
		tokens = append(tokens, token)
	}

	if len(tokens) == 0 {
		return "", ""
	}

	if folders, err := h.stockDB.GetFoldersBySection("stock"); err == nil {
		for _, folder := range folders {
			candidate := strings.ToLower(folder.FullPath + " " + folder.TopicSlug)
			for _, token := range tokens {
				if strings.Contains(candidate, token) {
					if id, name, ok := tryFolder(&folder); ok {
						return id, name
					}
				}
			}
		}
	}

	return "", ""
}

func (h *ScriptPipelineHandler) createDocumentFromRequest(ctx context.Context, req *CreateDocumentRequest) (*CreateDocumentResponse, error) {
	h.normalizeCreateDocumentRequest(req)

	topic := req.Topic
	if topic == "" {
		topic = req.Title
	}

	h.enrichCreateDocumentRequest(ctx, req, topic)

	stockFolderID := normalizeDriveFolderID(req.StockFolderURL)
	scriptBody := req.Script
	if strings.TrimSpace(scriptBody) == "" {
		scriptBody = req.SourceText
	}
	content := h.BuildDocumentContent(
		req.Title,
		topic,
		req.Duration,
		req.Language,
		scriptBody,
		req.Segments,
		req.ArtlistAssocs,
		stockFolderID,
		req.StockFolder,
		req.DriveAssocs,
		req.FrasiImportanti,
		req.NomiSpeciali,
		req.ParoleImportanti,
		req.EntitaConImmagine,
		req.Translations,
	)

	if req.PreviewOnly {
		previewPath, err := savePreviewDocument(req.Title, content)
		if err != nil {
			return nil, err
		}
		return &CreateDocumentResponse{
			Ok:          true,
			DocID:       "local_file",
			DocURL:      previewPath,
			PreviewPath: previewPath,
			Mode:        "preview",
		}, nil
	}

	if h.docClient == nil {
		return nil, fmt.Errorf("Docs client not initialized")
	}

	publishCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	doc, err := h.docClient.CreateDoc(publishCtx, req.Title, content, "")
	if err != nil {
		return nil, err
	}

	return &CreateDocumentResponse{
		Ok:     true,
		DocID:  doc.ID,
		DocURL: doc.URL,
		Mode:   "publish",
	}, nil
}

func (h *ScriptPipelineHandler) normalizeCreateDocumentRequest(req *CreateDocumentRequest) {
	if strings.TrimSpace(req.Script) == "" && strings.TrimSpace(req.SourceText) != "" {
		req.Script = req.SourceText
	}
}

func savePreviewDocument(title, content string) (string, error) {
	base := strings.TrimSpace(title)
	if base == "" {
		base = "script_doc"
	}
	base = strings.NewReplacer(" ", "_", ":", "", "/", "_", "\\", "_", "\n", "_", "\r", "_").Replace(base)
	if len([]rune(base)) > 50 {
		runes := []rune(base)
		base = string(runes[:50])
	}
	filename := fmt.Sprintf("/tmp/%s_%d.md", base, time.Now().Unix())
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to save preview file: %w", err)
	}
	return fmt.Sprintf("file://%s", filename), nil
}

func (h *ScriptPipelineHandler) enrichCreateDocumentRequest(ctx context.Context, req *CreateDocumentRequest, topic string) {
	// 1) Build segments from script when caller sends only plain text.
	if len(req.Segments) == 0 && strings.TrimSpace(req.Script) != "" {
		semanticSegments, _, err := h.buildSemanticSegments(ctx, topic, req.Script, req.Duration, req.Language, 4)
		if err == nil && len(semanticSegments) > 0 {
			req.Segments = semanticSegments
			// Enrich segments with Keywords and Entities (NO HARDCODED)
			req.Segments = enrichSegments(req.Segments)
		} else {
			sentences := scriptdocs.ExtractSentences(req.Script)
			if len(sentences) > 0 {
				avgDuration := 20
				if req.Duration > 0 {
					avgDuration = req.Duration / len(sentences)
					if avgDuration <= 0 {
						avgDuration = 20
					}
				}
				req.Segments = make([]Segment, 0, len(sentences))
				for i, sentence := range sentences {
					req.Segments = append(req.Segments, Segment{
						Index:     i,
						Text:      sentence,
						StartTime: i * avgDuration,
						EndTime:   (i + 1) * avgDuration,
					})
				}
				// Enrich segments with Keywords and Entities (NO HARDCODED)
				req.Segments = enrichSegments(req.Segments)
			}
		}
	}

	// 2) Entity extraction fallback.
	if len(req.FrasiImportanti) == 0 || len(req.NomiSpeciali) == 0 || len(req.ParoleImportanti) == 0 || len(req.EntitaConImmagine) == 0 {
		seenNomi := make(map[string]bool)
		seenParole := make(map[string]bool)
		allSentences := make([]string, 0, len(req.Segments))
		for _, seg := range req.Segments {
			if len(seg.Text) > 20 && len(req.FrasiImportanti) < 12 {
				req.FrasiImportanti = append(req.FrasiImportanti, seg.Text)
			}
			nomi := scriptdocs.ExtractProperNouns([]string{seg.Text})
			for _, n := range nomi {
				key := strings.ToLower(strings.TrimSpace(n))
				if key == "" || len(key) <= 2 || seenNomi[key] {
					continue
				}
				seenNomi[key] = true
				req.NomiSpeciali = append(req.NomiSpeciali, n)
			}
			parole := scriptdocs.ExtractKeywords(seg.Text)
			for _, p := range parole {
				key := strings.ToLower(strings.TrimSpace(p))
				if key == "" || len(key) <= 2 || seenParole[key] {
					continue
				}
				seenParole[key] = true
				req.ParoleImportanti = append(req.ParoleImportanti, p)
			}
			allSentences = append(allSentences, seg.Text)
		}
		if len(req.EntitaConImmagine) == 0 && len(allSentences) > 0 {
			for entity, imageURL := range scriptdocs.ExtractEntitiesWithImages(allSentences) {
				if strings.TrimSpace(imageURL) == "" {
					continue
				}
				req.EntitaConImmagine = append(req.EntitaConImmagine, EntityImage{
					Entity:   entity,
					ImageURL: imageURL,
				})
			}
		}
	}

	// 3) Topic folder resolution fallback (DB only: STOCK section).
	if strings.TrimSpace(req.StockFolderURL) == "" {
		if folderID, folderName := h.resolveStockFolderForDocument(topic); folderID != "" {
			req.StockFolderURL = folderID
			if strings.TrimSpace(req.StockFolder) == "" {
				req.StockFolder = folderName
			}
		}
	}

	// 4) DRIVE CLIPS fallback is DB-only (clips section), never stock/indexer.
	stockFolderID := normalizeDriveFolderID(req.StockFolderURL)
	cleanedDrive := make([]DriveFolderAssoc, 0, len(req.DriveAssocs))
	for _, assoc := range req.DriveAssocs {
		fid := normalizeDriveFolderID(assoc.FolderURL)
		if fid == "" {
			cleanedDrive = append(cleanedDrive, assoc)
			continue
		}
		// Never allow DRIVE CLIPS to point to STOCK folder/root.
		if (stockFolderID != "" && fid == stockFolderID) || (h.stockRootFolder != "" && fid == h.stockRootFolder) {
			continue
		}
		cleanedDrive = append(cleanedDrive, assoc)
	}
	req.DriveAssocs = cleanedDrive
	if len(req.DriveAssocs) == 0 && h.stockDB != nil && strings.TrimSpace(topic) != "" {
		if folder, _ := h.stockDB.FindFolderByTopicInSection(topic, "clips"); folder != nil {
			req.DriveAssocs = append(req.DriveAssocs, DriveFolderAssoc{
				Phrase:     topic,
				FolderName: folder.FullPath,
				FolderURL:  "https://drive.google.com/drive/folders/" + folder.DriveID,
			})
		}
	}

	// 5) Artlist association fallback.
	if len(req.ArtlistAssocs) == 0 && (h.artlistDB != nil || h.artlistIndex != nil) {
		used := make(map[string]bool)
		for _, seg := range req.Segments {
			terms := scriptdocs.ExtractKeywords(seg.Text)
			if len(terms) == 0 {
				terms = []string{seg.Text}
			}
			var clips []ArtlistClipRef
			if h.artlistDB != nil {
				if results, err := h.artlistDB.FindDownloadedClipsWithSimilarTags(terms, 1); err == nil {
					for _, r := range results {
						if used[r.URL] {
							continue
						}
						used[r.URL] = true
						clips = append(clips, ArtlistClipRef{
							ClipID:    r.URL,
							Name:      r.Name,
							Term:      seg.Text,
							URL:       r.DriveURL,
							Folder:    r.FolderID,
							Timestamp: "",
						})
						break
					}
				}
			}
			if len(clips) == 0 && h.artlistIndex != nil {
				for _, r := range h.artlistIndex.Search(terms, 1) {
					if used[r.URL] {
						continue
					}
					used[r.URL] = true
					clips = append(clips, ArtlistClipRef{
						ClipID:    r.URL,
						Name:      r.Name,
						Term:      seg.Text,
						URL:       r.URL,
						Folder:    "Artlist",
						Timestamp: "",
					})
					break
				}
			}
			if len(clips) > 0 {
				req.ArtlistAssocs = append(req.ArtlistAssocs, ArtlistAssoc{
					Phrase: seg.Text,
					Clips:  clips,
				})
			}
		}
	}
}

func (h *ScriptPipelineHandler) GenerateFullPipeline(c *gin.Context) {
	var req FullPipelineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Start progress tracking
	tracker := GetProgressTracker()
	operationID := GenerateOperationID("full_pipeline")
	tracker.StartTracking(operationID)

	// Send initial progress
	tracker.SendProgress(operationID, "start", "Starting full pipeline", 0.0, gin.H{"topic": req.Topic})

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

	segments := []Segment{}
	chapters := []ChapterPlan{}
	var err error
		segments, chapters, err = h.buildSemanticSegments(reqContext, req.Topic, text, req.Duration, req.Language, 4)
		if err != nil {
			tracker.SendProgress(operationID, "error", "Failed to build segments", 0.0, gin.H{"error": err.Error()})
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		tracker.SendProgress(operationID, "segments_built", "Built semantic segments", 0.2, gin.H{"count": len(segments)})

		// Enrich segments with Keywords and Entities (NO HARDCODED)
		segments = enrichSegments(segments)
		tracker.SendProgress(operationID, "segments_enriched", "Enriched segments with keywords", 0.3, nil)

		if len(segments) == 0 {
			sentences := scriptdocs.ExtractSentences(text)
		avgDuration := 20
		for i, sentence := range sentences {
			segments = append(segments, Segment{
				Index:     i,
				Text:      sentence,
				StartTime: i * avgDuration,
				EndTime:   (i + 1) * avgDuration,
			})
		}
	}

		// --- NEW: ENTITY EXTRACTION LOGIC ---
		tracker.SendProgress(operationID, "extracting_entities", "Extracting entities", 0.4, nil)

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

	// --- NEW: TIGHT LIMITS & CLEANING ---
	const personaLimit = 6
	frasiImportanti = uniqueAndLimit(frasiImportanti, personaLimit)
	nomiSpecialiAll = uniqueAndLimit(nomiSpecialiAll, personaLimit)
	paroleImportantiAll = uniqueAndLimit(paroleImportantiAll, personaLimit)
		entitaConImmagine = uniqueEntitiesWithImage(entitaConImmagine, personaLimit)
		tracker.SendProgress(operationID, "entities_extracted", "Entities extracted", 0.5, gin.H{"count": len(allEntities)})

		// --- END ENTITY EXTRACTION ---

		// 1. Find Topic Folder for Prioritization
		tracker.SendProgress(operationID, "searching_clips", "Searching for clips", 0.6, nil)

		var topicFolderID string
		if h.stockDB != nil && req.Topic != "" {
			folder, _ := h.stockDB.FindFolderByTopicInSection(req.Topic, "stock")
			if folder != nil {
				topicFolderID = folder.DriveID
			}
		}

	// --- NEW: Persona-based Negative Filters ---
	negativeTerms := []string{}
	topicLower := strings.ToLower(req.Topic)
	if strings.Contains(topicLower, "davis") {
		negativeTerms = append(negativeTerms, "mayweather", "floyd", "interview", "talking")
	} else if strings.Contains(topicLower, "mayweather") {
		negativeTerms = append(negativeTerms, "davis", "tank")
	}

	usedClipIDs := make(map[string]bool)
	usedFolderIDs := make(map[string]bool) // For de-duplicating drive folders
	var allStockClips []StockClip
	var stockAssocs []StockAssoc
	var driveAssocs []DriveFolderAssoc
	var artlistAssocs []ArtlistAssoc
	var allArtlistClips []ArtlistClipRef

	for _, seg := range segments {
		segTextLower := strings.ToLower(seg.Text)
		// 1. Get specific keywords for THIS segment
		allTerms := scriptdocs.ExtractKeywords(seg.Text)
		var specificTerms []string
		for _, t := range allTerms {
			t = strings.ToLower(t)
			// Filter out too short or common meaningless words
			if len(t) > 3 && t != "with" && t != "that" && t != "from" && t != "this" {
				specificTerms = append(specificTerms, t)
			}
		}
		if len(specificTerms) == 0 && len(allTerms) > 0 {
			specificTerms = allTerms
		}
		if len(specificTerms) == 0 {
			specificTerms = append(specificTerms, seg.Text)
		}

		// 2. Prepare search query (prioritize specific terms)
		searchTerms := append([]string{}, specificTerms...)

		// For Gervonta Davis specifically, force some boxing terms if few keywords found
		if strings.Contains(topicLower, "davis") && len(specificTerms) < 3 {
			searchTerms = append(searchTerms, "boxing", "fight", "match")
		}

		// --- A. STOCK DRIVE (Prioritized Search - STOCK ONLY) ---
		var segmentStockClips []StockClip
		initial, final := extractPhrases(seg.Text)

		// Search in clipIndexer/clipSearch for specific DRIVE clips
		if h.clipIndexer != nil {
			// Try specific terms first for better accuracy
			query := strings.Join(specificTerms, " ")
			indexerResults := h.clipIndexer.Search(query, clip.SearchFilters{})

			// If no results, try individual specific terms (the most relevant ones)
			if len(indexerResults) == 0 {
				for _, term := range specificTerms {
					if len(term) < 4 {
						continue
					}
					res := h.clipIndexer.Search(term, clip.SearchFilters{})
					if len(res) > 0 {
						indexerResults = append(indexerResults, res...)
						if len(indexerResults) >= 5 {
							break
						}
					}
				}
			}

			// If still no results, try broader searchTerms
			if len(indexerResults) == 0 && len(searchTerms) > len(specificTerms) {
				query = strings.Join(searchTerms, " ")
				indexerResults = h.clipIndexer.Search(query, clip.SearchFilters{})
			}

			for _, r := range indexerResults {
				if len(segmentStockClips) >= 3 {
					break
				}
				if usedClipIDs[r.ID] {
					continue
				}

				// Apply negative filters for Stock Clips
				filenameLower := strings.ToLower(r.Filename)
				pathLower := strings.ToLower(r.FolderPath)
				isBanned := false
				for _, neg := range negativeTerms {
					if strings.Contains(filenameLower, neg) || strings.Contains(pathLower, neg) {
						// Only ban if the segment is about action/knockout and we are hitting an interview
						if (strings.Contains(segTextLower, "knockout") || strings.Contains(segTextLower, "action") || strings.Contains(segTextLower, "punch")) &&
							(strings.Contains(filenameLower, "interview") || strings.Contains(filenameLower, "talking")) {
							isBanned = true
							break
						}
					}
				}
				if isBanned {
					continue
				}

				clipRef := StockClip{
					ClipID:     r.ID,
					Filename:   r.Filename,
					FolderPath: r.FolderPath,
					DriveLink:  r.DriveLink,
				}
				segmentStockClips = append(segmentStockClips, clipRef)
				usedClipIDs[r.ID] = true
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
		tracker.SendProgress(operationID, "clips_found", "Clips found", 0.7, gin.H{"stock": len(allStockClips), "artlist": len(allArtlistClips)})

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
				fid := folders[0].ID
				if !usedFolderIDs[fid] {
					driveAssocs = append(driveAssocs, DriveFolderAssoc{
						Phrase:     seg.Text,
						FolderName: folders[0].Name,
						FolderURL:  "https://drive.google.com/drive/folders/" + fid,
					})
					usedFolderIDs[fid] = true
				}
			}
		}

		// --- C. ARTLIST (Pure Source) ---
		var segmentArtlistClips []ArtlistClipRef
		if h.artlistDB != nil {
			results, _ := h.artlistDB.FindDownloadedClipsWithSimilarTags(specificTerms, 1)
			for _, r := range results {
				if len(segmentArtlistClips) >= 1 { // REDUCED TO 1 PER SEGMENT
					break
				}
				if usedClipIDs[r.URL] {
					continue
				}

				// Apply negative filters for Artlist
				nameLower := strings.ToLower(r.Name)
				isBanned := false
				for _, neg := range negativeTerms {
					if strings.Contains(nameLower, neg) {
						if strings.Contains(segTextLower, "knockout") || strings.Contains(segTextLower, "action") {
							isBanned = true
							break
						}
					}
				}
				if isBanned {
					continue
				}

				clipRef := ArtlistClipRef{
					ClipID: r.URL,
					Name:   r.Name,
					Term:   strings.Join(specificTerms, ", "),
					URL:    r.DriveURL,
					Folder: r.FolderID,
					Source: "ArtlistDB",
					Score:  90.0, // High confidence for tagged clips
				}
				segmentArtlistClips = append(segmentArtlistClips, clipRef)
				allArtlistClips = append(allArtlistClips, clipRef)
				usedClipIDs[r.URL] = true
			}
		}

		// Fallback to ArtlistIndex (Stock/Artlist folders)
		if len(segmentArtlistClips) == 0 {
			if h.artlistIndex != nil {
				idxResults := h.artlistIndex.Search(specificTerms, 1) // REDUCED TO 1
				for _, r := range idxResults {
					if len(segmentArtlistClips) >= 1 {
						break
					}
					if usedClipIDs[r.URL] {
						continue
					}
					clipRef := ArtlistClipRef{
						ClipID: r.URL,
						Name:   r.Name,
						Term:   strings.Join(specificTerms, ", "),
						URL:    r.URL,
						Folder: "Stock/Artlist (Indexed)",
						Source: "ArtlistIndex",
						Score:  80.0,
					}
					segmentArtlistClips = append(segmentArtlistClips, clipRef)
					allArtlistClips = append(allArtlistClips, clipRef)
					usedClipIDs[r.URL] = true
				}
			}
		}

		// --- D. DYNAMIC SEARCH & DOWNLOAD (New clips from Artlist/YouTube) ---
		if len(segmentArtlistClips) == 0 && len(segmentStockClips) == 0 && h.clipSearch != nil {
			// Select the best keyword for dynamic search (longest specific term)
			var bestKW string
			for _, t := range specificTerms {
				if len(t) > len(bestKW) {
					bestKW = t
				}
			}

			if bestKW != "" && len(bestKW) > 3 {
				// Search, download and upload in background-like but blocking for this doc
				dynamicResults, err := h.clipSearch.SearchClips(reqContext, []string{bestKW})
				if err == nil && len(dynamicResults) > 0 {
					dr := dynamicResults[0]
					clipRef := ArtlistClipRef{
						ClipID: dr.ClipID,
						Name:   dr.Filename,
						Term:   bestKW,
						URL:    dr.DriveURL,
						Folder: dr.Folder,
						Source: "DynamicSearch",
						Score:  70.0,
					}
					segmentArtlistClips = append(segmentArtlistClips, clipRef)
					allArtlistClips = append(allArtlistClips, clipRef)
					usedClipIDs[dr.ClipID] = true
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

		tracker.SendProgress(operationID, "building_doc", "Building document content", 0.8, nil)

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

	publishCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

		tracker.SendProgress(operationID, "creating_doc", "Creating document", 0.9, nil)

		doc, err := h.docClient.CreateDoc(publishCtx, req.Topic, content, "")
		if err != nil {
			tracker.SendProgress(operationID, "error", "Failed to create document", 0.0, gin.H{"error": err.Error()})
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}

		tracker.SendProgress(operationID, "complete", "Pipeline completed", 1.0, gin.H{"doc_url": doc.URL})
		tracker.Complete(operationID)

		c.JSON(http.StatusOK, gin.H{
			"ok":                  true,
			"doc_url":             doc.URL,
			"operation_id":        operationID,
			"progress_url":        "/api/script/progress/" + operationID,
			"segments_count":      len(segments),
			"chapters_count":      len(chapters),
			"stock_clips_found":   len(allStockClips),
			"artlist_clips_found": len(allArtlistClips),
			"entities_found":      len(allEntities),
		})
}
