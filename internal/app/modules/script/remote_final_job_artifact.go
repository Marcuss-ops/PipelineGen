package script

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

// remote_final_job_artifact.go — receipt projection and local materialization
// of the final MP4 the Master published.

// ErrRemoteFinalArtifactTokenMissing is the fail-fast classification of a
// SUCCEEDED artifact that cannot be fetched because the admin bearer is not
// configured. Before this sentinel the adapter returned the untouched receipt
// and relied on the persister to reject it later; failing here makes the
// operator see the configuration error at the boundary that owns the download,
// before any bytes or wall time are spent.
var ErrRemoteFinalArtifactTokenMissing = errors.New("remote final artifact: admin artifact token is not configured")

// artifactHTTPClient bounds every artifact transfer. There is deliberately no
// Client.Timeout: a multi-hundred-megabyte MP4 must not be truncated by a
// whole-request deadline. The bounds that matter are connection setup and the
// wait for response headers, so a dead origin fails in seconds while the body
// transfer keeps the caller's context deadline as its only overall limit.
var artifactHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

const (
	defaultArtifactDownloadConnections = 4
	maxArtifactDownloadConnections     = 8
	defaultArtifactParallelMinBytes    = int64(8 << 20)
)

// artifactDownloadConnections is the number of Range requests to overlap when
// the origin advertises byte ranges. It is operator-tunable because the
// observed 3–5 MB/s can be a per-connection cap; the default stays modest so a
// single-connection origin is not hit by a thundering herd.
func artifactDownloadConnections() int {
	value := strings.TrimSpace(os.Getenv("VELOX_ARTIFACT_DOWNLOAD_CONNECTIONS"))
	if value == "" {
		return defaultArtifactDownloadConnections
	}
	connections, err := strconv.Atoi(value)
	if err != nil || connections < 1 {
		return defaultArtifactDownloadConnections
	}
	if connections > maxArtifactDownloadConnections {
		return maxArtifactDownloadConnections
	}
	return connections
}

// artifactParallelMinBytes avoids four round-trips for a tiny artifact where
// the probe overhead would exceed any transfer saving.
func artifactParallelMinBytes() int64 {
	value := strings.TrimSpace(os.Getenv("VELOX_ARTIFACT_DOWNLOAD_PARALLEL_MIN_BYTES"))
	if value == "" {
		return defaultArtifactParallelMinBytes
	}
	minimum, err := strconv.ParseInt(value, 10, 64)
	if err != nil || minimum <= 0 {
		return defaultArtifactParallelMinBytes
	}
	return minimum
}

// finalJobResult projects the client's Result onto the capability's durable
// receipt. The id is carried even on a failed wait: the Master job exists, and
// losing its address is what turns a resumable render into a duplicate one.
func finalJobResult(jobID string, remote remotejob.Result) scriptgen.RemoteFinalJobResult {
	id := strings.TrimSpace(remote.JobID)
	if id == "" {
		id = strings.TrimSpace(jobID)
	}
	return scriptgen.RemoteFinalJobResult{JobID: id, Status: remote.Status, WorkerID: remote.WorkerID, ArtifactURL: remote.ArtifactURL, SHA256: remote.SHA256, ReportOwner: scriptgen.FinalJobReportOwner, Timing: finalJobTiming(remote.Timing)}
}

// withDownloadedArtifact materializes the Master's final MP4 locally so the
// artifact persister can hand it to the normal finalize Drive delivery. The
// download is part of the final-job receipt, not an operator command: on a
// SUCCEEDED HTTP artifact the Producer either has the bytes or returns the
// download error. A Drive URL is already published, so there is nothing to
// fetch; a missing artifact token leaves the receipt untouched and the
// persister fails closed with a configuration error.
func (a *RemoteFinalJobAdapter) withDownloadedArtifact(ctx context.Context, receipt scriptgen.RemoteFinalJobResult) (scriptgen.RemoteFinalJobResult, error) {
	if a == nil || !strings.EqualFold(strings.TrimSpace(receipt.Status), "SUCCEEDED") {
		return receipt, nil
	}
	rawURL := strings.TrimSpace(receipt.ArtifactURL)
	lower := strings.ToLower(rawURL)
	if rawURL == "" || (!strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://")) {
		return receipt, nil
	}
	if strings.TrimSpace(a.artifactToken) == "" {
		return receipt, fmt.Errorf("receipt %s: %w", receipt.JobID, ErrRemoteFinalArtifactTokenMissing)
	}
	startedAt := time.Now()
	path, size, sha, reused, err := a.downloadFinalArtifact(ctx, rawURL, receipt.SHA256, receipt.JobID)
	finishedAt := time.Now()
	if receipt.Timing == nil {
		receipt.Timing = &scriptgen.RemoteFinalJobTiming{}
	}
	// Measured on the error path too: an operator must be able to tell a slow
	// transfer from a failed one, and a failed GET still consumed wall time.
	receipt.Timing.ArtifactDownloadMS = finishedAt.Sub(startedAt).Milliseconds()
	receipt.Timing.ArtifactDownloadBytes = size
	receipt.Timing.ArtifactDownloadReused = reused
	if err != nil {
		return receipt, fmt.Errorf("download remote final artifact %s: %w", receipt.JobID, err)
	}
	receipt.ArtifactPath = path
	receipt.ArtifactSizeBytes = size
	receipt.SHA256 = sha
	// The download is an external-boundary operation owned by this adapter, so
	// it is recorded (not re-timed) under the final-job stage. Without this the
	// artifact transfer was wall time with no owner in the run report.
	cacheStatus := "miss"
	if reused {
		cacheStatus = "hit"
	}
	kernobs.RecordOperation(ctx, kernobs.OperationInfo{
		Stage:       kernobs.StageRunFinalJob,
		Component:   kernobs.ComponentRenderQueue,
		Operation:   kernobs.OperationDownload,
		CacheStatus: cacheStatus,
		Items:       1,
		Bytes:       size,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
	}, receipt.Timing.ArtifactDownloadMS)
	return receipt, nil
}

// downloadFinalArtifact materializes the Master artifact with the admin bearer
// and verifies the Master-reported SHA-256 before making the bytes visible
// under their final name. A deterministic `.part` path makes a retry resume
// from the bytes already transferred; when the origin advertises byte ranges
// and the artifact is large enough, independent chunks are fetched in parallel.
func (a *RemoteFinalJobAdapter) downloadFinalArtifact(ctx context.Context, rawURL, wantSHA, jobID string) (string, int64, string, bool, error) {
	seed := digest.SHA256Sum([]byte(jobID + "|" + wantSHA + "|" + rawURL))
	path := filepath.Join(os.TempDir(), "pipelinegen-final-"+hex.EncodeToString(seed[:8])+".mp4")
	partPath := path + ".part"
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		if got, _, hashErr := digest.SHA256File(path); hashErr == nil && (wantSHA == "" || strings.EqualFold(got, wantSHA)) {
			return path, info.Size(), got, true, nil
		}
	}

	client := artifactHTTPClient
	if a.client != nil && a.client.HTTP != nil {
		client = a.client.HTTP
	}
	size, ranges, probeErr := probeArtifact(ctx, client, rawURL, a.artifactToken)
	if probeErr != nil {
		// The probe is an optimization, not a requirement: a server that
		// rejects HEAD and Range still supports the normal full GET.
		size, ranges = 0, false
	}

	// A previous attempt may have completed every byte but crashed before the
	// rename. Hash the part before transferring anything again.
	if size > 0 {
		if info, err := os.Stat(partPath); err == nil && info.Mode().IsRegular() && info.Size() == size {
			if sha, _, verifyErr := verifyArtifactFile(partPath, wantSHA); verifyErr == nil {
				if err := os.Rename(partPath, path); err != nil {
					return "", 0, "", false, fmt.Errorf("commit artifact: %w", err)
				}
				return path, size, sha, false, nil
			}
			_ = os.Remove(partPath)
		}
	}

	connections := artifactDownloadConnections()
	if ranges && size > 0 && size >= artifactParallelMinBytes() && connections > 1 {
		// Parallel chunks must start from a clean file: a resumed hole would be
		// indistinguishable from a chunk that has not been written yet.
		_ = os.Remove(partPath)
		if err := downloadArtifactParallel(ctx, client, rawURL, a.artifactToken, partPath, size, connections); err != nil {
			_ = os.Remove(partPath)
			return "", 0, "", false, err
		}
	} else {
		resume := int64(0)
		if info, err := os.Stat(partPath); err == nil && info.Mode().IsRegular() {
			resume = info.Size()
		}
		if size > 0 && resume > size {
			_ = os.Remove(partPath)
			resume = 0
		}
		if err := downloadArtifactSequential(ctx, client, rawURL, a.artifactToken, partPath, resume); err != nil {
			// Keep the partial file: the next attach retry can resume it.
			return "", 0, "", false, err
		}
	}

	sha, actualSize, err := verifyArtifactFile(partPath, wantSHA)
	if err != nil {
		_ = os.Remove(partPath)
		return "", 0, "", false, err
	}
	if size > 0 && actualSize != size {
		_ = os.Remove(partPath)
		return "", 0, "", false, fmt.Errorf("artifact size mismatch: got %d want %d", actualSize, size)
	}
	if err := os.Rename(partPath, path); err != nil {
		return "", 0, "", false, fmt.Errorf("commit artifact: %w", err)
	}
	return path, actualSize, sha, false, nil
}

// probeArtifact reads the artifact length and whether the origin honors byte
// ranges. It prefers HEAD; servers that reject HEAD are probed with an
// explicit `Range: bytes=0-0`, and a plain 200 response is a valid "no ranges"
// answer.
func probeArtifact(ctx context.Context, client *http.Client, rawURL, token string) (int64, bool, error) {
	head, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err == nil {
		head.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		head.Header.Set("Accept", "video/mp4, application/octet-stream")
		if resp, headErr := client.Do(head); headErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				size := resp.ContentLength
				ranges := strings.Contains(strings.ToLower(resp.Header.Get("Accept-Ranges")), "bytes")
				if size > 0 {
					return size, ranges, nil
				}
			}
		}
	}

	req, err := newArtifactGET(ctx, rawURL, token, 0, 0)
	if err != nil {
		return 0, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("probe artifact range: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		total := parseContentRangeTotal(resp.Header.Get("Content-Range"))
		if total <= 0 {
			return 0, true, fmt.Errorf("range probe response has no total size: %q", resp.Header.Get("Content-Range"))
		}
		return total, true, nil
	case http.StatusOK:
		return resp.ContentLength, false, nil
	default:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, false, fmt.Errorf("probe artifact %s: %s: %s", rawURL, resp.Status, strings.TrimSpace(string(snippet)))
	}
}

func parseContentRangeTotal(value string) int64 {
	slash := strings.LastIndex(value, "/")
	if slash < 0 {
		return 0
	}
	total, err := strconv.ParseInt(strings.TrimSpace(value[slash+1:]), 10, 64)
	if err != nil || total <= 0 {
		return 0
	}
	return total
}

func newArtifactGET(ctx context.Context, rawURL, token string, start, end int64) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build artifact request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "video/mp4, application/octet-stream")
	if start >= 0 {
		if end >= start {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		} else {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
		}
	}
	return req, nil
}

func downloadArtifactSequential(ctx context.Context, client *http.Client, rawURL, token, partPath string, resume int64) error {
	req, err := newArtifactGET(ctx, rawURL, token, resume, -1)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusRequestedRangeNotSatisfiable:
		// The part already covers the whole artifact; verification decides
		// whether it is usable.
		return nil
	case http.StatusOK, http.StatusPartialContent:
	default:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", rawURL, resp.Status, strings.TrimSpace(string(snippet)))
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resume > 0 && resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	tmp, err := os.OpenFile(partPath, flags, 0o600)
	if err != nil {
		return fmt.Errorf("open artifact part: %w", err)
	}
	_, copyErr := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("write artifact: %w", errors.Join(copyErr, closeErr))
	}
	return nil
}

func downloadArtifactParallel(ctx context.Context, client *http.Client, rawURL, token, partPath string, size int64, connections int) error {
	tmp, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create artifact part: %w", err)
	}
	if err := tmp.Truncate(size); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("size artifact part: %w", err)
	}

	chunkCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, connections)
	for i := 0; i < connections; i++ {
		start := size * int64(i) / int64(connections)
		end := size*int64(i+1)/int64(connections) - 1
		if end < start {
			continue
		}
		wg.Add(1)
		go func(start, end int64) {
			defer wg.Done()
			req, reqErr := newArtifactGET(chunkCtx, rawURL, token, start, end)
			if reqErr != nil {
				errCh <- reqErr
				cancel()
				return
			}
			resp, respErr := client.Do(req)
			if respErr != nil {
				errCh <- fmt.Errorf("range bytes=%d-%d: %w", start, end, respErr)
				cancel()
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusPartialContent {
				snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				errCh <- fmt.Errorf("range bytes=%d-%d: %s: %s", start, end, resp.Status, strings.TrimSpace(string(snippet)))
				cancel()
				return
			}
			want := end - start + 1
			if resp.ContentLength >= 0 && resp.ContentLength != want {
				errCh <- fmt.Errorf("range bytes=%d-%d: content length %d, want %d", start, end, resp.ContentLength, want)
				cancel()
				return
			}
			if _, copyErr := io.Copy(io.NewOffsetWriter(tmp, start), resp.Body); copyErr != nil {
				errCh <- fmt.Errorf("range bytes=%d-%d: %w", start, end, copyErr)
				cancel()
				return
			}
		}(start, end)
	}
	wg.Wait()
	close(errCh)
	var firstErr error
	for err := range errCh {
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		_ = tmp.Close()
		return firstErr
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync artifact part: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close artifact part: %w", err)
	}
	return nil
}

func verifyArtifactFile(path, wantSHA string) (string, int64, error) {
	got, size, err := digest.SHA256File(path)
	if err != nil {
		return "", 0, err
	}
	if wantSHA != "" && !strings.EqualFold(got, wantSHA) {
		return got, size, fmt.Errorf("artifact sha256 mismatch: got %s want %s", got, wantSHA)
	}
	return got, size, nil
}
