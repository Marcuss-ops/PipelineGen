package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
)

func main() {
	ctx := context.Background()

	artlistPath := getenv("ARTLIST_DB_PATH", config.ResolveDataPath("artlist_local.db.json"))
	stockPath := getenv("STOCK_DB_PATH", config.ResolveDataPath("stock.db.json"))
	clipPath := getenv("CLIP_DB_PATH", config.ResolveDataPath("clip_index.json"))

	alDB, err := artlistdb.Open(artlistPath)
	if err != nil {
		fmt.Printf("open artlist db error: %v\n", err)
		os.Exit(1)
	}
	stDB, err := stockdb.Open(stockPath)
	if err != nil {
		fmt.Printf("open stock db error: %v\n", err)
		os.Exit(1)
	}
	clDB, err := clipdb.Open(clipPath)
	if err != nil {
		fmt.Printf("open clip db error: %v\n", err)
		os.Exit(1)
	}
	drv, err := drive.NewClient(ctx, drive.DefaultConfig())
	if err != nil {
		fmt.Printf("init drive client error: %v\n", err)
		os.Exit(1)
	}

	stats, err := alDB.DeduplicateDownloadedByVisualHash()
	if err != nil {
		fmt.Printf("dedup db error: %v\n", err)
		os.Exit(1)
	}
	stockDBDupIDs, err := stDB.DeduplicateByFolderAndFilename()
	if err != nil {
		fmt.Printf("warn: stockdb dedup failed: %v\n", err)
	}
	clipDBDupIDs, err := clDB.DeduplicateByFolderAndFilename()
	if err != nil {
		fmt.Printf("warn: clipdb dedup failed: %v\n", err)
	}

	// Second pass: dedupe exact same filename within the same Drive folder.
	folderIDs := collectFolderIDs(alDB, stDB, clDB)
	filenameDupIDs, err := findDuplicateDriveFilesByFolderAndName(ctx, drv, folderIDs)
	if err != nil {
		fmt.Printf("warn: filename dedup scan failed: %v\n", err)
	}
	allDeleteSet := make(map[string]bool)
	for _, id := range stats.DriveIDsToDelete {
		allDeleteSet[id] = true
	}
	for _, id := range stockDBDupIDs {
		allDeleteSet[id] = true
	}
	for _, id := range clipDBDupIDs {
		allDeleteSet[id] = true
	}
	for _, id := range filenameDupIDs {
		allDeleteSet[id] = true
	}
	allDeleteIDs := make([]string, 0, len(allDeleteSet))
	for id := range allDeleteSet {
		allDeleteIDs = append(allDeleteIDs, id)
	}
	sort.Strings(allDeleteIDs)

	deletedDrive := 0
	deletedStock := 0
	deletedClip := 0
	for _, id := range allDeleteIDs {
		if err := drv.DeleteFile(ctx, id); err == nil {
			deletedDrive++
		} else {
			fmt.Printf("warn: delete drive file %s failed: %v\n", id, err)
		}
		if err := stDB.DeleteClipByID(id); err == nil {
			deletedStock++
		} else {
			fmt.Printf("warn: delete stock clip %s failed: %v\n", id, err)
		}
		if err := clDB.DeleteClipByID(id); err == nil {
			deletedClip++
		} else {
			fmt.Printf("warn: delete clipdb clip %s failed: %v\n", id, err)
		}
	}
	clearedArtlist, err := alDB.ClearDeletedDriveFiles(allDeleteIDs)
	if err != nil {
		fmt.Printf("warn: clear deleted drive ids in artlistdb failed: %v\n", err)
	}

	fmt.Printf("dedup complete: canonical=%d duplicates_marked=%d hash_targets=%d filename_targets=%d total_targets=%d drive_deleted=%d stock_deleted=%d clipdb_deleted=%d artlist_cleared=%d\n",
		stats.CanonicalKept, stats.DuplicateMarked, len(stats.DriveIDsToDelete)+len(stockDBDupIDs)+len(clipDBDupIDs), len(filenameDupIDs), len(allDeleteIDs), deletedDrive, deletedStock, deletedClip, clearedArtlist)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func collectFolderIDs(alDB *artlistdb.ArtlistDB, stDB *stockdb.StockDB, clDB *clipdb.ClipDB) []string {
	folderSet := make(map[string]bool)

	terms := alDB.GetAllTerms()
	for _, term := range terms {
		clips, ok := alDB.GetDownloadedClipsForTerm(term)
		if !ok {
			continue
		}
		for _, c := range clips {
			if id := strings.TrimSpace(c.FolderID); id != "" {
				folderSet[id] = true
			}
		}
	}
	if folders, err := stDB.GetAllFolders(); err == nil {
		for _, f := range folders {
			if id := strings.TrimSpace(f.DriveID); id != "" {
				folderSet[id] = true
			}
		}
	}
	if clips, err := stDB.GetAllClips(); err == nil {
		for _, c := range clips {
			if id := strings.TrimSpace(c.FolderID); id != "" {
				folderSet[id] = true
			}
		}
	}
	for _, c := range clDB.GetAllClips() {
		if id := strings.TrimSpace(c.FolderID); id != "" {
			folderSet[id] = true
		}
	}

	out := make([]string, 0, len(folderSet))
	for id := range folderSet {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func findDuplicateDriveFilesByFolderAndName(ctx context.Context, drv *drive.Client, folderIDs []string) ([]string, error) {
	var deleteIDs []string
	for _, folderID := range folderIDs {
		content, err := drv.GetFolderContent(ctx, folderID)
		if err != nil || content == nil {
			continue
		}
		type fileRef struct {
			id   string
			name string
			ts   int64
		}
		byName := make(map[string][]fileRef)
		for _, f := range content.Files {
			n := strings.TrimSpace(strings.ToLower(f.Name))
			if n == "" {
				continue
			}
			byName[n] = append(byName[n], fileRef{id: f.ID, name: n, ts: f.CreatedTime.Unix()})
		}
		for _, list := range byName {
			if len(list) <= 1 {
				continue
			}
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].ts < list[j].ts
			})
			// Keep oldest, remove the rest.
			for i := 1; i < len(list); i++ {
				deleteIDs = append(deleteIDs, list[i].id)
			}
		}
	}
	sort.Strings(deleteIDs)
	return deleteIDs, nil
}
