package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// TestOpsJobPayloadsStockFolderIDMatchesLink is the anti-regression gate for
// the 2026-09-30 Milton incident: the tracked stock-only payloads carried a
// hand-transcribed stock_folder_id that differed from the ID inside their own
// stock_folder_link (`…KJku…` vs the caller's `…KQku…` folder). The runtime
// contract already rejects that mismatch (ExpandSegmentStockFolders →
// INVALID_STOCK_FOLDER, pinned by TestMediaModeStockOnlyRejectsMismatchedSegmen
// tDriveFolder), but only when the payload is SUBMITTED — hours after the typo
// was written into the job file and after the run burned real generation work.
//
// This gate reads every tracked ops/jobs payload at TEST time and fails on any
// segment whose folder identity does not agree, so a drifted file can never
// reach a run again.
//
// The check reuses the production parser (urlutil.FolderIDFromDriveLink, the
// same SSOT ExpandSegmentStockFolders validates with) instead of a local
// regex, so this test cannot drift from what the ingress actually accepts.
//
// Only segments carrying BOTH fields are checked: a segment with just one of
// them is normalized by the ingress (the missing side is derived from the
// other), so no divergence is possible there by construction.
func TestOpsJobPayloadsStockFolderIDMatchesLink(t *testing.T) {
	root := filepath.Join("..", "..", "..", "ops", "jobs")
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("tracked ops/jobs tree must exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", root)
	}

	checked := 0
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var envelope struct {
			Items []struct {
				ID           string `json:"id"`
				ScriptParams struct {
					Segments []struct {
						ID              string `json:"id"`
						StockFolderID   string `json:"stock_folder_id"`
						StockFolderLink string `json:"stock_folder_link"`
					} `json:"segments"`
				} `json:"script_params"`
			} `json:"items"`
		}
		if json.Unmarshal(body, &envelope) != nil {
			// Not a generation envelope (catalog, extract, timing …):
			// nothing with stock folder fields to check here.
			return nil
		}
		for _, item := range envelope.Items {
			for _, segment := range item.ScriptParams.Segments {
				folderID := strings.TrimSpace(segment.StockFolderID)
				folderLink := strings.TrimSpace(segment.StockFolderLink)
				if folderID == "" || folderLink == "" {
					continue
				}
				checked++
				if parsed := urlutil.FolderIDFromDriveLink(folderLink); parsed != folderID {
					t.Errorf("%s: item %q segment %q: stock_folder_id %q does not match the ID parsed from stock_folder_link (got %q); the runtime gate (INVALID_STOCK_FOLDER) rejects this payload at submit time — fix the tracked file instead",
						path, item.ID, segment.ID, folderID, parsed)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk ops/jobs: %v", walkErr)
	}
	t.Logf("checked %d segments carrying both stock_folder_id and stock_folder_link", checked)
}
