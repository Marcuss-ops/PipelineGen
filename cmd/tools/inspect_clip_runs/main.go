package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"velox/go-master/internal/service/channelmonitor"
)

func main() {
	dbPath := flag.String("db", "data/channel_monitor_clip_runs.sqlite", "path to the clip run sqlite database")
	videoID := flag.String("video", "", "optional video id filter")
	all := flag.Bool("all", false, "show all runs instead of only failed/needs-review ones")
	flag.Parse()

	store, err := channelmonitor.OpenClipRunStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open clip run store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	var records []channelmonitor.ClipRunRecord
	if *all {
		if strings.TrimSpace(*videoID) != "" {
			records = store.ListByVideo(*videoID)
		} else {
			records = store.ListAll()
		}
	} else {
		records = store.ListAttentionNeeded()
		if strings.TrimSpace(*videoID) != "" {
			filtered := make([]channelmonitor.ClipRunRecord, 0, len(records))
			for _, rec := range records {
				if rec.VideoID == *videoID {
					filtered = append(filtered, rec)
				}
			}
			records = filtered
		}
	}

	if len(records) == 0 {
		if *all {
			fmt.Println("no clip runs found")
		} else {
			fmt.Println("no clip runs matched the selected filters")
		}
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RUN_KEY\tVIDEO_ID\tSTATUS\tSTART\tEND\tDUR\tCONF\tREVIEW\tTXT_FILE_ID\tDRIVE_FILE_ID\tERROR")
	for _, rec := range records {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%.2f\t%t\t%s\t%s\t%s\n",
			shortRunKey(rec.RunKey),
			rec.VideoID,
			rec.Status,
			rec.StartSec,
			rec.EndSec,
			rec.Duration,
			rec.Confidence,
			rec.NeedsReview,
			trimID(rec.TxtFileID),
			trimID(rec.DriveFileID),
			squashSpaces(rec.Error),
		)
	}
	_ = w.Flush()
}

func shortRunKey(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func trimID(value string) string {
	if value == "" {
		return "-"
	}
	if len(value) <= 16 {
		return value
	}
	return value[:16] + "..."
}

func squashSpaces(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
