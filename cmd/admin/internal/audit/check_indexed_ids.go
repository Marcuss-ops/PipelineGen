package audit

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

func RunCheckIndexedIds(args []string) error {
	ids := []string{
		"1pwiVpdpgAEI9FWhPRxRp_sXh1CQQbdVm",
		"1uNj528n86OA8oPIVXErSz3SJSedpk761",
		"1DsVCkLdDzy2SMYq2XukAH8NhXk02tOCC",
		"10FlTug0bPNeOOjykNvwQ4qLXLSSUdbwr",
		"1zrMoov4UZRcqGE0Sw6bLywttjrCqqZnt",
		"1catJ78Ve0F9QxgeJfMhdp6bflDS4TLjl",
		"1oi3TWTyMmQbAwrSY6ry6sYE0xR5gEuY6",
		"11UkYi35l01iP5wFzG7TPdFQW5d_hr0M0",
		"1AF4zJlndCX_G-geEA_Q94xMMjMiJ1dik",
		"19_-yDyTPdaED9O3e2CLzzal_TIeRtZuI",
		"1age5XewoBELovLIp3g0vmcPi3xz2S0o5",
		"1lohTy6IWpsQaGm9kBywsmpRkXclkfqnG",
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
	indexedCount := 0

	fmt.Println("Checking indexed status of the provided Drive IDs:")
	for i, id := range ids {
		asset, err := root.Repos.ClipsRepo.GetClip(ctx, id)
		if err != nil {
			fmt.Printf("[%d] Error checking ID %s: %v\n", i+1, id, err)
			continue
		}
		if asset != nil {
			fmt.Printf("[%d] INDEXED: ID %s -> Name: %q, Filename: %q\n", i+1, id, asset.Name, asset.Filename)
			indexedCount++
		} else {
			fmt.Printf("[%d] NOT INDEXED: ID %s\n", i+1, id)
		}
	}

	fmt.Printf("\nTotal checked: %d, Total indexed: %d, Missing: %d\n", len(ids), indexedCount, len(ids)-indexedCount)
	return nil
}
