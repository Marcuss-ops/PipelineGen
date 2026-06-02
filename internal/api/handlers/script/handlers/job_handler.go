package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/images"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/pkg/concurrent"
)

// HandleSourceScriptGenerateJob processes the background job for script.generate_with_images
func (h *ScriptFlowHandler) HandleSourceScriptGenerateJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	h.log.Info("handling script.generate_with_images job", zap.String("job_id", job.ID))

	var req GenerateFromSourceRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	if len(req.Languages) > 0 && req.Language == "" {
		req.Language = req.Languages[0]
	}
	if req.Language == "" {
		req.Language = "en"
	}

	if tools.Progress != nil {
		tools.Progress(5, "Generating rewritten script with Ollama")
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(req.OutputName)
	}
	if title == "" {
		title = "Generated Script"
	}
	outputName := strings.TrimSpace(req.OutputName)
	if outputName == "" {
		outputName = images.Slugify(title)
	}
	if outputName == "" {
		outputName = "generated-script"
	}

	textDuration := types.EstimateDuration(types.CountWords(req.SourceText))
	if textDuration < 60 {
		textDuration = 60
	}
	textModel := ""
	if h.cfg != nil {
		textModel = strings.TrimSpace(h.cfg.External.OllamaModel)
	}
	textReq := types.TextGenerationRequest{
		Language:   req.Language,
		Duration:   textDuration,
		Tone:       req.Tone,
		Model:      textModel,
		Prompt:     req.SourceText,
		SourceText: req.SourceText,
		Title:      title,
	}
	if strings.TrimSpace(textReq.Model) == "" {
		textReq.Model = req.Model
	}

	h.log.Info("generating rewritten script from source text", zap.Int("source_len", len(req.SourceText)))
	generated, err := h.generator.GenerateScript(ctx, textReq)
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	rewritten := strings.TrimSpace(generated.Script)
	if rewritten == "" {
		rewritten = strings.TrimSpace(req.SourceText)
	}
	rewritten = types.CleanScript(rewritten)

	if tools.Progress != nil {
		tools.Progress(20, "Splitting script into sentences")
	}

	sentences := splitScriptSentences(rewritten)
	if len(sentences) == 0 {
		sentences = []string{rewritten}
	}
	sentences = groupSentences(sentences, 5)
	if req.SceneCount > 0 && len(sentences) > req.SceneCount {
		sentences = sentences[:req.SceneCount]
	}

	if tools.Progress != nil {
		tools.Progress(30, fmt.Sprintf("Generating %d scenes concurrently", len(sentences)))
	}

	// 2. Concurrently generate images
	packageData := GeneratedScriptPackage{
		SourceText:        req.SourceText,
		RewrittenScript:   rewritten,
		Language:          req.Language,
		Style:             req.Style,
		VisualStyle:       req.VisualStyle,
		Title:             title,
		OutputName:        outputName,
		WordCount:         generated.WordCount,
		EstimatedDuration: generated.EstDuration,
		Scenes:            make([]GeneratedScene, 0, len(sentences)),
		GeneratedAt:       time.Now().UTC(),
	}

	visualStyle := strings.TrimSpace(req.VisualStyle)
	if visualStyle == "" {
		visualStyle = req.Style
	}
	imageModel := strings.TrimSpace(req.Model)
	if imageModel == "" {
		imageModel = "FLUX.1-schnell"
	}
	imagesPerScene := req.ImagesPerScene
	if imagesPerScene < 1 {
		imagesPerScene = 1
	}
	if imagesPerScene > 5 {
		imagesPerScene = 5 // Safety cap
	}

	packageData.Scenes = concurrent.ParallelMap(sentences, 6, func(idx int, sentence string) GeneratedScene {
		sceneID := fmt.Sprintf("scene_%03d", idx+1)
		query := buildVisualQuery(sentence, title, visualStyle, req.Language)
		scene := GeneratedScene{
			ID:    sceneID,
			Index: idx,
			Text:  sentence,
			Query: query,
		}

			// 1. Try Qdrant Vector search for existing image cache match
			var matchedAsset *models.ImageAsset
			if h.realtimeSvc != nil {
				h.log.Info("checking semantic vector store cache for scene", zap.Int("scene_idx", idx), zap.String("query", sentence))
				matchResp, err := h.realtimeSvc.Match(ctx, &realtime.MatchRequest{
					Query:     sentence,
					MediaType: "image",
					MinScore:  0.85,
				})
				if err == nil && matchResp != nil && strings.HasPrefix(matchResp.Status, "instant_match") && matchResp.Asset != nil {
					h.log.Info("semantic image cache HIT! Reusing existing asset", zap.String("asset_id", matchResp.Asset.ID), zap.Float64("score", matchResp.Asset.Score))
					
					// Extract drive file id
					driveFileID := ""
					link := matchResp.Asset.DriveLink
					if strings.Contains(link, "file/d/") {
						parts := strings.Split(link, "file/d/")
						if len(parts) > 1 {
							subparts := strings.Split(parts[1], "/")
							if len(subparts) > 0 {
								driveFileID = subparts[0]
							}
						}
					}
					
					matchedAsset = &models.ImageAsset{
						DriveFileID: driveFileID,
						PathRel:     matchResp.Asset.LocalPath,
						SourceURL:   matchResp.Asset.Source,
						Description: matchResp.Asset.Name,
					}
				}
			}

			// 2. Generate images — either from cache or fresh (imagesPerScene variants)
			// Variant angle suffixes to diversify prompts and avoid YouTube content reuse flagging
			variantSuffixes := []string{
				"",                             // primary — exact prompt
				", close-up view, zoomed in",   // variant 2
				", wide angle, establishing shot", // variant 3
				", side view, different angle",  // variant 4
				", overhead view, birds eye",    // variant 5
			}

			var generatedImages []GeneratedImage

			for variantIdx := 0; variantIdx < imagesPerScene; variantIdx++ {
				variantSentence := sentence
				if variantIdx > 0 && variantIdx < len(variantSuffixes) {
					variantSentence = sentence + variantSuffixes[variantIdx]
				}

				var assetForVariant *models.ImageAsset

				// Only try cache for the primary (variant 0) — variants must be fresh for uniqueness
				if variantIdx == 0 && matchedAsset != nil {
					assetForVariant = matchedAsset
				} else {
					var genErr error
					assetForVariant, genErr = h.imgService.GenerateSmartImage(
						ctx,
						variantSentence,
						title,
						visualStyle,
						[]string{variantSentence},
						[]string{req.Style, req.VisualStyle, req.Language},
						req.Width,
						req.Height,
						imageModel,
						false,
					)
					if genErr != nil {
						h.log.Warn("image variant generation failed",
							zap.Int("scene_idx", idx),
							zap.Int("variant", variantIdx),
							zap.Error(genErr),
						)
						if variantIdx == 0 {
							scene.Error = genErr.Error()
						}
						continue // skip failed variant, continue with next
					}
				}

				if assetForVariant != nil {
					img := generatedImageFromAsset(assetForVariant)
					if img != nil {
						generatedImages = append(generatedImages, *img)
					}
				}
			}

			// Set primary image and full images slice
			if len(generatedImages) > 0 {
				scene.Image = &generatedImages[0]
				scene.Images = generatedImages
			}

			// Voiceover generation moved to unified generation after scene loop


		return scene
	})

	// 3. Generate a single unified voiceover for the entire rewritten script
	if h.voService != nil {
		h.log.Info("generating single unified voiceover for the entire script", zap.String("output_name", outputName))
		
		var destReq *voiceover.DestinationRequest
		dbConn := h.voService.DB()
		if dbConn != nil {
			var folderID string
			err := dbConn.QueryRowContext(ctx, "SELECT folder_id FROM clip_folders WHERE id = 'explainatory' OR group_name = 'explainatory' LIMIT 1").Scan(&folderID)
			if err == nil && folderID != "" {
				destReq = &voiceover.DestinationRequest{
					FolderID:        folderID,
					Group:           "explainatory",
					SubfolderName:   outputName,
					CreateSubfolder: true,
				}
				h.log.Info("found explainatory folder, routing voiceover destination", zap.String("folder_id", folderID), zap.String("subfolder", outputName))
			}
		}

		voRes, voErr := h.voService.GenerateWithDestination(ctx, rewritten, req.Language, outputName, destReq)
		if voErr != nil {
			h.log.Error("unified voiceover generation failed", zap.Error(voErr))
		} else if voRes != nil {
			packageData.Voiceover = &GeneratedVoiceover{
				LocalPath: voRes.Path,
				DriveLink: voRes.DriveLink,
				Voice:     voRes.Voice,
			}
			h.log.Info("unified voiceover generated successfully", zap.String("drive_link", voRes.DriveLink))
		}
	}

	// 4. Translate rewritten script and scenes for all other requested languages in parallel
	packageData.Translations = make(map[string]ScriptTranslation)
	var transMu sync.Mutex
	var transWg sync.WaitGroup

	for _, lang := range req.Languages {
		if lang == req.Language {
			continue // Already base language
		}

		transWg.Add(1)
		go func(lang string) {
			defer transWg.Done()

			h.log.Info("translating script to target language", zap.String("lang", lang))
			translatedScript, transErr := h.generator.TranslateText(ctx, rewritten, lang)
			if transErr != nil {
				h.log.Error("failed to translate script", zap.String("lang", lang), zap.Error(transErr))
				return
			}

			// Translate scene texts concurrently
			translatedScenes := concurrent.ParallelMap(packageData.Scenes, 5, func(idx int, baseScene GeneratedScene) GeneratedScene {
				transSceneText, sceneTransErr := h.generator.TranslateText(ctx, baseScene.Text, lang)
				if sceneTransErr != nil {
					transSceneText = baseScene.Text // Fallback
				}

				return GeneratedScene{
					ID:    baseScene.ID,
					Index: baseScene.Index,
					Text:  transSceneText,
					Query: baseScene.Query,
					Image: baseScene.Image, // Reuse the same image mapping!
					Error: baseScene.Error,
				}
			})

			// Generate unified voiceover for this translation
			var transVo *GeneratedVoiceover
			if h.voService != nil {
				voFilename := fmt.Sprintf("%s-%s", outputName, lang)
				h.log.Info("generating translated voiceover for script", zap.String("lang", lang), zap.String("filename", voFilename))

				var destReq *voiceover.DestinationRequest
				dbConn := h.voService.DB()
				if dbConn != nil {
					var folderID string
					err := dbConn.QueryRowContext(ctx, "SELECT folder_id FROM clip_folders WHERE id = 'explainatory' OR group_name = 'explainatory' LIMIT 1").Scan(&folderID)
					if err == nil && folderID != "" {
						destReq = &voiceover.DestinationRequest{
							FolderID:        folderID,
							Group:           "explainatory",
							SubfolderName:   outputName,
							CreateSubfolder: true,
						}
					}
				}

				voRes, voErr := h.voService.GenerateWithDestination(ctx, translatedScript, lang, voFilename, destReq)
				if voErr != nil {
					h.log.Error("translated voiceover generation failed", zap.String("lang", lang), zap.Error(voErr))
				} else if voRes != nil {
					transVo = &GeneratedVoiceover{
						LocalPath: voRes.Path,
						DriveLink: voRes.DriveLink,
						Voice:     voRes.Voice,
					}
					h.log.Info("translated voiceover generated successfully", zap.String("lang", lang), zap.String("drive_link", voRes.DriveLink))
				}
			}

			transMu.Lock()
			packageData.Translations[lang] = ScriptTranslation{
				Language:        lang,
				RewrittenScript: translatedScript,
				Scenes:          translatedScenes,
				Voiceover:       transVo,
			}
			transMu.Unlock()
		}(lang)
	}
	transWg.Wait()

	videoScenes := make([]VideoScene, 0, len(packageData.Scenes))
	for _, s := range packageData.Scenes {
		link := ""
		if s.Image != nil {
			if s.Image.DriveLink != "" {
				link = s.Image.DriveLink
			} else {
				link = s.Image.LocalPath
			}
		}
		// Collect all variant links
		var allLinks []string
		for _, img := range s.Images {
			if img.DriveLink != "" {
				allLinks = append(allLinks, img.DriveLink)
			} else if img.LocalPath != "" {
				allLinks = append(allLinks, img.LocalPath)
			}
		}
		videoScenes = append(videoScenes, VideoScene{
			Text:       s.Text,
			ImageLink:  link,
			ImageLinks: allLinks,
		})
	}

	if tools.Progress != nil {
		tools.Progress(80, "Writing generated script files")
	}

	outputDir, packageData, err := h.writeGeneratedScriptFiles(packageData, videoScenes)
	if err != nil {
		return nil, fmt.Errorf("failed to write generated script files: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(90, "Uploading to Google Docs")
	}

	doc, err := h.createGeneratedGoogleDoc(ctx, packageData, videoScenes)
	if err != nil {
		return nil, fmt.Errorf("failed to create Google Doc: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(100, "Script generation finished successfully")
	}        // 5. Generate video metadata (YouTube description, tags, translated titles) for all languages
        metadataLanguages := BuildMetadataLanguages(req.Language, req.Languages)
        videoMetadataResult := GenerateVideoMetadata(ctx, h.generator, title, metadataLanguages)

        // Convert to the format expected by the API response
        videoMetadata := make([]map[string]any, len(videoMetadataResult))
        for i, m := range videoMetadataResult {
            videoMetadata[i] = map[string]any{
                "language":    m.Language,
                "title":       m.Title,
                "description": m.Description,
                "tags":        m.Tags,
            }
        }

	// Return final metadata
	return map[string]any{
		"output_dir":    outputDir,
		"doc_id":        doc.ID,
		"doc_url":       doc.URL,
		"docs_url":      doc.URL,
		"markdown_path": packageData.Files.Markdown,
		"json_path":     packageData.Files.JSON,
		"word_count":    packageData.WordCount,
		"est_duration":  packageData.EstimatedDuration,
		"scenes_count":  len(packageData.Scenes),
		"metadata":      videoMetadata,
	}, nil
}

// RegisterJobHandlers registers the handler for script.generate_with_images jobs
func (h *ScriptFlowHandler) RegisterJobHandlers(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobType(jobs.JobTypeSourceScriptGenerate), h.HandleSourceScriptGenerateJob)
		h.log.Info("registered script.generate_with_images job handler")
	}
}
