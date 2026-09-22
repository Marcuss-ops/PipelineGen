// cmd/admin/internal/drive/search_drive.go — search-drive: raw Drive
// query search over the account.
//
// Previously this command was a dev utility with a hardcoded folder ID
// and a context.Background(); it is now parameterised and fails closed
// when no query is supplied. For "what is inside this folder?" use
// `drive-ls`; for a recursive folder walk use `list-drive-folder`.
//
// Usage:
//
//	go run ./cmd/admin search-drive --query "name contains 'beta' and trashed=false" [--json]
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

type searchDriveResult struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Link     string `json:"web_view_link"`
	Trashed  bool   `json:"-"`
}

func RunSearchDrive(args []string) error {
	fs := flag.NewFlagSet("search-drive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	query := fs.String("query", "", "raw Google Drive query, e.g. \"name contains 'beta' and trashed=false\"")
	jsonOut := fs.Bool("json", false, "Emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	q := strings.TrimSpace(*query)
	if q == "" {
		return fmt.Errorf("search-drive: --query is required (raw Google Drive query, e.g. \"name contains 'beta' and trashed=false\")")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return err
	}
	defer rootCleanup()
	if root == nil || root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("search-drive: Drive reader port is not available")
	}

	ctx := cli.CmdContext()
	files, err := root.Drive.Reader.SearchFiles(ctx, q)
	if err != nil {
		return fmt.Errorf("search-drive: %w", err)
	}

	results := make([]searchDriveResult, 0, len(files))
	for _, f := range files {
		results = append(results, searchDriveResult{
			Name:     f.Name,
			ID:       f.ID,
			MimeType: f.MimeType,
			Size:     f.Size,
			Link:     f.WebViewLink,
		})
	}

	if *jsonOut {
		b, mErr := json.MarshalIndent(results, "", "  ")
		if mErr != nil {
			return fmt.Errorf("search-drive: marshal JSON: %w", mErr)
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("Query: %s\nMatches: %d\n\n", q, len(results))
	for _, r := range results {
		fmt.Printf("  %-40s  %10d B  %-28s  %s\n", r.Name, r.Size, r.MimeType, r.ID)
	}
	return nil
}
