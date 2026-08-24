package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"flag"
	"fmt"
	"os"
	"strings"
)

func RunDriveCreateFolder(args []string) error {
	fs := flag.NewFlagSet("drive-create-folder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	parent := fs.String("parent", "", "parent Google Drive folder ID")
	name := fs.String("name", "", "folder name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*parent) == "" || strings.TrimSpace(*name) == "" {
		return fmt.Errorf("drive-create-folder: --parent and --name are required")
	}
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	admin, err := cli.BuildDriveAdminForCLI(cli.CmdContext(), cfg, log)
	if err != nil {
		return fmt.Errorf("drive-create-folder: build Drive admin: %w", err)
	}
	parentID := strings.TrimSpace(*parent)
	folderName := strings.TrimSpace(*name)
	id, err := admin.GetOrCreateFolder(cli.CmdContext(), folderName, parentID)
	if err != nil {
		return fmt.Errorf("drive-create-folder: %w", err)
	}
	fmt.Printf("folder_id=%s\nfolder_name=%s\nparent_id=%s\nurl=https://drive.google.com/drive/folders/%s\n", id, folderName, parentID, id)
	return nil
}
