package scriptgeneration

import (
	"context"
	"fmt"
)

// final_job_stock_planner.go owns WHICH stock source a final job consumes and
// which Drive reference/window goes into the compiled plan. The planner reads
// saved metadata only; the Master downloads and processes the selected stock
// window outside the Producer.
// finalJobStockClip is one stock window the remote Master will place: the
// resolved Drive reference and the exact duration the scene consumes from it.
type finalJobStockClip struct {
	Asset   map[string]any
	ChunkMS int64
}

// finalJobStockPlanner carries the per-handoff stock bookkeeping: the eligible
// file list per folder, the resolved asset cache, and the rotation cursor. The
// cursor is per FOLDER and advances across scenes, so one scene's consumption
// decides what the next scene in the same folder gets — the planner must be
// shared by every scene of a handoff, never recreated per scene.
type finalJobStockPlanner struct {
	files   map[string][]FinalJobStockFile
	assets  map[string]map[string]any
	cursors map[string]int
}

func newFinalJobStockPlanner() *finalJobStockPlanner {
	return &finalJobStockPlanner{
		files:   make(map[string][]FinalJobStockFile),
		assets:  make(map[string]map[string]any),
		cursors: make(map[string]int),
	}
}

// folder returns the video files of one stock folder, listing it once per
// handoff and warming the metadata of the sources it will offer. The Producer
// never normalizes or uploads stock: the compiled plan carries the Drive
// reference and the remote Master processes the selected window.
func (p *finalJobStockPlanner) folder(ctx context.Context, resolver FinalJobAssetResolver, folderID string) ([]FinalJobStockFile, error) {
	if files, ok := p.files[folderID]; ok {
		return files, nil
	}
	listed, err := resolver.ListFinalJobStockFolder(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("list selected stock folder %s: %w", folderID, err)
	}
	if len(listed) == 0 {
		return nil, fmt.Errorf("selected stock folder %s contains no video files", folderID)
	}
	p.files[folderID] = listed
	return listed, nil
}

// sceneClips consumes one scene's canonical duration from its stock folder and
// returns the exact chunks the assembler will place.
//
// It reproduces the stock walk of the final-job payload byte for byte: rotate
// through the folder, consume one chunk per file, and take the scene's
// remaining duration from the last one. The cursor is stored after the file has
// been claimed, so a failure half way through a scene leaves the folder
// advanced exactly as the successful walk would have.
func (p *finalJobStockPlanner) sceneClips(ctx context.Context, resolver FinalJobAssetResolver, folderID string, durationUS int64) ([]finalJobStockClip, error) {
	files, err := p.folder(ctx, resolver, folderID)
	if err != nil {
		return nil, err
	}
	remainingMS := (durationUS + 999) / 1000
	if remainingMS <= 0 {
		return nil, nil
	}
	clips := make([]finalJobStockClip, 0, 1)
	folderIndex := p.cursors[folderID]
	for remainingMS > 0 {
		file := files[folderIndex%len(files)]
		folderIndex++
		p.cursors[folderID] = folderIndex
		asset, ok := p.assets[file.ID]
		if !ok {
			asset, err = resolveFinalJobStockAsset(ctx, resolver, file.ID)
			if err != nil {
				return nil, fmt.Errorf("resolve stock file %s (%s): %w", file.Name, file.ID, err)
			}
			p.assets[file.ID] = asset
		}
		durationMS, _ := asset["duration_ms"].(int64)
		if durationMS <= 0 {
			return nil, fmt.Errorf("stock file %s has no canonical duration", file.Name)
		}
		chunkMS := durationMS
		if chunkMS > remainingMS {
			chunkMS = remainingMS
		}
		clips = append(clips, finalJobStockClip{Asset: asset, ChunkMS: chunkMS})
		remainingMS -= chunkMS
		folderIndex++
	}
	return clips, nil
}
