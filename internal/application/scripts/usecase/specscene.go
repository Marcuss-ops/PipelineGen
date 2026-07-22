// Package usecase — entity extraction and image enrichment.
//
// specscene.go owns the entity-extraction and image-enrichment pipeline:
// EnrichSpecialNamesWithImages, enrichSingleEntity, ExtractScriptEntities,
// and their helpers. Extracted from flow_helpers.go
// (July 2026, LONG-FILES-SPLIT-2026-07-06).
package usecase

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Entity image enrichment ─────────────────────────────────────────────────

// EnrichSpecialNamesWithImages searches for or generates images for each special name.
func EnrichSpecialNamesWithImages(ctx context.Context, svc ClipServices, specialNames []string) []ScriptEntityImage {
	if svc.ImgSvc == nil || len(specialNames) == 0 {
		return nil
	}

	var mu sync.Mutex
	results := make([]ScriptEntityImage, 0, len(specialNames))
	group, groupCtx := concurrent.WithContext(ctx)

	for _, name := range specialNames {
		name := strings.TrimSpace(name)
		if name == "" || len(name) < 2 {
			continue
		}

		group.Go("entity-image-"+name, func() error {
			img := enrichSingleEntity(groupCtx, svc, name)
			mu.Lock()
			results = append(results, img)
			mu.Unlock()
			return nil
		})
	}

	_ = group.Wait()
	return results
}

func enrichSingleEntity(ctx context.Context, svc ClipServices, name string) ScriptEntityImage {
	img := ScriptEntityImage{EntityName: name}

	if isLikelyNonEntityWord(name) {
		if svc.Logger != nil {
			svc.Logger.Debug("Skipping non-entity word", zap.String("name", name))
		}
		img.Error = "skipped: not a named entity"
		return img
	}

	entityCtx, entityCancel := context.WithTimeout(ctx, 90*time.Second)
	defer entityCancel()

	asset, err := svc.ImgSvc.SearchAndDownload(entityCtx, name, name, name, "en")
	if err == nil && asset != nil {
		populateEntityImage(&img, asset, "")
		return img
	}
	if svc.Logger != nil {
		svc.Logger.Info("Web search found no image for entity, trying AI generation",
			zap.String("entity", name),
			zap.Error(err),
		)
	}

	aiCtx, aiCancel := context.WithTimeout(ctx, 60*time.Second)
	defer aiCancel()
	aiAsset, aiErr := svc.ImgSvc.GenerateSceneImage(aiCtx, name,
		"Portrait or representative image of "+name,
		"realistic",
		[]string{"Portrait or representative image of " + name},
		[]string{"entity", name}, 1024, 1024, "", false)
	if aiErr == nil && aiAsset != nil {
		populateEntityImage(&img, aiAsset, "ai")
		return img
	}

	if svc.Logger != nil {
		svc.Logger.Warn("Both web search and AI generation failed for entity",
			zap.String("entity", name),
			zap.Error(aiErr),
		)
	}
	img.Error = "no image found (web search and AI generation both failed)"
	return img
}

func isLikelyNonEntityWord(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || len(name) < 3 {
		return true
	}
	first := name[0]
	if first >= 'a' && first <= 'z' {
		return true
	}
	return false
}

func populateEntityImage(img *ScriptEntityImage, imgAsset *asset.ImageAsset, forcedSource string) {
	img.ImageHash = imgAsset.Hash
	img.ImageURL = imgAsset.SourceURL
	img.PathRel = imgAsset.PathRel
	img.Description = imgAsset.Description
	if forcedSource != "" {
		img.Source = forcedSource
	} else {
		img.Source = extractSourceFromMeta(imgAsset.MetadataJSON)
	}
	fileID := strings.TrimSpace(imgAsset.DriveFileID)
	if fileID != "" {
		img.DriveLink = drive.FileURLFromID(fileID)
	}
}

func extractSourceFromMeta(metaJSON string) string {
	if metaJSON == "" || metaJSON == "{}" {
		return "web"
	}
	if d := asset.DefaultProviderRegistry().Match(strings.ToLower(metaJSON)); d != nil {
		return string(d.ID)
	}
	return "web"
}

// ── Entity extraction ───────────────────────────────────────────────────────

// ExtractScriptEntities extracts entities from a script text and returns
// the JSON-serialized entity analysis.
func ExtractScriptEntities(ctx context.Context, extractor EntityScriptExtractor, script string, model string) (string, error) {
	if extractor == nil {
		return "", nil
	}

	segments := textutil.SplitScriptSentences(script)
	if len(segments) == 0 {
		script = strings.TrimSpace(script)
		if script != "" {
			segments = []string{script}
		}
	}
	if len(segments) > 12 {
		segments = sliceutil.GroupSentences(segments, 4)
	}

	analysis, err := extractor.ExtractEntitiesFromScriptWithModel(ctx, segments, 12, model)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(analysis)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
