// driveupload uploads local files into a Google Drive folder using the
// platform's Google OAuth client (credentials.json + token.json, with the
// same refreshing/static fallback the pipeline uses), then prints the
// viewable links.
//
// Usage:
//
//	go run ./cmd/driveupload --folder <driveFolderId> --file <path> [--file <path> ...]
//
// Parented into the folder; if the folder id is empty the file lands in the
// drive root (and the tool says so).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

func main() {
	var (
		folderID = flag.String("folder", "", "target Drive folder id (empty = drive root)")
		creds    = flag.String("credentials", "credentials.json", "OAuth credentials file path")
		token    = flag.String("token", "token.json", "OAuth token file path")
	)
	flag.Var(&uploadFiles, "file", "local file to upload (repeatable)")
	flag.Parse()

	files := uploadFiles.values
	if len(files) == 0 {
		fatal(fmt.Errorf("no --file given"))
	}

	ctx := context.Background()
	client, err := platformdrive.NewGoogleHTTPClient(ctx, *creds, *token, drive.DriveScope)
	if err != nil {
		fatal(fmt.Errorf("google http client: %w", err))
	}
	svc, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fatal(fmt.Errorf("drive service: %w", err))
	}

	if *folderID != "" {
		folder, err := svc.Files.Get(*folderID).
			Fields("id, name, webViewLink").
			SupportsAllDrives(true).
			Do()
		if err != nil {
			fatal(fmt.Errorf("folder %s not accessible: %w", *folderID, err))
		}
		fmt.Printf("target folder: %s (%s)\n", folder.Name, folder.WebViewLink)
	}

	for _, path := range files {
		link, err := uploadOne(ctx, svc, path, *folderID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", path, err)
			fatal(err)
		}
		fmt.Printf("UPLOADED %s -> %s\n", filepath.Base(path), link)
	}
}

func uploadOne(ctx context.Context, svc *drive.Service, path, folderID string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	name := filepath.Base(path)
	file := &drive.File{
		Name: name,
	}
	if folderID != "" {
		file.Parents = []string{folderID}
	}

	uploaded, err := svc.Files.Create(file).
		Media(f).
		SupportsAllDrives(true).
		Fields("id, webViewLink, size").
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}

	// Best-effort permission grant so the folder's viewers can watch without
	// extra clicks (anyone with the link already can, when the folder shares
	// inherit; this is for files uploaded into folders with restricted
	// inheritance).
	_, permErr := svc.Permissions.Create(uploaded.Id, &drive.Permission{
		Type: "anyone",
		Role: "reader",
	}).
		SupportsAllDrives(true).
		Context(ctx).
		Do()
	if permErr != nil {
		fmt.Fprintf(os.Stderr, "note: could not add link-sharing permission on %s (%v); the folder sharing applies\n", name, permErr)
	}

	return uploaded.WebViewLink, nil
}

type multiFlag struct{ values []string }

var uploadFiles multiFlag

func (m *multiFlag) String() string { return strings.Join(m.values, ",") }
func (m *multiFlag) Set(v string) error {
	if v == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		for _, line := range strings.Fields(string(b)) {
			m.values = append(m.values, line)
		}
		return nil
	}
	m.values = append(m.values, v)
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "driveupload: %v\n", err)
	os.Exit(1)
}
