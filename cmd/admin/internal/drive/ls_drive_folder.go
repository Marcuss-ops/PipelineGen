// cmd/admin/internal/drive/ls_drive_folder.go — drive-ls: neutral,
// read-only listing of a Drive folder's children (files AND folders).
//
// Why this command exists: `list-drive-folder` walks the hierarchy but
// prints ONLY folders, and `search-drive` runs a raw query. Neither
// answers the everyday question "what is inside this folder?". drive-ls
// is the canonical answer — it uses the existing `drive.Reader` port
// (ListFiles) and never writes to Drive or the database.
//
// Usage:
//
//	go run ./cmd/admin drive-ls [--folder <ID>] [--recursive] [--max-depth N]
//	                            [--files-only] [--json]
//
//	--folder      Drive folder ID to list (default: cfg.Drive.RootFolder()
//	              or the canonical media root)
//	--recursive   Also list subfolders (bounded by --max-depth)
//	--max-depth   Maximum recursion depth when --recursive is set (default 5)
//	--files-only  Hide folder entries (subfolders are still walked)
//	--json        Emit machine-readable JSON
//
// godlike/07 NO-FAKE-AVAILABILITY: read-only by construction — the only
// Drive calls are Files.List (via Reader) + a best-effort Files.Get for
// the root label.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// driveListEntry is one child of a Drive folder. Type is the human-facing
// discriminator ("folder" | "file"); MimeType keeps the raw Drive value.
type driveListEntry struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Link     string `json:"web_view_link"`
	Path     string `json:"path"`
	Depth    int    `json:"depth"`
}

// driveLSReport is the --json payload.
type driveLSReport struct {
	Root     string           `json:"root"`
	RootName string           `json:"root_name,omitempty"`
	Folders  int              `json:"folders"`
	Files    int              `json:"files"`
	Entries  []driveListEntry `json:"entries"`
}

func RunDriveLS(args []string) error {
	fs := flag.NewFlagSet("drive-ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folder := fs.String("folder", "", "Drive folder ID to list (default: cfg.Drive.RootFolder() or the canonical media root)")
	recursive := fs.Bool("recursive", false, "Also list subfolders (bounded by --max-depth)")
	maxDepth := fs.Int("max-depth", 5, "Maximum recursion depth when --recursive is set")
	filesOnly := fs.Bool("files-only", false, "Hide folder entries (subfolders are still walked)")
	jsonOut := fs.Bool("json", false, "Emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *maxDepth < 0 {
		return fmt.Errorf("drive-ls: --max-depth must be >= 0")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	rootID := strings.TrimSpace(*folder)
	if rootID == "" && cfg != nil {
		rootID = strings.TrimSpace(cfg.Drive.RootFolder())
	}
	if rootID == "" {
		rootID = config.DefaultMediaRootFolderID
	}
	if rootID == "" {
		return fmt.Errorf("drive-ls: --folder is required (and no media root is configured)")
	}

	admin, err := cli.BuildDriveAdminForCLI(cli.CmdContext(), cfg, log)
	if err != nil {
		return fmt.Errorf("drive-ls: init Drive client: %w", err)
	}

	ctx, cancel := context.WithTimeout(cli.CmdContext(), 5*time.Minute)
	defer cancel()

	// Best-effort root label. A failure here must not abort the listing.
	rootName, nameErr := admin.GetFolderName(ctx, rootID)
	if nameErr != nil {
		log.Warn("drive-ls: could not resolve root folder name", zap.String("folder_id", rootID), zap.Error(nameErr))
		rootName = ""
	}

	entries, err := walkDriveFolder(ctx, admin, rootID, "", 0, *recursive, *maxDepth, *filesOnly, log)
	if err != nil {
		return fmt.Errorf("drive-ls: %w", err)
	}
	sortDriveListEntries(entries)

	if *jsonOut {
		printDriveLSJSON(rootID, rootName, entries)
		return nil
	}
	printDriveLSText(rootID, rootName, entries, *recursive, *maxDepth)
	return nil
}

// walkDriveFolder lists folderID's children and, when recursive, descends
// into each subfolder up to maxDepth. A transient error on a subfolder is
// logged and skipped rather than failing the whole listing; an error on
// the root folder is propagated.
func walkDriveFolder(
	ctx context.Context,
	reader drive.Reader,
	folderID, parentPath string,
	depth int,
	recursive bool,
	maxDepth int,
	filesOnly bool,
	log *zap.Logger,
) ([]driveListEntry, error) {
	files, err := reader.ListFiles(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("list Drive folder %s: %w", folderID, err)
	}

	type childFolder struct{ id, path string }
	children := make([]childFolder, 0)
	entries := make([]driveListEntry, 0, len(files))

	for _, f := range files {
		isFolder := f.MimeType == driveFolderMimeType
		childPath := f.Name
		if parentPath != "" {
			childPath = parentPath + "/" + f.Name
		}
		if isFolder && recursive && depth < maxDepth {
			children = append(children, childFolder{id: f.ID, path: childPath})
		}
		if isFolder && filesOnly {
			continue
		}
		entryType := "file"
		link := f.WebViewLink
		if isFolder {
			entryType = "folder"
			if link == "" {
				link = "https://drive.google.com/drive/folders/" + f.ID
			}
		}
		entries = append(entries, driveListEntry{
			Name:     f.Name,
			ID:       f.ID,
			Type:     entryType,
			MimeType: f.MimeType,
			Size:     f.Size,
			Link:     link,
			Path:     childPath,
			Depth:    depth,
		})
	}

	for _, c := range children {
		sub, subErr := walkDriveFolder(ctx, reader, c.id, c.path, depth+1, recursive, maxDepth, filesOnly, log)
		if subErr != nil {
			if log != nil {
				log.Warn("drive-ls: skipping unreadable subfolder", zap.String("folder_id", c.id), zap.Error(subErr))
			}
			continue
		}
		entries = append(entries, sub...)
	}
	return entries, nil
}

// sortDriveListEntries orders entries the way a filesystem listing reads:
// folders before files, then by path case-insensitively.
func sortDriveListEntries(entries []driveListEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "folder"
		}
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
}

// summarizeDriveList returns (folders, files) counts.
func summarizeDriveList(entries []driveListEntry) (int, int) {
	folders, files := 0, 0
	for _, e := range entries {
		if e.Type == "folder" {
			folders++
		} else {
			files++
		}
	}
	return folders, files
}

// formatDriveListEntry renders a single entry for the human-readable output.
func formatDriveListEntry(e driveListEntry) string {
	indent := strings.Repeat("  ", e.Depth)
	if e.Type == "folder" {
		return fmt.Sprintf("%s📁 %-40s  %s", indent, e.Path, e.ID)
	}
	return fmt.Sprintf("%s📄 %-40s  %10d B  %-28s  %s", indent, e.Path, e.Size, e.MimeType, e.ID)
}

func printDriveLSText(rootID, rootName string, entries []driveListEntry, recursive bool, maxDepth int) {
	label := rootID
	if rootName != "" {
		label = fmt.Sprintf("%s (%s)", rootName, rootID)
	}
	fmt.Printf("=== Drive LS: %s ===\n", label)
	if recursive {
		fmt.Printf("Recursive: true (max depth %d)\n", maxDepth)
	}
	fmt.Println()
	for _, e := range entries {
		fmt.Println(formatDriveListEntry(e))
	}
	folders, files := summarizeDriveList(entries)
	fmt.Printf("\nTotal: %d entries (%d folder(s), %d file(s))\n", len(entries), folders, files)
}

func printDriveLSJSON(rootID, rootName string, entries []driveListEntry) {
	folders, files := summarizeDriveList(entries)
	report := driveLSReport{
		Root:     rootID,
		RootName: rootName,
		Folders:  folders,
		Files:    files,
		Entries:  entries,
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive-ls: marshal JSON: %v\n", err)
		return
	}
	fmt.Println(string(b))
}
