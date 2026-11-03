package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
)

// final_job_stock_planner.go owns WHICH stock source a final job consumes.
//
// The selection used to exist only inside BuildFinalJobPayloads, intertwined
// with building scenes, runtime assets and script text. That made the selection
// unavailable to anything that wanted to resolve the same sources EARLIER than
// the handoff: r75 measured 9,8 s of serial Drive resolution inside `handoff`
// (576 ms per source, concurrency does not help because each source publishes
// its own bytes) while the overlay render was occupying 13,4 s of the critical
// path with no work queued behind it.
//
// A prewarm that re-derived the selection would be a second implementation of
// the same rule, and two implementations of "which source does scene N get"
// drift: the prewarm would warm the wrong file and the walk would still pay the
// full price, silently. So the rule lives here, once, and both the payload
// build and the prewarm walk through it.
//
// The walk is DEPENDENT ON RESOLVED DURATIONS: which file a scene consumes next
// depends on how long the previous chunk filled the scene, and durations come
// from the resolver. A prewarm therefore cannot know the set without resolving
// it — but resolving is exactly the expensive work, and the resolver's cache
// (plus its single-flight index) makes the later walk answer from what the
// prewarm already paid for. That is why sharing the walk is sufficient and a
// separate "predict the set" heuristic is not needed.

// finalJobStockClip is one stock window the remote assembler will place: the
// resolved asset (Drive identity in, immutable HTTPS reference out) and the
// exact duration the scene consumes from it.
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
// handoff and warming the sources it will offer. The final-job preparer
// normalizes sources that do not qualify for packet copy before publishing
// them to the remote assembler.
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
	// Warm the sources this folder can offer BEFORE the walk asks for them. A
	// scene at 10-14 s spends 195 ms per uncached Drive source (metadata,
	// staged bytes, publish, signature) and r69's handoff paid 56 of those
	// serially: 10,9 s of its 14,4 s. The set is known here and the identities
	// are independent, so the only reason to stay serial was that nobody had
	// asked for them concurrently.
	prefetchFinalJobStockAssets(ctx, resolver, listed)
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

// PrewarmFinalJobStockSources resolves every stock source the final job WILL
// consume, during a window the run is already spending elsewhere, and reports
// how many windows it planned.
//
// It is deliberately the same walk the handoff uses (see finalJobStockPlanner):
// a source is only warmable if the walk would ask for it, and the walk only
// asks for it once it has resolved the previous chunk's duration. Running the
// walk early therefore resolves the exact identities the handoff would have
// resolved serially, and the handoff's own cache answers them.
//
// Failure is not fatal and not silent: the error is returned to the caller (the
// runner logs it) while the handoff re-walks and reports its own error, so a
// prewarm miss can only ever cost the time it tried to save.
func PrewarmFinalJobStockSources(ctx context.Context, result *GenerateResult, resolver FinalJobAssetResolver) (int, error) {
	if result == nil || result.CanonicalTimeline == nil || resolver == nil {
		return 0, nil
	}
	scenesByID := make(map[string]Scene, len(result.Scenes))
	for _, scene := range result.Scenes {
		scenesByID[scene.ID] = scene
	}
	planner := newFinalJobStockPlanner()
	planned := 0
	for _, segment := range result.CanonicalTimeline.Segments {
		localScene, ok := scenesByID[segment.ID]
		if !ok {
			continue
		}
		// Fixed-media scenes place protected intro clips and clip-only scenes
		// place the pipeline's own certified render: neither touches a stock
		// folder, so neither advances the cursor this walk reproduces.
		if segment.FixedMedia || localScene.ExecutionMode.IsFixedMedia() {
			continue
		}
		if localScene.Stock == nil {
			continue
		}
		folderID := strings.TrimSpace(localScene.Stock.FolderID)
		if folderID == "" {
			continue
		}
		clips, err := planner.sceneClips(ctx, resolver, folderID, segment.DurationUS)
		if err != nil {
			return planned, err
		}
		planned += len(clips)
	}
	return planned, nil
}
