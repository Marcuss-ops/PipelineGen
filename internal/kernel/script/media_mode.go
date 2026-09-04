package script

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// MediaMode is the explicit media contract for a generation item.
type MediaMode string

const (
	MediaModeStockOnly MediaMode = "stock_only"
	MediaModeClipOnly  MediaMode = "clip_only"
	MediaModeMixed     MediaMode = "mixed"
)

func (m MediaMode) Valid() bool {
	return m == "" || m == MediaModeStockOnly || m == MediaModeClipOnly || m == MediaModeMixed
}

func mediaModeError(item GenerationItemV2, code, message string) error {
	return &PayloadValidationError{Code: code, Message: fmt.Sprintf("%s: %s", item.ID, message), Stage: "request.validation", Retryable: false}
}

func stockReferences(item GenerationItemV2) bool {
	return item.Output.StockEnabled.AsBool() || len(item.Output.StockBindings) > 0
}

func clipReferences(item GenerationItemV2) bool {
	return item.Source.Type == SourceClips || len(item.Source.ClipIDs) > 0
}

// validateMediaMode enforces the explicit stock/clip separation before the
// request reaches the queue or any provider.
func validateMediaMode(item GenerationItemV2, ref string) error {
	mode := item.MediaMode
	if !mode.Valid() {
		return mediaModeError(item, "INVALID_MEDIA_MODE", ref+": media_mode must be stock_only, clip_only, or mixed")
	}
	stock, clips := stockReferences(item), clipReferences(item)
	switch mode {
	case MediaModeStockOnly:
		if clips {
			return mediaModeError(item, "MEDIA_MODE_CONFLICT", ref+": stock_only cannot use clip source or clip IDs")
		}
		if item.Source.Type != SourceText && item.Source.Type != SourceResearch {
			return mediaModeError(item, "MEDIA_MODE_CONFLICT", ref+": stock_only requires source.type=text or research")
		}
		if !item.Output.StockEnabled.AsBool() || len(item.Output.StockBindings) == 0 {
			return mediaModeError(item, "STOCK_ONLY_BINDINGS_REQUIRED", ref+": stock_only requires stock_enabled=enabled and stock_bindings")
		}
		for i, binding := range item.Output.StockBindings {
			if strings.TrimSpace(binding.AssetID) != "" || strings.TrimSpace(binding.DriveLink) != "" {
				return mediaModeError(item, "STOCK_ONLY_FILE_REFERENCE_FORBIDDEN", fmt.Sprintf("%s: stock_bindings[%d] cannot contain asset_id or drive_link", ref, i))
			}
			folderID, folderLink := strings.TrimSpace(binding.FolderID), strings.TrimSpace(binding.FolderLink)
			if folderID == "" {
				return mediaModeError(item, "STOCK_ONLY_FOLDER_ID_REQUIRED", fmt.Sprintf("%s: stock_bindings[%d] requires folder_id", ref, i))
			}
			if folderLink == "" {
				return mediaModeError(item, "STOCK_ONLY_FOLDER_LINK_REQUIRED", fmt.Sprintf("%s: stock_bindings[%d] requires folder_link", ref, i))
			}
			if urlutil.FolderIDFromDriveLink(folderLink) != folderID {
				return mediaModeError(item, "STOCK_ONLY_FOLDER_MISMATCH", fmt.Sprintf("%s: stock_bindings[%d] folder_link does not match folder_id", ref, i))
			}
		}
	case MediaModeClipOnly:
		if item.Source.Type != SourceClips || len(item.Source.ClipIDs) == 0 {
			return mediaModeError(item, "CLIP_ONLY_SOURCE_REQUIRED", ref+": clip_only requires source.type=clips with clip_ids")
		}
		if stock {
			return mediaModeError(item, "CLIP_ONLY_STOCK_REFERENCE_FORBIDDEN", ref+": clip_only cannot contain stock references")
		}
	case MediaModeMixed:
		return mediaModeError(item, "MEDIA_MODE_UNSUPPORTED", ref+": media_mode=mixed is not supported")
	case "":
		if stock && clips {
			return mediaModeError(item, "MEDIA_MODE_REQUIRED_FOR_MIXED_REFERENCES", ref+": media_mode is required when clip and stock references are combined")
		}
	}
	return nil
}
