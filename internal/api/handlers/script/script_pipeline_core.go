package script

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/service/scriptdocs"
)

// createDocumentFromRequest orchestrates the document creation process.
func (h *ScriptPipelineHandler) createDocumentFromRequest(ctx context.Context, req *CreateDocumentRequest) (*CreateDocumentResponse, error) {
	h.normalizeCreateDocumentRequest(req)

	topic := req.Topic
	if topic == "" {
		topic = req.Title
	}

	if !req.SkipEnrichment {
		h.enrichCreateDocumentRequest(ctx, req, topic)
	}

	stockFolderID := normalizeDriveFolderID(req.StockFolderURL)
	scriptBody := req.Script
	if strings.TrimSpace(scriptBody) == "" {
		scriptBody = req.SourceText
	}
	var content string
	if req.MinimalDoc {
		content = buildMinimalDocumentContent(req.Title, topic, req.Duration, req.Language, scriptBody)
	} else {
		content = h.BuildDocumentContent(
			req.Title,
			topic,
			req.Duration,
			req.Language,
			scriptBody,
			req.Segments,
			req.ArtlistAssocs,
			stockFolderID,
			req.StockFolder,
			req.StockDriveAssocs,
			req.ClipDriveAssocs,
			req.FrasiImportanti,
			req.NomiSpeciali,
			req.ParoleImportanti,
			req.EntitaConImmagine,
			req.ImageAssociations,
			req.MixedSegments,
			req.Translations,
		)
	}

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

// enrichCreateDocumentRequest adds segments, entities, and clip associations to a request.
func (h *ScriptPipelineHandler) enrichCreateDocumentRequest(ctx context.Context, req *CreateDocumentRequest, topic string) {
	// 1) Build segments from script when caller sends only plain text.
	if len(req.Segments) == 0 && strings.TrimSpace(req.Script) != "" {
		semanticSegments, _, err := h.buildSemanticSegments(ctx, topic, req.Script, req.Duration, req.Language, 4)
		if err == nil && len(semanticSegments) > 0 {
			req.Segments = semanticSegments
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
				req.Segments = enrichSegments(req.Segments)
			}
		}
	}

	// 2) Entity extraction fallback.
	if len(req.FrasiImportanti) == 0 || len(req.NomiSpeciali) == 0 || len(req.ParoleImportanti) == 0 || len(req.EntitaConImmagine) == 0 {
		frasi, nomi, parole, images := h.extractEntitiesForPipeline(req.Segments)
		req.FrasiImportanti = frasi
		req.NomiSpeciali = nomi
		req.ParoleImportanti = parole
		req.EntitaConImmagine = images
	}

	// 3) Topic folder resolution fallback.
	if strings.TrimSpace(req.StockFolderURL) == "" {
		if folderID, folderName := h.resolveStockFolderForDocument(topic); folderID != "" {
			req.StockFolderURL = folderID
			if strings.TrimSpace(req.StockFolder) == "" {
				req.StockFolder = folderName
			}
		} else if compactTopic := compactSearchTopic(topic); compactTopic != "" {
			if folderID, folderName := h.resolveStockFolderForDocument(compactTopic); folderID != "" {
				req.StockFolderURL = folderID
				if strings.TrimSpace(req.StockFolder) == "" {
					req.StockFolder = folderName
				}
			}
		}
	}

	if strings.TrimSpace(req.StockFolder) == "" && strings.TrimSpace(h.stockRootFolder) != "" {
		if rootName := h.resolveDriveFolderName(h.stockRootFolder); rootName != "" {
			req.StockFolder = rootName
		}
	}

	// 4) Clip associations fallback.
	if len(req.StockDriveAssocs) == 0 || len(req.ClipDriveAssocs) == 0 || len(req.ArtlistAssocs) == 0 {
		_, drive, artlist, _ := h.searchClipsForPipeline(ctx, topic, req.Segments)
		if len(req.ClipDriveAssocs) == 0 {
			req.ClipDriveAssocs = drive
		}
		if len(req.ArtlistAssocs) == 0 {
			req.ArtlistAssocs = artlist
		}
	}

	if len(req.StockDriveAssocs) == 0 {
		if folderID, folderName := h.resolveStockFolderForDocument(topic); folderID != "" {
			req.StockDriveAssocs = append(req.StockDriveAssocs, DriveFolderAssoc{
				Phrase:        topic,
				InitialPhrase: topic,
				FinalPhrase:   topic,
				FolderName:    folderName,
				FolderURL:     "https://drive.google.com/drive/folders/" + folderID,
			})
		}
	}
}

// isJunkEntity checks if a string is useless technical/generic text
func isJunkEntity(s string) bool {
	junk := map[string]bool{
		"qui": true, "here": true, "ecco": true, "this": true, "that": true,
		"transizione": true, "transition": true, "immagini": true, "images": true,
		"musica": true, "music": true, "dettagli": true, "details": true,
		"inizio": true, "start": true, "fine": true, "end": true,
		"scena": true, "scene": true, "biografia": true, "biography": true,
		"video": true, "clip": true, "audio": true, "montaggio": true,
		"background": true, "sottofondo": true, "crescendo": true, "archi": true,
		"sequenza": true, "mostrano": true, "mostra": true, "narratore": true,
		"script": true, "testo": true, "parola": true, "frase": true,
		"titolo": true, "topic": true, "durata": true, "lingua": true,
		"english": true, "italiano": true, "italian": true, "versione": true,
		"l’inizio": true, "l'inizio": true, "era": true, "full": true, "version": true,
	}
	lower := strings.ToLower(strings.TrimSpace(s))
	// Rimuovi se troppo corta, se è in blacklist o se è puramente numerica
	if len(lower) < 3 || junk[lower] {
		return true
	}
	// Rimuovi se contiene caratteri tecnici o punteggiatura sospetta
	if strings.ContainsAny(lower, "()[]{}*:#/") {
		return true
	}
	return false
}

// cleanMetaFromPhrase removes (Musica: ...) or similar from a phrase
func cleanMetaFromPhrase(s string) string {
	re := regexp.MustCompile(`(?i)(\(|\[|\*\*)\s*(musica|immagini|scena|audio|video|clip|transizione|visual).*:.*(\)|\]|\*\*)`)
	return strings.TrimSpace(re.ReplaceAllString(s, ""))
}

// extractEntitiesForPipeline extracts high-quality quotes, names, and images from segments.
func (h *ScriptPipelineHandler) extractEntitiesForPipeline(segments []Segment) (frasi []string, nomi []string, parole []string, images []EntityImage) {
	seenPhrase := make(map[string]bool)
	seenName := make(map[string]bool)
	seenKeyword := make(map[string]bool)
	allSentences := make([]string, 0, len(segments))

	for _, seg := range segments {
		cleanText := cleanMetaFromPhrase(seg.Text)
		allSentences = append(allSentences, cleanText)
		
		// 1. Frasi Potenti
		if len(cleanText) > 45 {
			phrase := shortPhrase(cleanText, 16)
			lowerPhrase := strings.ToLower(phrase)
			if !seenPhrase[lowerPhrase] && !isJunkEntity(phrase) {
				frasi = append(frasi, phrase)
				seenPhrase[lowerPhrase] = true
			}
		}

		// 2. Nomi Propri (Solo reali e non duplicati)
		foundNomi := scriptdocs.ExtractProperNouns([]string{cleanText})
		for _, n := range foundNomi {
			lowerN := strings.ToLower(n)
			if !isJunkEntity(n) && !seenName[lowerN] && len(n) > 2 {
				nomi = append(nomi, n)
				seenName[lowerN] = true
			}
		}

		// 3. Parole Chiave (Solo concetti forti)
		foundParole := scriptdocs.ExtractKeywords(cleanText)
		for _, p := range foundParole {
			lowerP := strings.ToLower(p)
			if !isJunkEntity(p) && !seenKeyword[lowerP] && !seenName[lowerP] && len(p) > 3 {
				parole = append(parole, p)
				seenKeyword[lowerP] = true
			}
		}
	}

	// 4. Immagini (Soggetti principali dai primi segmenti)
	topSentences := allSentences
	if len(topSentences) > 4 {
		topSentences = topSentences[:4]
	}
	entityImagesMap := scriptdocs.ExtractEntitiesWithImages(topSentences)
	for entity, imageURL := range entityImagesMap {
		if imageURL != "" && !isJunkEntity(entity) {
			images = append(images, EntityImage{Entity: entity, ImageURL: imageURL})
		}
	}

	frasi = uniqueAndLimit(frasi, 10)
	nomi = uniqueAndLimit(nomi, 15)
	parole = uniqueAndLimit(parole, 15)
	images = uniqueEntitiesWithImage(images, 6)
	return
}

// extractEntitiesForPipelineNoImages extracts phrases, nouns, and keywords without image lookup.
// This is the fast path for analysis/publish endpoints where image enrichment would be too slow.
func (h *ScriptPipelineHandler) extractEntitiesForPipelineNoImages(segments []Segment) (frasi []string, nomi []string, parole []string) {
	seenEntity := make(map[string]bool)

	for _, seg := range segments {
		if len(seg.Text) > 20 {
			frasi = append(frasi, shortPhrase(seg.Text, 12))
		}
		foundNomi := uniqueAndLimit(scriptdocs.ExtractProperNouns([]string{seg.Text}), 5)
		foundParole := uniqueAndLimit(scriptdocs.ExtractKeywords(seg.Text), 5)

		for _, n := range foundNomi {
			lower := strings.ToLower(n)
			if !seenEntity[lower] && len(n) > 2 {
				seenEntity[lower] = true
				nomi = append(nomi, n)
			}
		}
		for _, p := range foundParole {
			lower := strings.ToLower(p)
			if !seenEntity[lower] && len(p) > 2 {
				seenEntity[lower] = true
				parole = append(parole, p)
			}
		}
	}

	frasi = uniqueAndLimit(frasi, 5)
	nomi = uniqueAndLimit(nomi, 15)
	parole = uniqueAndLimit(parole, 15)
	return
}

// extractImagesForPipeline extracts a small set of entity-image pairs for publish docs.
func (h *ScriptPipelineHandler) extractImagesForPipeline(topic string, title string, segments []Segment) []EntityImage {
	if len(segments) == 0 && strings.TrimSpace(topic) == "" && strings.TrimSpace(title) == "" {
		return nil
	}
	var allSentences []string
	if strings.TrimSpace(topic) != "" {
		allSentences = append(allSentences, topic)
	}
	if strings.TrimSpace(title) != "" {
		allSentences = append(allSentences, title)
	}
	for _, seg := range segments {
		if strings.TrimSpace(seg.Text) != "" {
			allSentences = append(allSentences, seg.Text)
		}
	}
	if len(allSentences) == 0 {
		return nil
	}
	imagesMap := scriptdocs.ExtractEntitiesWithImages(allSentences)
	if len(imagesMap) == 0 {
		return nil
	}
	images := make([]EntityImage, 0, len(imagesMap))
	for entity, imageURL := range imagesMap {
		if strings.TrimSpace(entity) == "" || strings.TrimSpace(imageURL) == "" {
			continue
		}
		images = append(images, EntityImage{Entity: entity, ImageURL: imageURL})
	}
	return uniqueEntitiesWithImage(images, 10)
}

// searchClipsForPipeline performs clip searches across multiple sources.
func (h *ScriptPipelineHandler) searchClipsForPipeline(ctx context.Context, topic string, segments []Segment) (stock []StockAssoc, drive []DriveFolderAssoc, artlist []ArtlistAssoc, topicFolderID string) {
	searchTopic := compactSearchTopic(topic)
	if h.stockDB != nil && topic != "" {
		if folder, _ := h.stockDB.FindFolderByTopicInSection(topic, "stock"); folder != nil {
			topicFolderID = folder.DriveID
		} else if searchTopic != "" {
			if folder, _ := h.stockDB.FindFolderByTopicInSection(searchTopic, "stock"); folder != nil {
				topicFolderID = folder.DriveID
			}
		}
	}

	usedClipIDs := make(map[string]bool)
	usedFolderIDs := make(map[string]bool)
	var mu sync.Mutex

	type segmentResult struct {
		stock   *StockAssoc
		drive   *DriveFolderAssoc
		artlist *ArtlistAssoc
	}
	results := make([]segmentResult, len(segments))
	var wg sync.WaitGroup

	for i := range segments {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			seg := segments[idx]

			initial, final := extractPhrases(seg.Text)
			displayPhrase := shortPhrase(seg.Text, 10)
			if displayPhrase == "" {
				displayPhrase = initial
			}
			specificTerms := extractSpecificTerms(seg.Text)

			// A. STOCK DRIVE
			var segmentStockClips []StockClip
			if h.clipIndexer != nil {
				indexerResults := h.clipIndexer.Search(strings.Join(specificTerms, " "), clip.SearchFilters{})
				for _, r := range indexerResults {
					if len(segmentStockClips) >= 3 {
						break
					}

					mu.Lock()
					if usedClipIDs[r.ID] {
						mu.Unlock()
						continue
					}
					usedClipIDs[r.ID] = true
					mu.Unlock()

					segmentStockClips = append(segmentStockClips, StockClip{
						ClipID: r.ID, Filename: r.Filename, FolderPath: r.FolderPath, DriveLink: r.DriveLink,
					})
				}
			}
			if len(segmentStockClips) > 0 {
				results[idx].stock = &StockAssoc{Phrase: displayPhrase, InitialPhrase: initial, FinalPhrase: final, Clips: segmentStockClips}
			}

			// B. DRIVE CLIPS (Folders)
			if h.clipIndexer != nil {
				candidateQueries := make([]string, 0, len(specificTerms)+2)
				candidateQueries = append(candidateQueries, specificTerms...)
				if searchTopic != "" {
					candidateQueries = append(candidateQueries, searchTopic)
				}
				if topic != "" {
					candidateQueries = append(candidateQueries, topic)
				}
				seenQueries := make(map[string]bool)
				var folders []clip.IndexedFolder
				for _, q := range candidateQueries {
					q = strings.TrimSpace(q)
					if q == "" || seenQueries[strings.ToLower(q)] {
						continue
					}
					seenQueries[strings.ToLower(q)] = true
					folders = h.clipIndexer.SearchFolders(q)
					if len(folders) > 0 {
						break
					}
				}
				if len(folders) > 0 {
					mu.Lock()
					if !usedFolderIDs[folders[0].ID] {
						usedFolderIDs[folders[0].ID] = true
						mu.Unlock()

						folderName := formatClipFolderDisplayPath(folders[0])
						results[idx].drive = &DriveFolderAssoc{
							Phrase:        shortPhrase(seg.Text, 10),
							InitialPhrase: initial,
							FinalPhrase:   final,
							FolderName:    folderName,
							FolderURL:     "https://drive.google.com/drive/folders/" + folders[0].ID,
						}
					} else {
						mu.Unlock()
					}
				}
			}

			// C. ARTLIST
			var segmentArtlistClips []ArtlistClipRef
			if h.artlistDB != nil {
				artResults, _ := h.artlistDB.FindDownloadedClipsWithSimilarTags(specificTerms, 1)
				for _, r := range artResults {
					mu.Lock()
					if !usedClipIDs[r.URL] {
						usedClipIDs[r.URL] = true
						mu.Unlock()

						segmentArtlistClips = append(segmentArtlistClips, ArtlistClipRef{
							ClipID: r.URL, Name: r.Name, Term: strings.Join(specificTerms, ", "), URL: r.DriveURL, Folder: r.FolderID, Source: "ArtlistDB", Score: 90.0,
						})
						break
					} else {
						mu.Unlock()
					}
				}
			}
			if len(segmentArtlistClips) > 0 {
				results[idx].artlist = &ArtlistAssoc{Phrase: displayPhrase, Clips: segmentArtlistClips}
			}
		}(i)
	}
	wg.Wait()

	// Merge results in order
	for _, res := range results {
		if res.stock != nil {
			stock = append(stock, *res.stock)
		}
		if res.drive != nil {
			drive = append(drive, *res.drive)
		}
		if res.artlist != nil {
			artlist = append(artlist, *res.artlist)
		}
	}
	return
}

func extractSpecificTerms(text string) []string {
	allTerms := scriptdocs.ExtractKeywords(text)
	var specificTerms []string
	for _, t := range allTerms {
		t = strings.ToLower(t)
		if len(t) > 3 && t != "with" && t != "that" && t != "from" && t != "this" {
			specificTerms = append(specificTerms, t)
		}
	}
	if len(specificTerms) == 0 {
		return allTerms
	}
	return specificTerms
}
