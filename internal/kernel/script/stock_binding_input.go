package script

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// StockBindingInput is the caller-facing direct stock contract.
type StockBindingInput struct {
	Index      int     `json:"index"`
	SceneID    string  `json:"scene_id,omitempty"`
	SegmentID  string  `json:"segment_id,omitempty"`
	AssetID    string  `json:"asset_id,omitempty"`
	Name       string  `json:"name,omitempty"`
	Source     string  `json:"source,omitempty"`
	DriveLink  string  `json:"drive_link,omitempty"`
	FolderID   string  `json:"folder_id,omitempty"`
	FolderLink string  `json:"folder_link,omitempty"`
	Score      float64 `json:"score,omitempty"`
	Fallback   bool    `json:"fallback"`
	StartMs    int64   `json:"start_ms,omitempty"`
	EndMs      int64   `json:"end_ms,omitempty"`
}

// ExpandSegmentStockFolders materializes the public per-segment Drive-folder
// shorthand into the internal stock binding shape. Explicit stock_bindings
// remain authoritative for backwards compatibility; when they are absent,
// every segment with a folder is bound to the scene at the same index.
func ExpandSegmentStockFolders(item *GenerationItemV2) error {
	if item == nil || len(item.ScriptParams.Segments) == 0 || len(item.Output.StockBindings) > 0 {
		return nil
	}

	bindings := make([]StockBindingInput, 0, len(item.ScriptParams.Segments))
	for index, segment := range item.ScriptParams.Segments {
		folderID := strings.TrimSpace(segment.StockFolderID)
		folderLink := strings.TrimSpace(segment.StockFolderLink)
		if folderID == "" && folderLink == "" {
			continue
		}
		if folderID == "" {
			folderID = urlutil.FolderIDFromDriveLink(folderLink)
		}
		if folderID == "" {
			return stockFolderError(item, index, "stock_folder_link must be a Google Drive folder URL")
		}
		if folderLink == "" {
			folderLink = "https://drive.google.com/drive/folders/" + folderID
		}
		if parsedID := urlutil.FolderIDFromDriveLink(folderLink); parsedID != folderID {
			return stockFolderError(item, index, "stock_folder_link does not match stock_folder_id")
		}

		segmentID := strings.TrimSpace(segment.ID)
		if segmentID == "" {
			segmentID = fmt.Sprintf("segment-%d", index+1)
		}
		bindings = append(bindings, StockBindingInput{
			Index:      index,
			SceneID:    fmt.Sprintf("scene-%d", index),
			SegmentID:  segmentID,
			FolderID:   folderID,
			FolderLink: folderLink,
			Source:     "drive",
			StartMs:    0,
			EndMs:      5000,
		})
	}

	if len(bindings) > 0 {
		item.Output.StockBindings = bindings
		item.Output.StockEnabled = ToggleEnabled
	}
	return nil
}

func stockFolderError(item *GenerationItemV2, index int, message string) error {
	return &PayloadValidationError{
		Code:      "INVALID_STOCK_FOLDER",
		Message:   fmt.Sprintf("%s: script_params.segments[%d]: %s", item.ID, index, message),
		Stage:     "request.validation",
		Retryable: false,
	}
}
