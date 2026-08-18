package main

import (
	"flag"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// runTextTracksAlignCues backfills timed cues for already-translated
// transcript tracks by distributing each translated full-text across the
// source-language cue windows (cuesWithText). The source-language cues are
// read from the DB (not a VTT file), so it works for any asset id
// (local/manual/youtube), unlike transcript-cues-backfill which is VTT +
// `yt_`-id scoped.
//
// Usage:
//
//	pipelinegen-admin text-tracks-align-cues \
//	    --asset-ids=manual_9b8daf320154 --source-lang=en
func runTextTracksAlignCues(args []string) error {
	fs := flag.NewFlagSet("text-tracks-align-cues", flag.ContinueOnError)
	ids := fs.String("asset-ids", "", "comma-separated clip asset IDs")
	sourceLang := fs.String("source-lang", "en", "BCP-47 source language whose cues provide the timing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	assetIDs := splitCSV(*ids)
	if len(assetIDs) == 0 {
		return fmt.Errorf("text-tracks-align-cues: --asset-ids is required")
	}
	if *sourceLang == "" {
		return fmt.Errorf("text-tracks-align-cues: --source-lang is required")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return err
	}
	defer rootCleanup()

	svc, err := texttracks.NewCueRepairService(root.Domains.CueWriter)
	if err != nil {
		return err
	}

	for _, id := range assetIDs {
		_, srcCues, err := root.Repos.TextTrackRepo.FindReady(cmdContext(), id, *sourceLang, asset.TextTrackTranscript)
		if err != nil {
			return fmt.Errorf("%s: read source cues: %w", id, err)
		}
		if len(srcCues) == 0 {
			return fmt.Errorf("%s: no source cues for language %q", id, *sourceLang)
		}

		tracks, err := root.Repos.TextTrackRepo.ListByAsset(cmdContext(), id)
		if err != nil {
			return fmt.Errorf("%s: list tracks: %w", id, err)
		}

		byLang := map[string][]asset.TimedCue{*sourceLang: srcCues}
		for _, track := range tracks {
			if track.TextKind != asset.TextTrackTranscript || track.Status != asset.TextTrackReady || !track.IsCurrent {
				continue
			}
			if track.LanguageCode == *sourceLang {
				continue
			}
			byLang[track.LanguageCode] = texttracks.CuesWithText(srcCues, track.TextContent)
		}

		if err := svc.Repair(cmdContext(), id, byLang); err != nil {
			return fmt.Errorf("%s: repair cues: %w", id, err)
		}
		fmt.Printf("%s: aligned %d languages onto %d source cues\n", id, len(byLang), len(srcCues))
	}
	return nil
}
