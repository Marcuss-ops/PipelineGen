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

// finalJobStockScanWindow bounds how many folder entries one selection step
// inspects. The prefetch lane warms the same head of the folder, so this is
// also the window in which the next source is already cached. A folder that
// cannot fill a scene inside this window fails closed instead of sending the
// Master a stock window it cannot packet-copy.
const finalJobStockScanWindow = 64

// sceneClips consumes one scene's canonical duration from its stock folder
// using WHOLE files only. The remote Master's stock lane is packet-copy: it
// accepts source_in_us == 0 and source_duration_us == file duration, and
// rejects a sub-window (r56: stock_window_not_packet_copyable). The Producer
// therefore never trims a stock file. It prefers a file whose duration exactly
// matches the remaining window; when none exists it takes the first whole file
// that fits, so the scene is composed of whole files summing exactly to the
// canonical duration. If no such composition exists, the handoff fails here
// instead of sending a doomed plan.
//
// Selection is deterministic: the cursor advances past the claimed file and the
// next scene continues through the folder. This replaces the former walk that
// consumed a partial tail from the last file, which is exactly what the Master
// refused in r53-r58.
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
		limit := len(files)
		if limit > finalJobStockScanWindow {
			limit = finalJobStockScanWindow
		}
		chosen := -1
		chosenDurationMS := int64(0)
		var chosenAsset map[string]any
		var firstResolveErr error
		for offset := 0; offset < limit; offset++ {
			index := (folderIndex + offset) % len(files)
			file := files[index]
			asset, ok := p.assets[file.ID]
			if !ok {
				asset, err = resolveFinalJobStockAsset(ctx, resolver, file.ID)
				if err != nil {
					if firstResolveErr == nil {
						firstResolveErr = fmt.Errorf("resolve stock file %s (%s): %w", file.Name, file.ID, err)
					}
					continue
				}
				p.assets[file.ID] = asset
			}
			durationMS, _ := asset["duration_ms"].(int64)
			if durationMS <= 0 {
				if firstResolveErr == nil {
					firstResolveErr = fmt.Errorf("stock file %s has no canonical duration", file.Name)
				}
				continue
			}
			// Exact match wins immediately. Otherwise retain the first whole
			// file that fits; do not pick a larger file and leave a tail the
			// next step cannot fill.
			if durationMS == remainingMS {
				chosen, chosenDurationMS, chosenAsset = index, durationMS, asset
				break
			}
			if durationMS < remainingMS && chosen < 0 {
				chosen, chosenDurationMS, chosenAsset = index, durationMS, asset
			}
		}
		if chosen < 0 {
			if firstResolveErr != nil {
				return nil, fmt.Errorf("stock folder %s cannot fill the remaining %dms with whole files (no partial stock windows allowed): %w", folderID, remainingMS, firstResolveErr)
			}
			return nil, fmt.Errorf("stock folder %s cannot fill the remaining %dms with whole files; no partial stock windows are allowed", folderID, remainingMS)
		}
		clips = append(clips, finalJobStockClip{Asset: chosenAsset, ChunkMS: chosenDurationMS})
		remainingMS -= chosenDurationMS
		folderIndex = (chosen + 1) % len(files)
		p.cursors[folderID] = folderIndex
	}
	return clips, nil
}
