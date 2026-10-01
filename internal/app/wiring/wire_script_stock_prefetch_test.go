package wiring

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestScriptStockPrefetchURLsSkipsFolderBindings(t *testing.T) {
	urls, skipped := scriptStockPrefetchURLs([]scriptpkg.StockBindingInput{
		{FolderID: "stock-folder", FolderLink: "https://drive.google.com/drive/folders/stock-folder"},
		{DriveLink: "https://drive.google.com/file/d/individual-stock-file/view"},
	})
	if skipped != 1 {
		t.Fatalf("skipped folder bindings = %d, want 1", skipped)
	}
	if len(urls) != 1 || urls[0] != "https://drive.google.com/file/d/individual-stock-file/view" {
		t.Fatalf("prefetch URLs = %#v, want only the individual file", urls)
	}
}

func TestScriptStockPrefetchURLsDoesNotExpandFolderID(t *testing.T) {
	urls, skipped := scriptStockPrefetchURLs([]scriptpkg.StockBindingInput{{FolderID: "stock-folder"}})
	if len(urls) != 0 || skipped != 1 {
		t.Fatalf("URLs/skipped = %#v/%d, want nil/1", urls, skipped)
	}
}
