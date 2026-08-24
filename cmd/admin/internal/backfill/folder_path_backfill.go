package backfill

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"flag"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
)

func RunFolderPathBackfill(args []string) error {
	fs := flag.NewFlagSet("folder-path-backfill", flag.ContinueOnError)
	ids := fs.String("asset-ids", "", "comma-separated asset IDs")
	path := fs.String("folder-path", "", "canonical Drive folder path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ids == "" || *path == "" {
		return fmt.Errorf("folder-path-backfill: --asset-ids and --folder-path are required")
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
	svc, err := texttracks.NewFolderPathRepairService(root.Domains.FolderPathWriter)
	if err != nil {
		return err
	}
	for _, id := range cli.SplitCSV(*ids) {
		if err := svc.Repair(cli.CmdContext(), id, *path); err != nil {
			return err
		}
		fmt.Printf("%s: folder_path=%s\n", id, *path)
	}
	return nil
}
