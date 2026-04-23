package script

import (
	"context"
	"fmt"
	"strings"
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
		}
	}

	// 4) Clip associations fallback.
	if len(req.DriveAssocs) == 0 || len(req.ArtlistAssocs) == 0 {
		_, drive, artlist, _ := h.searchClipsForPipeline(ctx, topic, req.Segments)
		if len(req.DriveAssocs) == 0 {
			req.DriveAssocs = drive
		}
		if len(req.ArtlistAssocs) == 0 {
			req.ArtlistAssocs = artlist
		}
	}
}

// extractEntitiesForPipeline extracts and limits entities from segments.
func (h *ScriptPipelineHandler) extractEntitiesForPipeline(segments []Segment) (frasi []string, nomi []string, parole []string, images []EntityImage) {
	seenEntity := make(map[string]bool)
	allSentences := make([]string, 0, len(segments))

	for _, seg := range segments {
		allSentences = append(allSentences, seg.Text)
		if len(seg.Text) > 20 {
			frasi = append(frasi, seg.Text)
		}
		foundNomi := scriptdocs.ExtractProperNouns([]string{seg.Text})
		foundParole := scriptdocs.ExtractKeywords(seg.Text)

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

	entityImagesMap := scriptdocs.ExtractEntitiesWithImages(allSentences)
	for entity, imageURL := range entityImagesMap {
		if imageURL != "" {
			images = append(images, EntityImage{Entity: entity, ImageURL: imageURL})
		}
	}

	const limit = 6
	frasi = uniqueAndLimit(frasi, limit)
	nomi = uniqueAndLimit(nomi, limit)
	parole = uniqueAndLimit(parole, limit)
	images = uniqueEntitiesWithImage(images, limit)
	return
}

// searchClipsForPipeline performs clip searches across multiple sources.
func (h *ScriptPipelineHandler) searchClipsForPipeline(ctx context.Context, topic string, segments []Segment) (stock []StockAssoc, drive []DriveFolderAssoc, artlist []ArtlistAssoc, topicFolderID string) {
	if h.stockDB != nil && topic != "" {
		if folder, _ := h.stockDB.FindFolderByTopicInSection(topic, "stock"); folder != nil {
			topicFolderID = folder.DriveID
		}
	}

	usedClipIDs := make(map[string]bool)
	usedFolderIDs := make(map[string]bool)

	for _, seg := range segments {
		initial, final := extractPhrases(seg.Text)
		specificTerms := extractSpecificTerms(seg.Text)

		// A. STOCK DRIVE
		var segmentStockClips []StockClip
		if h.clipIndexer != nil {
			indexerResults := h.clipIndexer.Search(strings.Join(specificTerms, " "), clip.SearchFilters{})
			for _, r := range indexerResults {
				if len(segmentStockClips) >= 3 {
					break
				}
				if usedClipIDs[r.ID] {
					continue
				}
				segmentStockClips = append(segmentStockClips, StockClip{
					ClipID: r.ID, Filename: r.Filename, FolderPath: r.FolderPath, DriveLink: r.DriveLink,
				})
				usedClipIDs[r.ID] = true
			}
		}
		if len(segmentStockClips) > 0 {
			stock = append(stock, StockAssoc{Phrase: seg.Text, InitialPhrase: initial, FinalPhrase: final, Clips: segmentStockClips})
		}

		// B. DRIVE CLIPS (Folders)
		if h.clipIndexer != nil {
			folders := h.clipIndexer.SearchFolders(topic)
			if len(folders) > 0 && !usedFolderIDs[folders[0].ID] {
				drive = append(drive, DriveFolderAssoc{
					Phrase: seg.Text, FolderName: folders[0].Name, FolderURL: "https://drive.google.com/drive/folders/" + folders[0].ID,
				})
				usedFolderIDs[folders[0].ID] = true
			}
		}

		// C. ARTLIST
		var segmentArtlistClips []ArtlistClipRef
		if h.artlistDB != nil {
			results, _ := h.artlistDB.FindDownloadedClipsWithSimilarTags(specificTerms, 1)
			for _, r := range results {
				if !usedClipIDs[r.URL] {
					segmentArtlistClips = append(segmentArtlistClips, ArtlistClipRef{
						ClipID: r.URL, Name: r.Name, Term: strings.Join(specificTerms, ", "), URL: r.DriveURL, Folder: r.FolderID, Source: "ArtlistDB", Score: 90.0,
					})
					usedClipIDs[r.URL] = true
					break
				}
			}
		}
		if len(segmentArtlistClips) > 0 {
			artlist = append(artlist, ArtlistAssoc{Phrase: seg.Text, Clips: segmentArtlistClips})
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
