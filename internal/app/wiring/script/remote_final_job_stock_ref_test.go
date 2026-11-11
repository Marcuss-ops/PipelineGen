package script

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestFinalJobStockDriveReference pins the exact reference the Master receives
// for Drive stock: a directly fetchable Drive HTTPS source and the bare content
// SHA as asset_id. A display prefix here makes prefetch and the worker lease
// diverge (r37 v2, cached asset not found); the test refuses its return.
func TestFinalJobStockDriveReference(t *testing.T) {
	const (
		driveID = "14VFCfOpMX5lcDKcOPPZuXUntaUUUlh0H"
		sha     = "2702d270015095cc853972fa58691ec368fe0339c6531761b05f2ce2a80be9a6"
		size    = int64(3152434)
	)
	saved := &asset.Asset{Duration: 3500 * time.Millisecond}
	saved.SetDriveFileID(driveID)
	// Uppercase input proves the runtime identity is normalized to lowercase.
	saved.SetContentHash(strings.ToUpper(sha))

	ref := finalJobStockDriveReference(driveID, saved, size)

	if got, want := ref["asset_id"], sha; got != want {
		t.Fatalf("asset_id = %v, want the bare lowercase SHA %q", got, want)
	}
	if strings.HasPrefix(ref["asset_id"].(string), "stock-sha256-") {
		t.Fatal("asset_id must not carry the media-registry display prefix")
	}
	if got := ref["sha256"]; got != sha {
		t.Fatalf("sha256 = %v, want %q", got, sha)
	}
	if got, want := ref["drive_file_id"], driveID; got != want {
		t.Fatalf("drive_file_id = %v, want %q", got, want)
	}
	if got, want := ref["url"], "velox-drive://"+driveID; got != want {
		t.Fatalf("url = %v, want the Drive identity %q", got, want)
	}
	if got, want := ref["size_bytes"], size; got != want {
		t.Fatalf("size_bytes = %v, want %d", got, want)
	}
	if got, want := ref["duration_ms"], int64(3500); got != want {
		t.Fatalf("duration_ms = %v, want %d", got, want)
	}

	source, ok := ref["source_uri"].(string)
	if !ok || source == "" {
		t.Fatalf("source_uri = %v, want a non-empty HTTPS URL", ref["source_uri"])
	}
	parsed, err := url.Parse(source)
	if err != nil {
		t.Fatalf("parse source_uri %q: %v", source, err)
	}
	if parsed.Scheme != "https" || parsed.Host != "drive.usercontent.google.com" {
		t.Fatalf("source_uri = %q, want https://drive.usercontent.google.com/...", source)
	}
	if got := parsed.Query().Get("id"); got != driveID {
		t.Fatalf("source_uri id = %q, want %q", got, driveID)
	}
	if got := parsed.Query().Get("export"); got != "download" {
		t.Fatalf("source_uri export = %q, want download", got)
	}
}
