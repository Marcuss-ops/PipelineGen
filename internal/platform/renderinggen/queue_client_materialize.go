package renderinggen

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
	"golang.org/x/sync/errgroup"
)

// Materialize is the LAZY half of the locator-first boundary: it downloads the
// certified artifact located by outcome into destPath, verifying size + digest
// while streaming. It is what a consumer that genuinely needs a local file
// (localization today) calls; the canonical clip.render completion path never
// does, because it commits the locator and the Drive outbox streams from the
// object store. Fail-closed: an outcome with no locator, or a fetch that does
// not match the certified size/digest, is a typed error.
func (e *ClipRenderExecutor) Materialize(ctx context.Context, outcome *cliprender.RenderOutcome, destPath string) (string, error) {
	if outcome == nil {
		return "", fmt.Errorf("renderinggen clip executor: materialize: outcome is nil")
	}
	if strings.TrimSpace(outcome.ArtifactURL) == "" {
		return "", fmt.Errorf("renderinggen clip executor: materialize: outcome carries no artifact URL")
	}
	if strings.TrimSpace(destPath) == "" {
		return "", fmt.Errorf("renderinggen clip executor: materialize: destination path is required")
	}
	sha, size, err := materializeArtifact(ctx, outcome.ArtifactURL, destPath, outcome.SizeBytes, outcome.SHA256)
	if err != nil {
		return "", fmt.Errorf("renderinggen clip executor: materialize certified artifact: %w", err)
	}
	outcome.OutputPath = destPath
	if sha != "" {
		outcome.SHA256 = sha
	}
	if size > 0 {
		outcome.SizeBytes = size
	}
	return destPath, nil
}

var _ cliprender.RenderArtifactMaterializer = (*ClipRenderExecutor)(nil)

// materializeArtifact fetches the certified object-store artifact into a local
// file. It is the shared body of the lazy Materialize path (the eager Settle
// download it used to serve is gone). Single-pass: hashes while streaming
// (network → disk + SHA-256 in one pass, no re-read).
//
// It returns the CERTIFIED digest and byte count of the materialized file:
// the digest is computed from the exact bytes written to disk in the same
// io.Copy that produced them and verified against the queue's expected
// digest, so downstream publication reuses it instead of re-reading the
// artifact (the previous Seek(0)+SHA256Reader form doubled the disk I/O of
// every certified download and forced the publisher into a third read).
func materializeArtifact(ctx context.Context, rawURL, outputPath string, expectedSize int64, expectedSHA string) (string, int64, error) {
	if rawURL == "" || outputPath == "" {
		return "", 0, fmt.Errorf("artifact URL and output path are required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", 0, fmt.Errorf("create output directory: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := objectStoreHTTPClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", 0, fmt.Errorf("artifact download HTTP %d", resp.StatusCode)
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, err
	}
	hasher := digest.NewSHA256()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	gotSHA := hex.EncodeToString(hasher.Sum(nil))
	if expectedSize > 0 && written != expectedSize {
		return "", 0, fmt.Errorf("downloaded size %d, want %d", written, expectedSize)
	}
	if expectedSHA != "" && !strings.EqualFold(gotSHA, expectedSHA) {
		return "", 0, fmt.Errorf("artifact hash %s, want %s", gotSHA, expectedSHA)
	}
	return gotSHA, written, nil
}

// prefetchClipAssets publishes the already-resolved local assets to the
// content-addressed RenderingGen object store. The queue carries hashes, not
// VPS paths; without this boundary a remote/native worker cannot materialize
// the source and subtitle files. This is deliberately before enqueue so a
// job is never claimed with an incomplete asset set.
//
// Each asset is staged with a HEAD-before-PUT probe: an object that already
// exists under its content address is shared with every worker, so re-reading
// and re-uploading it would burn bandwidth, a full RAM buffer and a disk pass
// for zero benefit. Only absent objects are uploaded, streamed straight from
// the source file (constant memory — the historical os.ReadFile path buffered
// every asset in RAM). The declared SHA-256s are certified upstream by the
// plan resolver, so no full-file hash pass is repeated at this boundary; the
// RenderingGen worker independently re-verifies the content address when it
// materializes the asset, so wrong bytes can never reach Chronon.
func prefetchClipAssets(ctx context.Context, plan cliprender.ClipRenderPlanV1, refs []assetRef) error {
	store := strings.TrimRight(os.Getenv("RENDERINGGEN_STORE_URL"), "/")
	if store == "" {
		store = "http://127.0.0.1:9000"
	}
	paths := map[string]string{plan.Source.SHA256: plan.Source.Path}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset {
		paths[plan.Background.SHA256] = plan.Background.Path
	}
	if plan.Subtitles != nil {
		paths[plan.Subtitles.SHA256] = plan.Subtitles.Path
	}
	if plan.Watermark != nil && plan.Watermark.SHA256 != "" {
		paths[plan.Watermark.SHA256] = plan.Watermark.Path
	}
	// The source, background, subtitle and watermark/font objects are
	// independent. Probe/upload them concurrently, but keep a small bound so
	// one render cannot monopolise the object-store connection pool. This is
	// the clip-render equivalent of the shared prefetcher's four-transfer cap.
	var group errgroup.Group
	group.SetLimit(4)
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref := ref
		key := strings.ToLower(strings.TrimSpace(ref.Hash))
		if key == "" {
			continue
		}
		// Deduplicate by content address (never by name/path): the same bytes
		// reachable from several plan slots (e.g. a font referenced by both the
		// watermark and the burn-in subtitles) are staged exactly once.
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		group.Go(func() error {
			path := paths[ref.Hash]
			if path == "" {
				// overlayPlanAssets carries a LocalPath for assets it reads directly
				// (subtitle/watermark fonts); the paths map above only knows the
				// plan-owned files. Prefer the ref's own source so a Poppins
				// subtitle font is uploaded like any other font.
				path = ref.LocalPath
			}
			if path == "" {
				return fmt.Errorf("asset %s has no resolved local path", ref.Hash)
			}
			present, err := objectStored(ctx, store, ref.Hash)
			if err != nil {
				return fmt.Errorf("asset %s probe: %w", ref.Hash, err)
			}
			if present {
				return nil
			}
			if err := streamPutFile(ctx, store, ref.Hash, path); err != nil {
				return fmt.Errorf("upload %s: %w", ref.Hash, err)
			}
			return nil
		})
	}
	return group.Wait()
}

func boolPtr(b bool) *bool { return &b }

func scriptAssets(in []queueclient.AssetRef) []scriptgen.RenderQueueAsset {
	out := make([]scriptgen.RenderQueueAsset, len(in))
	for i, a := range in {
		out[i] = scriptgen.NewRenderQueueAsset(
			kernelasset.Ref{AssetID: a.LogicalPath, SHA256: a.Hash}, a.LogicalPath, a.SourceURL)
	}
	return out
}

// waitClipQueue used to live here: a second implementation of "wait for the
// remote render to reach a terminal state", with its own jitter, its own
// interval clamps and no wait metrics. It is DELETED — the canonical wait is
// scriptgen.WaitRenderQueueTerminal, called from ClipRenderExecutor.Settle.

var (
	_ scriptgen.RenderQueueClient         = (*Client)(nil)
	_ scriptgen.RenderQueueBatchSubmitter = (*Client)(nil)
	_ scriptgen.RenderQueueWaiter         = (*Client)(nil)
	_ scriptgen.RenderQueueRetrier        = (*Client)(nil)
	_ cliprender.RenderExecutor           = (*ClipRenderExecutor)(nil)
)
