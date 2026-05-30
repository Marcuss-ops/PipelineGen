package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/storage"
	"velox/go-master/internal/media/vectorstore"
)

func (s *Service) callSemanticTagger(ctx context.Context, prompt, style, mediaType, generator string) (*SemanticMetadataPayload, error) {
	scriptPath := filepath.Join(s.scriptsDir, "semantic_tagger.py")
	args := []string{
		scriptPath,
		"--prompt", prompt,
		"--style", style,
		"--media-type", mediaType,
		"--generator", generator,
	}
	// Pass Ollama config for LLM enrichment at ingest time (one-shot, not at search time)
	if s.cfg != nil && s.cfg.External.OllamaURL != "" {
		args = append(args, "--ollama-url", s.cfg.External.OllamaURL)
	}
	if s.cfg != nil && s.cfg.External.OllamaModel != "" {
		args = append(args, "--ollama-model", s.cfg.External.OllamaModel)
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("semantic_tagger failed: %w (output: %s)", err, string(output))
	}

	var payload SemanticMetadataPayload
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode semantic_tagger output: %w", err)
	}

	return &payload, nil
}


func (s *Service) callLLMFallback(ctx context.Context, mediaType, prompt, style string) string {
	if s.llmGen == nil {
		return prompt
	}
	desc, err := s.llmGen.GenerateDescription(ctx, mediaType, prompt, style)
	if err != nil {
		s.log.Warn("LLM fallback failed", zap.Error(err))
		return prompt
	}
	return desc
}

func (s *Service) indexAssetInVectorStore(ctx context.Context, assetID, source, name, localPath, driveLink, style, mediaType, searchText string, tags []string) {
	if s.vectorSvc == nil {
		return
	}

	// 1. Get embedding from Python server
	adapter := realtime.NewPythonEmbeddingAdapter(s.cfg.ClipIndexer.ServerURL)
	embedding, err := adapter.EmbedText(ctx, searchText)
	if err != nil {
		s.log.Warn("Failed to generate embedding for search_text", zap.String("asset_id", assetID), zap.Error(err))
		return
	}

	// Convert to float32
	vec := make([]float32, len(embedding))
	for i, f := range embedding {
		vec[i] = float32(f)
	}

	// 2. Upsert to Qdrant
	vAsset := vectorstore.VectorAsset{
		AssetID:       assetID,
		Source:        source,
		Name:          name,
		LocalPath:     localPath,
		DriveLink:     driveLink,
		Style:         style,
		MediaType:     mediaType,
		TextEmbedding: vec,
		Tags:          tags,
		CreatedAt:     time.Now(),
	}

	if err := s.vectorSvc.UpsertAsset(ctx, vAsset); err != nil {
		s.log.Warn("Failed to upsert to vector store", zap.String("asset_id", assetID), zap.Error(err))
		return
	}

	// 3. Update DB status if it's an image
	if mediaType == "image" {
		_ = s.repo.UpdateEmbeddingStatus(ctx, assetID, "ready")
	}

	s.log.Info("Asset indexed in vector store", zap.String("asset_id", assetID), zap.String("media_type", mediaType))
}

func (s *Service) downloadAndIngest(ctx context.Context, slug, imgURL, style, source, query, description string, tags []string) (*models.ImageAsset, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	return s.IngestImage(ctx, slug, style, "", resp.Body, filepath.Base(imgURL), imgURL, description, tags, false, false)
}

func (s *Service) IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive, skipMetadata bool) (*models.ImageAsset, error) {
	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}

	// Legacy dedup: SHA256 check per immagini già salvate col vecchio path
	hasher := sha256.New()
	hasher.Write(content)
	legacyHash := hex.EncodeToString(hasher.Sum(nil))

	if existing, err := s.repo.GetImageByHash(ctx, legacyHash); err == nil && existing != nil {
		// Verify the file actually exists on disk (may have been cleaned up)
		filePath := filepath.Join(s.imagesDir, existing.PathRel)
		if _, statErr := os.Stat(filePath); statErr == nil {
			s.log.Info("IngestImage: hash dedup hit, returning existing", zap.String("hash", legacyHash))
			return existing, nil
		}
		s.log.Warn("IngestImage: hash dedup stale, re-ingesting",
			zap.String("hash", legacyHash),
			zap.String("old_path", filePath),
		)
	}

	s.log.Info("IngestImage: ingesting image",
		zap.String("slug", slug),
		zap.String("style", style),
		zap.String("gen_id", genID),
		zap.String("hash", legacyHash),
		zap.Bool("skip_drive", skipDrive),
	)

	return s.ingestDirect(ctx, slug, style, genID, content, filename, sourceURL, description, tags, legacyHash, skipDrive, skipMetadata)
}

func (s *Service) ingestDirect(ctx context.Context, slug, style, genID string, content []byte, filename, source, description string, tags []string, hash string, skipDrive, skipMetadata bool) (*models.ImageAsset, error) {
	// Enrich tags and subject from prompt if needed
	promptSubject, promptTags := extractSubjectAndTags(description)
	if slug == "" || slug == "unknown" {
		slug = Slugify(promptSubject)
	}
	if len(tags) == 0 {
		tags = promptTags
	}

	// 1. Trova Soggetto (o crealo)
	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err != nil || subject == nil {
		subject = &models.Subject{
			Slug:        slug,
			DisplayName: slug,
		}
		_, err := s.repo.CreateSubject(ctx, subject)
		if err != nil {
			s.log.Warn("Ingest: subject might exist", zap.String("slug", slug))
		}
	}

	// 2. Prepara percorsi
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}

	// Create request for resolver
	req := storage.AssetDestinationRequest{
		Source:       source, // Use the provided source (e.g. google-flow)
		MediaType:    storage.MediaTypeImage,
		Subject:      slug, // Prompt slug
		Hash:         hash,
		Ext:          ext,
		Style:        style, // Chosen style
		GenerationID: genID,
	}

	dest, err := s.mediaStore.ResolveDest(req)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}

	relPath := dest.RelativePath
	fullPath := dest.LocalPath
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	// 3. Salva il file fisico
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		s.log.Error("ingestDirect: failed to write file", zap.String("path", fullPath), zap.Error(err))
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}
	s.log.Info("ingestDirect: file saved", zap.String("path", fullPath), zap.Int("bytes", len(content)))

	// 4. Resolve generator dynamically from source
	generator := source
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if strings.Contains(source, "wikipedia.org") {
			generator = "wikipedia"
		} else if strings.Contains(source, "duckduckgo") {
			generator = "duckduckgo"
		} else {
			generator = "web-download"
		}
	}

	// 5. Upload to Drive if configured (skip if disabled by fullimages pipeline)
	var driveFileID string
	if s.mediaStore != nil && !skipDrive {
		fileID, link, err := s.mediaStore.UploadToDrive(ctx, req, fullPath)
		if err != nil {
			s.log.Warn("Drive upload failed", zap.Error(err))
		} else {
			driveFileID = fileID
			s.log.Info("Drive upload successful", zap.String("file_id", fileID))

			if !skipMetadata {
				// Estrai dimensioni reali dell'immagine
				imgWidth, imgHeight := decodeImageDimensions(content)

				// Upload metadata.json
				prompt := description // Usiamo description come prompt o info
				s.uploadImageMetadata(ctx, req, prompt, style, generator, fileID, link, hash, fullPath, imgWidth, imgHeight)
			}
		}
	}

	// 6. Estrai dimensioni reali dell'immagine (per DB)
	imgWidth, imgHeight := decodeImageDimensions(content)

	// Call Python tagger for rich metadata for DB record
	cleanPrompt := description
	if strings.Contains(description, "for prompt: ") {
		parts := strings.SplitN(description, "for prompt: ", 2)
		if len(parts) == 2 {
			cleanPrompt = parts[1]
		}
	}
	meta, err := s.callSemanticTagger(ctx, cleanPrompt, style, "image", generator)

	// NEW: LLM Fallback if confidence is low
	if err == nil && meta.Confidence < 0.6 {
		s.log.Info("Semantic confidence low, calling LLM fallback", zap.Float64("confidence", meta.Confidence))
		meta.SemanticDescription = s.callLLMFallback(ctx, "image", cleanPrompt, style)
	}

	var metaJSON []byte
	if err == nil {
		meta.AssetID = hash
		metaJSON, _ = json.Marshal(meta)
		// Enrich tags from rich metadata
		tags = uniqueAppend(tags, meta.Tags...)
	} else {
		// Fallback to basic metadata if tagger fails
		metaMap := map[string]any{
			"prompt":    description,
			"style":     style,
			"generator": generator,
		}
		metaJSON, _ = json.Marshal(metaMap)
	}

	// 7. Crea record DB con dimensioni reali
	asset := &models.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relPath,
		SourceURL:    source,
		Description:  description,
		DriveFileID:  driveFileID,
		Width:        imgWidth,
		Height:       imgHeight,
		SizeBytes:    int64(len(content)),
		Status:       "ready",
		MetadataJSON: string(metaJSON),
		Tags:         tags,
	}

	if _, err := s.repo.AddImage(ctx, asset); err != nil {
		// Final safety check for UNIQUE constraint
		if existing, exErr := s.repo.GetImageByHash(ctx, hash); exErr == nil && existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("failed to add image to repository: %w", err)
	}

	// NEW: Asynchronous Vector Indexing
	// Use WithoutCancel so the goroutine inherits ctx values (trace, logger)
	// but is NOT cancelled when the HTTP request ends.
	if s.vectorSvc != nil && err == nil {
		asyncCtx := context.WithoutCancel(ctx)
		go func() {
			driveLink := ""
			if driveFileID != "" {
				driveLink = fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileID)
			}
			s.indexAssetInVectorStore(asyncCtx, hash, source, cleanPrompt, relPath, driveLink, style, "image", meta.SearchText, tags)
		}()
	}

	return asset, nil
}

// decodeImageDimensions estrae larghezza e altezza da bytes immagine.
// Supporta JPEG, PNG, GIF. Per altri formati (webp, etc.) restituisce 0,0.
func decodeImageDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func uniqueAppend(slice []string, items ...string) []string {
	seen := make(map[string]bool)
	for _, s := range slice {
		seen[s] = true
	}
	for _, item := range items {
		if !seen[item] {
			slice = append(slice, item)
			seen[item] = true
		}
	}
	return slice
}

// uploadImageMetadata writes a metadata.json file in the same Drive folder as the image.
func (s *Service) uploadImageMetadata(ctx context.Context, req storage.AssetDestinationRequest, prompt, style, generator, fileID, driveLink, hash, localPath string, width, height int) {
	// Call Python tagger for rich metadata
	cleanPrompt := prompt
	if strings.Contains(prompt, "for prompt: ") {
		parts := strings.SplitN(prompt, "for prompt: ", 2)
		if len(parts) == 2 {
			cleanPrompt = parts[1]
		}
	}
	meta, err := s.callSemanticTagger(ctx, cleanPrompt, style, "image", generator)
	if err != nil {
		s.log.Warn("uploadImageMetadata: semantic tagger failed, using fallback", zap.Error(err))
		// Fallback to basic metadata
		fSubject, fTags := extractSubjectAndTags(prompt)
		styleList := []string{}
		if style != "" {
			styleList = append(styleList, style)
		}
		meta = &SemanticMetadataPayload{
			AssetID:             hash,
			AssetType:           "image",
			SemanticTier:        "generated_light",
			Source:              "generated",
			MediaType:           "image",
			Generator:           generator,
			PromptOriginal:      prompt,
			SemanticDescription: prompt,
			Subjects:            []string{fSubject},
			Tags:                fTags,
			Style:               styleList,
			SearchText:          prompt,
			EmbeddingStatus:     "pending",
			CreatedAt:           time.Now().Format(time.RFC3339),
		}
	} else {
		meta.AssetID = hash
		// NEW: LLM Fallback if confidence is low
		if meta.Confidence < 0.6 {
			s.log.Info("uploadImageMetadata: confidence low, calling LLM fallback", zap.Float64("confidence", meta.Confidence))
			meta.SemanticDescription = s.callLLMFallback(ctx, "image", cleanPrompt, style)
		}
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		s.log.Warn("uploadImageMetadata: failed to marshal metadata", zap.Error(err))
		return
	}

	tmpPath := filepath.Join(s.tempDir, "img_metadata_"+req.Hash+".json")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		s.log.Warn("uploadImageMetadata: failed to write temp metadata file", zap.Error(err))
		return
	}
	defer os.Remove(tmpPath)

	metaReq := req
	metaReq.Ext = ".json"
	metaReq.Hash = "metadata" // Standard name for metadata inside the folder
	if metaReq.GenerationID == "" && hash != "" {
		metaReq.GenerationID = hash
	}

	// Resolve local destination to save it locally too
	dest, err := s.mediaStore.ResolveDest(metaReq)
	if err == nil {
		localMetaPath := filepath.Join(filepath.Dir(dest.LocalPath), "metadata.json")
		os.MkdirAll(filepath.Dir(localMetaPath), 0755)
		if err := os.WriteFile(localMetaPath, data, 0644); err != nil {
			s.log.Warn("uploadImageMetadata: failed to save local metadata", zap.Error(err))
		} else {
			s.log.Info("uploadImageMetadata: local metadata saved", zap.String("path", localMetaPath))
		}
	}

	if _, _, err := s.mediaStore.UploadToDrive(ctx, metaReq, tmpPath); err != nil {
		s.log.Warn("uploadImageMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("uploadImageMetadata: metadata.json uploaded", zap.String("prompt", prompt), zap.String("style", style))
}

// UploadBatchMetadata writes a single metadata.json for a group of assets.
func (s *Service) UploadBatchMetadata(ctx context.Context, genID, slug, style, prompt, generator string, assets []*models.ImageAsset) {
	s.log.Info("UploadBatchMetadata: starting", zap.String("gen_id", genID), zap.Int("assets", len(assets)))
	if s.mediaStore == nil {
		s.log.Warn("UploadBatchMetadata: mediaStore is nil")
		return
	}
	if genID == "" {
		s.log.Warn("UploadBatchMetadata: genID is empty")
		return
	}

	// 1. Call Python tagger for rich metadata base
	meta, err := s.callSemanticTagger(ctx, prompt, style, "image", generator)
	if err != nil {
		s.log.Warn("UploadBatchMetadata: semantic tagger failed, using fallback", zap.Error(err))
		// Fallback to basic metadata
		fSubject, fTags := extractSubjectAndTags(prompt)
		styleList := []string{}
		if style != "" {
			styleList = append(styleList, style)
		}
		meta = &SemanticMetadataPayload{
			AssetID:             genID,
			AssetType:           "image_group",
			SemanticTier:        "generated_light",
			Source:              "generated",
			MediaType:           "image",
			Generator:           generator,
			PromptOriginal:      prompt,
			SemanticDescription: prompt,
			Subjects:            []string{fSubject},
			Tags:                fTags,
			Style:               styleList,
			SearchText:          prompt,
			EmbeddingStatus:     "pending",
			CreatedAt:           time.Now().Format(time.RFC3339),
		}
	} else {
		meta.AssetID = genID
		meta.AssetType = "image_group"
		// NEW: LLM Fallback if confidence is low
		if meta.Confidence < 0.6 {
			s.log.Info("UploadBatchMetadata: confidence low, calling LLM fallback", zap.Float64("confidence", meta.Confidence))
			meta.SemanticDescription = s.callLLMFallback(ctx, "image", prompt, style)
		}
	}

	// 2. Add individual asset info
	assetInfos := make([]map[string]any, len(assets))
	for i, a := range assets {
		assetInfos[i] = map[string]any{
			"hash":          a.Hash,
			"path":          a.PathRel,
			"width":         a.Width,
			"height":        a.Height,
			"drive_id":      a.DriveFileID,
			"variant_index": i + 1,
		}
	}
	meta.Assets = assetInfos

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		s.log.Warn("UploadBatchMetadata: failed to marshal metadata", zap.Error(err))
		return
	}

	tmpPath := filepath.Join(s.tempDir, "group_metadata_"+genID+".json")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return
	}
	defer os.Remove(tmpPath)

	req := storage.AssetDestinationRequest{
		Source:       storage.SourceImage,
		MediaType:    storage.MediaTypeImage,
		Subject:      slug,
		GenerationID: genID,
		Style:        style,
		Ext:          ".json",
		Hash:         "metadata",
	}

	dest, err := s.mediaStore.ResolveDest(req)
	if err == nil {
		s.log.Info("UploadBatchMetadata: resolved destination",
			zap.String("gen_id", genID),
			zap.String("style", style),
			zap.String("drive_path", dest.DriveFolderPath),
			zap.String("local_path", dest.LocalPath),
		)
		localMetaPath := filepath.Join(filepath.Dir(dest.LocalPath), "metadata.json")

		os.MkdirAll(filepath.Dir(localMetaPath), 0755)
		if err := os.WriteFile(localMetaPath, data, 0644); err != nil {
			s.log.Warn("UploadBatchMetadata: failed to save local metadata", zap.Error(err))
		} else {
			s.log.Info("UploadBatchMetadata: local metadata saved", zap.String("path", localMetaPath))
		}
	}

	if _, _, err := s.mediaStore.UploadToDrive(ctx, req, tmpPath); err != nil {
		s.log.Warn("UploadBatchMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("UploadBatchMetadata: metadata.json uploaded for group", zap.String("gen_id", genID), zap.Int("assets_count", len(assets)))

	// NEW: Asynchronous Vector Indexing for each asset in the group
	// Use WithoutCancel so goroutines inherit ctx values but outlive the HTTP request.
	if s.vectorSvc != nil {
		asyncCtx := context.WithoutCancel(ctx)
		for _, a := range assets {
			go func(asset *models.ImageAsset) {
				driveLink := ""
				if asset.DriveFileID != "" {
					driveLink = fmt.Sprintf("https://drive.google.com/file/d/%s/view", asset.DriveFileID)
				}
				s.indexAssetInVectorStore(asyncCtx, asset.Hash, generator, prompt, asset.PathRel, driveLink, style, "image", meta.SearchText, asset.Tags)
			}(a)
		}
	}
}
