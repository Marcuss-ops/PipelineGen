package backfill

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"
)

var clipIDPattern = regexp.MustCompile(`^yt_(.+)_([0-9]+)_([0-9]+)_v[0-9]+$`)

func RunTranscriptCuesBackfill(args []string) error {
	fs := flag.NewFlagSet("transcript-cues-backfill", flag.ContinueOnError)
	ids := fs.String("asset-ids", "", "comma-separated clip asset IDs")
	dir := fs.String("subtitle-dir", "data/media/subtitles", "subtitle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	assetIDs := cli.SplitCSV(*ids)
	if len(assetIDs) == 0 {
		return fmt.Errorf("transcript-cues-backfill: --asset-ids is required")
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
		cues := make([]detail.TimedCue, 0, len(entries))
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
				cues = append(cues, detail.TimedCue{StartMs: s, EndMs: en, Text: e.Text})
			}
		}
		tracks, err := root.Repos.TextTrackRepo.ListByAsset(cli.CmdContext(), id)
		if err != nil {
			return err
		}
		byLang := make(map[string][]detail.TimedCue, len(tracks))
		for _, track := range tracks {
			if track.TextKind != detail.TextTrackTranscript || track.Status != detail.TextTrackReady || !track.IsCurrent {
				continue
			}
			byLang[track.LanguageCode] = texttracks.CuesWithText(cues, track.TextContent)
		}
		if err := svc.Repair(cli.CmdContext(), id, byLang); err != nil {
			return err
		}
		fmt.Printf("%s: repaired %d languages, %d cues\n", id, len(byLang), len(cues))
	}
	return nil
}
