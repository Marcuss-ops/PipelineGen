package script

import (
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"

	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Runtime-asset resolution for the remote final job: the identity cache that
// keeps a handoff from re-resolving one source per fragment. Stock resolution
// is metadata-only; the Master owns the bytes.

// resolverClock accumulates the runtime-asset resolution time of one handoff.
// It answers the only question the receipt could not: how much of handoff_ms is
// spent finding and publishing sources rather than encoding fragments.
func (a *RemoteFinalJobAdapter) resolverClock() func() {
	started := time.Now()
	return func() {
		if a != nil {
			a.resolverNS.Add(time.Since(started).Nanoseconds())
		}
	}
}

func (a *RemoteFinalJobAdapter) ResolveFinalJobAsset(ctx context.Context, id string) (scriptgen.FinalJobRuntimeAssetV1, error) {
	ref, err := a.resolveOnce("objectstore:"+strings.TrimSpace(id), func() (map[string]any, error) {
		defer a.resolverClock()()
		started := time.Now()
		ref, err := a.ResolveFinalJobObjectStoreAsset(ctx, id)
		log.Printf("final_job resolve kind=objectstore id=%s bytes=%v ms=%d err=%v",
			id, ref["size_bytes"], time.Since(started).Milliseconds(), err != nil)
		return ref, err
	})
	if err != nil {
		return scriptgen.FinalJobRuntimeAssetV1{}, err
	}
	return finaljob.RuntimeAssetFromMap(ref)
}

// resolveOnce answers a repeated resolution from the handoff's own cache. The
// cached reference is copied on the way out so a caller cannot mutate the entry
// (the payload builder stamps roles and durations onto the map it receives).
//
// Concurrent callers of the SAME identity share one resolution (single flight):
// two windows can ask for the same source, and without this the second call
// would publish the same bytes again. An error is never cached — a failed
// resolution must not poison the retry.
func (a *RemoteFinalJobAdapter) resolveOnce(key string, build func() (map[string]any, error)) (map[string]any, error) {
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

// beginHandoffCache scopes the per-handoff resolution cache to ONE run without
// throwing away what an earlier attempt of the SAME run already resolved.
func (a *RemoteFinalJobAdapter) beginHandoffCache(runID string) {
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

// ResolveFinalJobStockAsset returns a metadata-only Drive reference. That exact
// reference becomes the FINALIZE stock entry, and the remote Master downloads
// and processes it. The Producer never fetches the stock bytes or turns them
// into a local fragment.
func (a *RemoteFinalJobAdapter) ResolveFinalJobStockAsset(ctx context.Context, id string) (scriptgen.FinalJobRuntimeAssetV1, error) {
	ref, err := a.resolveOnce("drive:"+strings.TrimSpace(id), func() (map[string]any, error) {
		return a.stockDriveReference(ctx, id)
	})
	if err != nil {
		return scriptgen.FinalJobRuntimeAssetV1{}, err
	}
	return finaljob.RuntimeAssetFromMap(ref)
}

func (a *RemoteFinalJobAdapter) stockDriveReference(ctx context.Context, id string) (map[string]any, error) {
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
		if byID, idErr := a.media.ResolveByMediaAssetID(ctx, id); idErr == nil && byID != nil && strings.TrimSpace(byID.DriveFileID()) != "" {
			saved = byID
			id = strings.TrimSpace(byID.DriveFileID())
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
		"duration_us": saved.Duration.Microseconds(),
	}
}

// driveStockDownloadURL is the public HTTPS fetch of the exact Drive bytes.
// The remote Master uses it to download and process the stock window itself.
func driveStockDownloadURL(id string) string {
	return "https://drive.usercontent.google.com/download?id=" + id + "&export=download&confirm=t"
}

// FinalJobPublishedFileSize is retained for the capability port's legacy
// shape, but direct HTTPS delivery never resolves sizes through Drive.
func (a *RemoteFinalJobAdapter) FinalJobPublishedFileSize(context.Context, string) (int64, error) {
	return 0, fmt.Errorf("Drive file metadata is disabled for final-job delivery; use Objectstore HTTPS references")
}

func (a *RemoteFinalJobAdapter) ListFinalJobStockFolder(ctx context.Context, folderID string) ([]scriptgen.FinalJobStockFile, error) {
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
	_ scriptgen.FinalJobSubmitter = (*RemoteFinalJobAdapter)(nil)
	_ scriptgen.FinalJobAttacher  = (*RemoteFinalJobAdapter)(nil)
)
