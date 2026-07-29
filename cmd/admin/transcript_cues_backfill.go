package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
)

var clipIDPattern = regexp.MustCompile(`^yt_(.+)_([0-9]+)_([0-9]+)_v[0-9]+$`)

func runTranscriptCuesBackfill(args []string) error {
	fs := flag.NewFlagSet("transcript-cues-backfill", flag.ContinueOnError)
	ids := fs.String("asset-ids", "", "comma-separated clip asset IDs")
	dir := fs.String("subtitle-dir", "data/media/subtitles", "subtitle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	assetIDs := splitCSV(*ids)
	if len(assetIDs) == 0 {
		return fmt.Errorf("transcript-cues-backfill: --asset-ids is required")
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
		m := clipIDPattern.FindStringSubmatch(id)
		if len(m) != 4 {
			return fmt.Errorf("invalid asset id %q", id)
		}
		start, _ := strconv.Atoi(m[2])
		end, _ := strconv.Atoi(m[3])
		entries, err := ytinfra.ParseVTTEntries(filepath.Join(*dir, m[1]+".en.vtt"), float64(start), float64(end))
		if err != nil {
			return fmt.Errorf("%s: parse VTT: %w", id, err)
		}
		cues := make([]asset.TimedCue, 0, len(entries))
		maxMs := int64(end-start) * 1000
		for _, e := range entries {
			s := int64((e.Start - float64(start)) * 1000)
			en := int64((e.End - float64(start)) * 1000)
			if s < 0 {
				s = 0
			}
			if en > maxMs {
				en = maxMs
			}
			if en > s {
				cues = append(cues, asset.TimedCue{StartMs: s, EndMs: en, Text: e.Text})
			}
		}
		tracks, err := root.Repos.TextTrackRepo.ListByAsset(cmdContext(), id)
		if err != nil {
			return err
		}
		byLang := make(map[string][]asset.TimedCue, len(tracks))
		for _, track := range tracks {
			if track.TextKind != asset.TextTrackTranscript || track.Status != asset.TextTrackReady || !track.IsCurrent {
				continue
			}
			byLang[track.LanguageCode] = cuesWithText(cues, track.TextContent)
		}
		if err := svc.Repair(cmdContext(), id, byLang); err != nil {
			return err
		}
		fmt.Printf("%s: repaired %d languages, %d cues\n", id, len(byLang), len(cues))
	}
	return nil
}

// cuesWithText keeps canonical VTT timing while projecting each already
// translated full transcript onto the same cue count. The source translation
// remains authoritative in asset_text_tracks; this avoids storing English text
// in translated segment rows when no per-language VTT exists.
func cuesWithText(timing []asset.TimedCue, text string) []asset.TimedCue {
	words := strings.Fields(text)
	out := make([]asset.TimedCue, len(timing))
	for i, cue := range timing {
		start := i * len(words) / len(timing)
		end := (i + 1) * len(words) / len(timing)
		if end <= start && start < len(words) {
			end = start + 1
		}
		if start > len(words) {
			start = len(words)
		}
		if end > len(words) {
			end = len(words)
		}
		out[i] = cue
		out[i].Text = strings.Join(words[start:end], " ")
	}
	return out
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
