package wiring

import (
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"

	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Runtime-asset resolution for the remote final job: the identity cache, the
// stock staging and its receipts, and the prefetch lanes that keep handoff
// from re-resolving one source per fragment.

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
	raw := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_RESOLVE_CONCURRENCY"))
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

// PrewarmFinalJobStockSources is the capability the scripts runner calls while
// the overlay render is still running (FinalJobSourcePrewarmer). It walks the
// FINAL JOB's own stock selection through the same planner the payload build
// uses, so the identities it resolves are exactly the ones the handoff would
// have resolved serially, and the handoff then answers them from this cache.
//
// It never fails the run: the error is returned for the log, and the handoff
// re-walks the same selection and reports its own error.
func (a *remoteFinalJobAdapter) PrewarmFinalJobStockSources(ctx context.Context, runID string, req scriptgen.GenerateRequest, result *scriptgen.GenerateResult) error {
	if a == nil || result == nil {
		return nil
	}
	// The prewarm owns the cache of THIS run; StartFinalJob keeps it because it
	// sees the same run id.
	a.beginHandoffCache(runID)
	started := time.Now()
	windows, err := scriptgen.PrewarmFinalJobStockSources(ctx, result, a)
	a.resolvedMu.Lock()
	unique := len(a.resolved)
	a.resolvedMu.Unlock()
	log.Printf("final_job prewarm kind=stock windows=%d unique_resolved=%d wall_ms=%d err=%v",
		windows, unique, time.Since(started).Milliseconds(), err)
	return err
}

// beginHandoffCache scopes the per-handoff resolution cache to ONE run without
// throwing away what a prewarm of the SAME run already resolved.
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

// finalJobStockCacheDir is where a Drive stock file is kept between runs. The
// handoff publishes every source it sends, and publishing needs the bytes; the
// same file id with the same byte size is the same bytes, so the next run must
// not pay the network again. VELOX_FINAL_JOB_STOCK_CACHE_DIR relocates it.
func finalJobStockCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_STOCK_CACHE_DIR")); dir != "" {
		return dir
	}
	return filepath.Join("data", "cache", "final_job_stock")
}

// stagedStockPath returns a local copy of one Drive stock file, downloading it
// only when the cache does not already hold exactly that many bytes. The size is
// part of the cache key: a file that changed identity cannot be served stale.
func (a *remoteFinalJobAdapter) stagedStockPath(ctx context.Context, id string, size int64) (string, func(), error) {
	dir := finalJobStockCacheDir()
	cached := filepath.Join(dir, fmt.Sprintf("%s-%d.bin", id, size))
	if info, err := os.Stat(cached); err == nil && info.Size() == size {
		return cached, func() {}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create stock cache %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".stock-*")
	if err != nil {
		return "", nil, fmt.Errorf("stage stock file in %q: %w", dir, err)
	}
	tmpPath := tmp.Name()
	discard := func() { _ = os.Remove(tmpPath) }
	reader, _, err := a.stock.DownloadFile(ctx, id)
	if err != nil {
		_ = tmp.Close()
		discard()
		return "", nil, fmt.Errorf("download stock Drive file %q: %w", id, err)
	}
	written, copyErr := io.Copy(tmp, reader)
	readerErr := reader.Close()
	fileErr := tmp.Close()
	if copyErr != nil {
		discard()
		return "", nil, fmt.Errorf("copy stock Drive file %q: %w", id, copyErr)
	}
	if readerErr != nil {
		discard()
		return "", nil, fmt.Errorf("close stock Drive response %q: %w", id, readerErr)
	}
	if fileErr != nil {
		discard()
		return "", nil, fmt.Errorf("close staged stock file %q: %w", id, fileErr)
	}
	if written != size {
		discard()
		return "", nil, fmt.Errorf("stock Drive file %q downloaded %d bytes, expected %d", id, written, size)
	}
	if err := os.Rename(tmpPath, cached); err != nil {
		discard()
		return "", nil, fmt.Errorf("commit stock cache entry %q: %w", cached, err)
	}
	return cached, func() {}, nil
}

// ResolveFinalJobStockAsset retains the explicitly allowed Drive path for
// selected stock-folder videos. Every other final-job asset goes through the
// HTTPS Objectstore resolver above.
func (a *remoteFinalJobAdapter) ResolveFinalJobStockAsset(ctx context.Context, id string) (map[string]any, error) {
	return a.resolveOnce("drive:"+strings.TrimSpace(id), func() (map[string]any, error) {
		return a.resolveStockAssetOnce(ctx, id)
	})
}

func (a *remoteFinalJobAdapter) resolveStockAssetOnce(ctx context.Context, id string) (map[string]any, error) {
	defer a.resolverClock()()
	started := time.Now()
	defer func() {
		log.Printf("final_job resolve kind=drive id=%s ms=%d", id, time.Since(started).Milliseconds())
	}()
	if a == nil || a.stock == nil {
		return nil, fmt.Errorf("final-job stock Drive reader is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\\x00\r\n") {
		return nil, fmt.Errorf("final-job stock Drive id is invalid")
	}
	meta, err := a.stock.GetFileMeta(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read stock Drive metadata %q: %w", id, err)
	}
	if meta == nil || meta.Size <= 0 {
		return nil, fmt.Errorf("stock Drive file %q has no positive byte size", id)
	}

	path, cleanup, err := a.stagedStockPath(ctx, id, meta.Size)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	// A staged stock file is written once, by us, from the Drive bytes of that
	// exact id and size — the same identity the staging cache already trusts to
	// serve the file without downloading it again. Hashing, probing and signing
	// those same bytes on every later handoff is therefore repeated work, not
	// verification: the receipt written next to the file answers all three.
	// Fail-closed: a receipt that does not describe exactly this file is dropped
	// and the bytes are re-derived.
	if receipt, ok := readFinalJobStockReceipt(path, meta.Size); ok {
		ref, publishErr := scriptgen.PublishFinalJobLocalObjectVerified(ctx, "stock-sha256-"+receipt.SHA256[:24], path, receipt.SHA256, receipt.SizeBytes)
		if publishErr == nil {
			ref["duration_ms"] = receipt.DurationMS
			return ref, nil
		}
	}
	hash, size, err := digest.SHA256File(path)
	if err != nil {
		return nil, fmt.Errorf("hash stock Drive file %q: %w", id, err)
	}
	if a.probe == nil {
		return nil, fmt.Errorf("canonical media probe is not configured")
	}
	info, probeErr := a.probe.Probe(ctx, path)
	if info == nil || !info.HasVideo {
		return nil, fmt.Errorf("probe stock Drive video %q: video stream is missing", id)
	}
	resolved := asset.ResolveAssetDuration(info.Duration.Microseconds(), probeErr, 0, false)
	if !resolved.Known() {
		return nil, fmt.Errorf("probe stock Drive video %q: duration is unknown: %w", id, probeErr)
	}
	durationMS := resolved.DurationUS / 1000
	assetID := "stock-sha256-" + hash[:24]
	ref, err := scriptgen.PublishFinalJobLocalObject(ctx, assetID, path, hash, size)
	if err != nil {
		return nil, fmt.Errorf("publish stock Drive file %q to HTTPS Objectstore: %w", id, err)
	}
	ref["duration_ms"] = durationMS
	writeFinalJobStockReceipt(path, finalJobStockReceipt{SHA256: hash, SizeBytes: size, DurationMS: durationMS})
	return ref, nil
}

// finalJobStockReceipt is what resolving a staged stock file produced: its
// content address and the duration the probe reported. It is bound to the exact
// staged file (size) so a re-downloaded or replaced file can never be served
// from a stale receipt.
type finalJobStockReceipt struct {
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	DurationMS int64  `json:"duration_ms"`
}

func finalJobStockReceiptPath(staged string) string { return staged + ".resolve.json" }

func readFinalJobStockReceipt(staged string, size int64) (finalJobStockReceipt, bool) {
	payload, err := os.ReadFile(finalJobStockReceiptPath(staged))
	if err != nil {
		return finalJobStockReceipt{}, false
	}
	var receipt finalJobStockReceipt
	if err := json.Unmarshal(payload, &receipt); err != nil {
		return finalJobStockReceipt{}, false
	}
	if len(receipt.SHA256) != 64 || receipt.SizeBytes != size || receipt.DurationMS <= 0 {
		return finalJobStockReceipt{}, false
	}
	return receipt, true
}

// writeFinalJobStockReceipt is best-effort: a handoff that cannot persist the
// receipt is slower next time, never wrong.
func writeFinalJobStockReceipt(staged string, receipt finalJobStockReceipt) {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return
	}
	_ = os.WriteFile(finalJobStockReceiptPath(staged), payload, 0o644)
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
