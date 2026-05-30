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
	"velox/go-master/internal/ml/ollama/types"
)

// HandleSourceScriptGenerateJob processes the background job for script.generate_from_source
func (h *ScriptFlowHandler) HandleSourceScriptGenerateJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	h.log.Info("handling script.generate_from_source job", zap.String("job_id", job.ID))

	var req GenerateFromSourceRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
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

	type result struct {
		index int
		scene GeneratedScene
	}

	resChan := make(chan result, len(sentences))
	sem := make(chan struct{}, 7) // Concurrency semaphore limit = 7
	var wg sync.WaitGroup

	for idx, sentence := range sentences {
		wg.Add(1)
		go func(idx int, sentence string) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire token
			defer func() { <-sem }() // Release token

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
				if err == nil && matchResp != nil && matchResp.Status == "instant_match" && matchResp.Asset != nil {
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

			// 2. Generate if cache missed
			if matchedAsset != nil {
				scene.Image = generatedImageFromAsset(matchedAsset)
			} else {
				asset, genErr := h.imgService.GenerateSmartImage(
					ctx,
					sentence,
					title,
					visualStyle,
					[]string{sentence},
					[]string{req.Style, req.VisualStyle, req.Language},
					req.Width,
					req.Height,
					imageModel,
					false,
				)
				if genErr != nil {
					scene.Error = genErr.Error()
				} else {
					scene.Image = generatedImageFromAsset(asset)
				}
			}

			// 3. Generate voiceover for the scene
			if h.voService != nil {
				voFilename := fmt.Sprintf("%s-scene-%d", outputName, idx+1)
				h.log.Info("generating voiceover for scene", zap.Int("scene_idx", idx))
				voRes, voErr := h.voService.Generate(ctx, sentence, req.Language, voFilename)
				if voErr != nil {
					h.log.Error("scene voiceover generation failed", zap.Int("scene_idx", idx), zap.Error(voErr))
					scene.Voiceover = &GeneratedVoiceover{
						Error: voErr.Error(),
					}
				} else if voRes != nil {
					scene.Voiceover = &GeneratedVoiceover{
						LocalPath: voRes.Path,
						DriveLink: voRes.DriveLink,
						Voice:     voRes.Voice,
					}
				}
			}

			resChan <- result{index: idx, scene: scene}
		}(idx, sentence)
	}

	wg.Wait()
	close(resChan)

	scenesList := make([]GeneratedScene, len(sentences))
	for res := range resChan {
		scenesList[res.index] = res.scene
	}
	packageData.Scenes = scenesList

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
		videoScenes = append(videoScenes, VideoScene{
			Text:      s.Text,
			ImageLink: link,
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
	}, nil
}

// RegisterJobHandlers registers the handler for script.generate_from_source jobs
func (h *ScriptFlowHandler) RegisterJobHandlers(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobType(jobs.JobTypeSourceScriptGenerate), h.HandleSourceScriptGenerateJob)
		h.log.Info("registered script.generate_from_source job handler")
	}
}
