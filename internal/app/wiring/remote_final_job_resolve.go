package wiring

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"

	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Runtime-asset resolution for the remote final job: the identity cache and
// the prefetch lanes that keep handoff from re-resolving one source per
// fragment. Stock resolution is metadata-only; the Master owns the bytes.

// resolverClock accumulates the runtime-asset resolution time of one handoff.
// It answers the only question the receipt could not: how much of handoff_ms is
// spent finding and publishing sources rather than encoding fragments.
func (a *remoteFinalJobAdapter) resolverClock() func() {
	started := time.Now()
	return func() {
		if a != nil {
			a.resolverNS.Add(time.Since(started).Nanoseconds())
		}
	}
}

func (a *remoteFinalJobAdapter) ResolveFinalJobAsset(ctx context.Context, id string) (map[string]any, error) {
	return a.resolveOnce("objectstore:"+strings.TrimSpace(id), func() (map[string]any, error) {
		defer a.resolverClock()()
		started := time.Now()
		ref, err := a.ResolveFinalJobObjectStoreAsset(ctx, id)
		log.Printf("final_job resolve kind=objectstore id=%s bytes=%v ms=%d err=%v",
			id, ref["size_bytes"], time.Since(started).Milliseconds(), err != nil)
		return ref, err
	})
}

// resolveOnce answers a repeated resolution from the handoff's own cache. The
// cached reference is copied on the way out so a caller cannot mutate the entry
// (the payload builder stamps roles and durations onto the map it receives).
//
// Concurrent callers of the SAME identity share one resolution (single flight):
// the prefetch warms a folder while the walk asks for the file it needs, and
// without this the prefetch would be duplicated work instead of saved work.
// An error is never cached — a failed warm-up must not poison the walk.
func (a *remoteFinalJobAdapter) resolveOnce(key string, build func() (map[string]any, error)) (map[string]any, error) {
	if a == nil {
		return nil, fmt.Errorf("final-job adapter is not configured")
	}
	a.resolvedMu.Lock()
	if cached, ok := a.resolved[key]; ok {
		a.hits++
		a.resolvedMu.Unlock()
		return copyFinalJobRef(cached), nil
	}
	a.resolvedMu.Unlock()

	a.inflightMu.Lock()
	if call, ok := a.inflight[key]; ok {
		a.inflightMu.Unlock()
		<-call.done
		if call.err != nil {
			return nil, call.err
		}
		a.resolvedMu.Lock()
		a.hits++
		a.resolvedMu.Unlock()
		return copyFinalJobRef(call.ref), nil
	}
	if a.inflight == nil {
		a.inflight = make(map[string]*finalJobResolveCall, 16)
	}
	call := &finalJobResolveCall{done: make(chan struct{})}
	a.inflight[key] = call
	a.inflightMu.Unlock()

	func() {
		defer close(call.done)
		defer func() {
			a.inflightMu.Lock()
			delete(a.inflight, key)
			a.inflightMu.Unlock()
		}()
		call.ref, call.err = build()
		if call.err != nil {
			return
		}
		a.resolvedMu.Lock()
		if a.resolved == nil {
			a.resolved = make(map[string]map[string]any, 16)
		}
		a.resolved[key] = copyFinalJobRef(call.ref)
		a.resolvedMu.Unlock()
	}()
	if call.err != nil {
		return nil, call.err
	}
	return copyFinalJobRef(call.ref), nil
}

const (
	// defaultFinalJobResolveConcurrency is how many Drive sources one handoff
	// resolves at a time. It is 1 because concurrency was MEASURED and it loses
	// on this stack: serial r69/r73 cost ~190 ms per source (61 sources = 10,9 s
	// of the handoff), while a 64-file warm-up at 6 workers paid 576 ms per
	// source on average (max 2.068 ms) and turned the same handoff into 49,5 s.
	// The per-call cost triples under load — the path is throughput bound, not
	// latency bound — so parallelising it multiplies work instead of hiding it.
	// The knob stays so a future stack fix can be measured against this table.
	defaultFinalJobResolveConcurrency = 1
	// maxFinalJobResolveConcurrency bounds the operator knob: beyond this the
	// handoff is no longer latency bound and a folder listing becomes a spike.
	maxFinalJobResolveConcurrency = 16
	// finalJobPrefetchPerFolder caps how many of one folder's files a prefetch
	// warms. It is a WINDOW, not a sample: the folder cursor consumes the list
	// in order, so warming the head of the list is exactly what the walk asks
	// for next (r73: a 240-file folder, 61 of them consumed, 12 warmed → 10,9 s
	// still spent on the critical path). 64 covers a long timeline at ~5 s per
	// source while costing ceil(64/6)·190 ms ≈ 2 s of wall instead of 64·190 ms.
	// A folder of hundreds is never resolved wholesale.
	finalJobPrefetchPerFolder = 64
)

// finalJobResolveConcurrency reads VELOX_FINAL_JOB_RESOLVE_CONCURRENCY. Absent
// means the measured default, 1 means the pre-2026-11-01 serial handoff, and a
// malformed value degrades to serial rather than inventing parallelism.
func finalJobResolveConcurrency() int {
	raw := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_RESOLVE_CONCURRENCY"))
	if raw == "" {
		return defaultFinalJobResolveConcurrency
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 1
	}
	if value > maxFinalJobResolveConcurrency {
		return maxFinalJobResolveConcurrency
	}
	return value
}

// beginHandoffCache scopes the per-handoff resolution cache to ONE run without
// throwing away what a prefetch of the SAME run already resolved.
func (a *remoteFinalJobAdapter) beginHandoffCache(runID string) {
	if a == nil {
		return
	}
	runID = strings.TrimSpace(runID)
	a.resolvedMu.Lock()
	defer a.resolvedMu.Unlock()
	if a.resolvedRunID == runID && a.resolved != nil {
		return
	}
	a.resolvedRunID = runID
	a.resolved = nil
	a.hits = 0
}

// PrefetchFinalJobStockAssets warms the handoff cache for the Drive sources one
// stock folder is about to offer. It returns when every requested identity has
// been attempted (or the context ended), and it never reports an error: a
// source that cannot be warmed is resolved — and reported — by the walk.
func (a *remoteFinalJobAdapter) PrefetchFinalJobStockAssets(ctx context.Context, ids []string) {
	a.prefetchFinalJobSources(ctx, ids, a.ResolveFinalJobStockAsset)
}

// prefetchFinalJobSources is the scheduling half of the prefetch: dedupe, skip
// what the cache already holds, then resolve with a bounded pool. The resolver
// is a parameter so the scheduling is testable without a Drive or an object
// store.
func (a *remoteFinalJobAdapter) prefetchFinalJobSources(ctx context.Context, ids []string, resolve func(context.Context, string) (map[string]any, error)) {
	if a == nil || resolve == nil {
		return
	}
	workers := finalJobResolveConcurrency()
	if workers <= 1 {
		return
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		a.resolvedMu.Lock()
		_, cached := a.resolved["drive:"+id]
		a.resolvedMu.Unlock()
		if cached {
			continue
		}
		unique = append(unique, id)
		if len(unique) == finalJobPrefetchPerFolder {
			break
		}
	}
	if len(unique) == 0 {
		return
	}
	if len(unique) < workers {
		workers = len(unique)
	}
	started := time.Now()
	log.Printf("final_job prefetch kind=drive requested=%d warm=%d workers=%d", len(ids), len(unique), workers)
	queue := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range queue {
				if ctx.Err() != nil {
					return
				}
				if _, err := resolve(ctx, id); err != nil {
					log.Printf("final_job prefetch kind=drive id=%s err=%v", id, err)
				}
			}
		}()
	}
	for _, id := range unique {
		select {
		case queue <- id:
		case <-ctx.Done():
			close(queue)
			wg.Wait()
			return
		}
	}
	close(queue)
	wg.Wait()
	log.Printf("final_job prefetch kind=drive warm=%d wall_ms=%d", len(unique), time.Since(started).Milliseconds())
}

func copyFinalJobRef(ref map[string]any) map[string]any {
	if ref == nil {
		return nil
	}
	out := make(map[string]any, len(ref))
	for key, value := range ref {
		out[key] = value
	}
	return out
}

// resolveHits reports the resolutions answered from the handoff cache, so a
// receipt can tell deduplicated work from work that never happened.
func (a *remoteFinalJobAdapter) resolveHits() int {
	if a == nil {
		return 0
	}
	a.resolvedMu.Lock()
	defer a.resolvedMu.Unlock()
	return a.hits
}

// ResolveFinalJobStockAsset returns a metadata-only Drive reference. That exact
// reference becomes the FINALIZE stock entry, and the remote Master downloads
// and processes it. The Producer never fetches the stock bytes or turns them
// into a local fragment.
func (a *remoteFinalJobAdapter) ResolveFinalJobStockAsset(ctx context.Context, id string) (map[string]any, error) {
	return a.resolveOnce("drive:"+strings.TrimSpace(id), func() (map[string]any, error) {
		return a.stockDriveReference(ctx, id)
	})
}

func (a *remoteFinalJobAdapter) stockDriveReference(ctx context.Context, id string) (map[string]any, error) {
	defer a.resolverClock()()
	if a == nil || a.stock == nil || a.media == nil {
		return nil, fmt.Errorf("final-job stock metadata readers are not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\\x00\r\n") {
		return nil, fmt.Errorf("final-job stock Drive id is invalid")
	}
	assets, err := a.media.ResolveByDriveFileID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read saved stock metadata for Drive file %q: %w", id, err)
	}
	var saved *asset.Asset
	for _, candidate := range assets {
		if candidate != nil && candidate.DriveFileID() == id {
			saved = candidate
			break
		}
	}
	if saved == nil {
		return nil, fmt.Errorf("stock Drive file %q has no saved media registry entry; run the stockpipeline first", id)
	}
	if saved.Duration <= 0 || len(saved.ContentHash()) != 64 {
		return nil, fmt.Errorf("stock Drive file %q lacks saved duration or SHA-256 metadata", id)
	}
	// The saved registry row already carries the certificate the handoff
	// needs (content SHA-256 and duration). Its size is a verified ingest
	// fact too, so querying Drive again was a serial metadata round-trip per
	// source for information this process already had. Only a row that never
	// recorded a size falls back to the provider.
	size := saved.SizeBytes()
	if size <= 0 {
		// The asset row does not project asset_locations, where ingest
		// records the uploaded artifact's verified size. Read that local
		// fact before spending a serial Drive metadata round-trip; a
		// failed read keeps the provider fallback rather than inventing a
		// size.
		if locationSize, sizeErr := a.media.DriveLocationSize(ctx, id); sizeErr == nil {
			size = locationSize
		}
	}
	if size <= 0 {
		meta, err := a.stock.GetFileMeta(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read stock Drive metadata %q: %w", id, err)
		}
		if meta == nil || meta.Size <= 0 {
			return nil, fmt.Errorf("stock Drive file %q has no positive byte size", id)
		}
		size = meta.Size
	}
	return finalJobStockDriveReference(id, saved, size), nil
}

// finalJobStockDriveReference is the metadata-only contract for a Drive stock
// source: the Master can fetch source_uri directly, while asset_id is the bare
// content SHA so the source identity is stable. This exact reference is sent in
// the FINALIZE asset list; no Producer-owned stock fragment exists.
func finalJobStockDriveReference(id string, saved *asset.Asset, size int64) map[string]any {
	sha := strings.ToLower(saved.ContentHash())
	return map[string]any{
		"asset_id": sha, "drive_file_id": id,
		"url":        "velox-drive://" + id,
		"source_uri": driveStockDownloadURL(id),
		"sha256":     sha, "kind": "video", "mime": "video/mp4",
		"size_bytes": size, "duration_ms": saved.Duration.Milliseconds(),
	}
}

// driveStockDownloadURL is the public HTTPS fetch of the exact Drive bytes.
// The remote Master uses it to download and process the stock window itself.
func driveStockDownloadURL(id string) string {
	return "https://drive.usercontent.google.com/download?id=" + id + "&export=download&confirm=t"
}

// FinalJobPublishedFileSize is retained for the capability port's legacy
// shape, but direct HTTPS delivery never resolves sizes through Drive.
func (a *remoteFinalJobAdapter) FinalJobPublishedFileSize(context.Context, string) (int64, error) {
	return 0, fmt.Errorf("Drive file metadata is disabled for final-job delivery; use Objectstore HTTPS references")
}

func (a *remoteFinalJobAdapter) ListFinalJobStockFolder(ctx context.Context, folderID string) ([]scriptgen.FinalJobStockFile, error) {
	defer a.resolverClock()()
	if a == nil || a.stock == nil {
		return nil, fmt.Errorf("remote final-job stock Drive reader is not configured")
	}
	listed, err := a.stock.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	files := make([]scriptgen.FinalJobStockFile, 0, len(listed))
	for _, f := range listed {
		if strings.HasPrefix(strings.ToLower(f.MimeType), "video/") || strings.HasSuffix(strings.ToLower(f.Name), ".mp4") {
			files = append(files, scriptgen.FinalJobStockFile{ID: f.ID, Name: f.Name})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

var (
	_ scriptgen.FinalJobSubmitter = (*remoteFinalJobAdapter)(nil)
	_ scriptgen.FinalJobAttacher  = (*remoteFinalJobAdapter)(nil)
)
