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
	"strings"
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

// downloadFinalArtifact GETs the Master artifact with the admin bearer and
// verifies the Master-reported SHA-256 before making the bytes visible under
// their final name. A deterministic temp path makes an attach retry reuse the
// already-downloaded bytes instead of transferring hundreds of megabytes again.
func (a *RemoteFinalJobAdapter) downloadFinalArtifact(ctx context.Context, rawURL, wantSHA, jobID string) (string, int64, string, bool, error) {
	seed := digest.SHA256Sum([]byte(jobID + "|" + wantSHA + "|" + rawURL))
	path := filepath.Join(os.TempDir(), "pipelinegen-final-"+hex.EncodeToString(seed[:8])+".mp4")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		if got, _, hashErr := digest.SHA256File(path); hashErr == nil && (wantSHA == "" || strings.EqualFold(got, wantSHA)) {
			return path, info.Size(), got, true, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, "", false, fmt.Errorf("build artifact request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(a.artifactToken))
	req.Header.Set("Accept", "video/mp4, application/octet-stream")
	client := artifactHTTPClient
	if a.client != nil && a.client.HTTP != nil {
		client = a.client.HTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, "", false, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", 0, "", false, fmt.Errorf("GET %s: %s: %s", rawURL, resp.Status, strings.TrimSpace(string(snippet)))
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".part-*")
	if err != nil {
		return "", 0, "", false, fmt.Errorf("create artifact temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	h := digest.NewSHA256()
	size, copyErr := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		cleanup()
		return "", 0, "", false, fmt.Errorf("write artifact: %w", errors.Join(copyErr, closeErr))
	}
	got := hex.EncodeToString(h.Sum(nil))
	if wantSHA != "" && !strings.EqualFold(got, wantSHA) {
		cleanup()
		return "", 0, "", false, fmt.Errorf("artifact sha256 mismatch: got %s want %s", got, wantSHA)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return "", 0, "", false, fmt.Errorf("commit artifact: %w", err)
	}
	return path, size, got, false, nil
}
