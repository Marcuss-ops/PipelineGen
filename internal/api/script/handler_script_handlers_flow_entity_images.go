package script

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database/drive"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// ScriptEntityImage represents an enriched image for a named entity.
type ScriptEntityImage struct {
	EntityName  string `json:"entity_name"`
	ImageHash   string `json:"image_hash,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	PathRel     string `json:"path_rel,omitempty"`
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`
	Error       string `json:"error,omitempty"`
}

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

	asset, err := svc.ImgSvc.SearchAndDownload(entityCtx, name, name, name, "en", nil)
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
	aiAsset, aiErr := svc.ImgSvc.GenerateSmartImage(aiCtx, name,
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

func populateEntityImage(img *ScriptEntityImage, asset *models.ImageAsset, forcedSource string) {
	img.ImageHash = asset.Hash
	img.ImageURL = asset.SourceURL
	img.PathRel = asset.PathRel
	img.Description = asset.Description
	if forcedSource != "" {
		img.Source = forcedSource
	} else {
		img.Source = extractSourceFromMeta(asset.MetadataJSON)
	}
	fileID := strings.TrimSpace(asset.DriveFileID)
	if fileID != "" {
		img.DriveLink = drive.FileURLFromID(fileID)
	}
}

func extractSourceFromMeta(metaJSON string) string {
	if metaJSON == "" || metaJSON == "{}" {
		return "web"
	}
	meta := strings.ToLower(metaJSON)
	switch {
	case strings.Contains(meta, "\"wikipedia\""):
		return "wikipedia"
	case strings.Contains(meta, "\"searxng\""):
		return "searxng"
	case strings.Contains(meta, "\"duckduckgo\""):
		return "duckduckgo"
	default:
		return "web"
	}
}
