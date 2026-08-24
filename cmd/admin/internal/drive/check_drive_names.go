package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

func RunCheckDriveNames(args []string) error {
	ids := []string{
		"1pwiVpdpgAEI9FWhPRxRp_sXh1CQQbdVm",
		"1ZqJsp8vTtyF3tR1HGbe8FLVO7mJ7dfUf",
		"1TiYGYXe0roWUYV-uLlnBEnLBFQTylxSo",
		"1sqRxw93fNOrp38YHMktrdXAFsPHiWcBg",
		"1i4XqIkrLmN052Qq22A7q3Ubtpdqfns6m",
		"1SveY9c6leuIieCcXbOYJsTu6y0-g8vRQ",
		"1AJ4rdYsWZ--OVVAEDptcnAPUCVnGdXta",
		"1tPCml1MmM1bVAQTSr-Q1Egjj1JMt_nJT",
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

	ctx := context.Background()

	fmt.Println("Checking real Google Drive filenames for the 8 IDs:")
	for _, id := range ids {
		meta, err := root.Drive.Reader.GetFileMeta(ctx, id)
		if err != nil {
			fmt.Printf("ID %s: Error: %v\n", id, err)
			continue
		}
		fmt.Printf("ID %s: Name = %q, MimeType = %q\n", id, meta.Name, meta.MimeType)
	}

	return nil
}
