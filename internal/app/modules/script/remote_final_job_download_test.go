package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

func TestDownloadFinalArtifactVerifiesAndReuses(t *testing.T) {
	payload := []byte("the-final-composite-mp4-bytes")
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-admin-token" {
			t.Errorf("Authorization=%q, want admin bearer", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			return
		}
		hits.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	adapter := &RemoteFinalJobAdapter{
		artifactToken: "test-admin-token",
		client:        &remotejob.Client{HTTP: server.Client()},
	}
	path, size, gotSHA, reused, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, "job-download-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if size != int64(len(payload)) || gotSHA != wantSHA {
		t.Fatalf("size=%d sha=%s, want %d/%s", size, gotSHA, len(payload), wantSHA)
	}
	if reused {
		t.Fatal("a first transfer must report reused=false")
	}
	downloaded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded) != string(payload) {
		t.Fatalf("downloaded bytes changed: %q", downloaded)
	}

	// A retry with the same job/sha must reuse the verified file instead of
	// transferring hundreds of megabytes again.
	_, _, _, reusedAgain, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, "job-download-test")
	if err != nil {
		t.Fatal(err)
	}
	if !reusedAgain {
		t.Fatal("a retry on the verified temp file must report reused=true")
	}
	if hits.Load() != 1 {
		t.Fatalf("artifact was fetched %d times, want reuse after the first", hits.Load())
	}
}

// TestWithDownloadedArtifactMeasuresTransferAndRecordsOperation pins the two
// facts the final-job stage was missing: the artifact transfer is a measured
// phase on the receipt (ms/bytes/reuse) and an operation under
// remote_final_job, so a 891 MB download can no longer hide as unexplained
// stage wall time.
func TestWithDownloadedArtifactMeasuresTransferAndRecordsOperation(t *testing.T) {
	payload := []byte("final-composite-payload-for-transfer-timing")
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])
	// A server-side delay proves the timer wraps the TRANSFER, not just the
	// receipt bookkeeping around it.
	const serverDelay = 40 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(serverDelay)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	adapter := &RemoteFinalJobAdapter{
		artifactToken: "test-admin-token",
		client:        &remotejob.Client{HTTP: server.Client()},
	}
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-transfer", AttemptID: "attempt-transfer"})
	ctx := kernobs.WithRun(context.Background(), run)

	receipt, err := adapter.withDownloadedArtifact(ctx, scriptgen.RemoteFinalJobResult{
		JobID:       "job-transfer",
		Status:      "SUCCEEDED",
		ArtifactURL: server.URL,
		SHA256:      wantSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ArtifactPath == "" {
		t.Fatal("artifact path is empty")
	}
	t.Cleanup(func() { _ = os.Remove(receipt.ArtifactPath) })
	if receipt.Timing == nil {
		t.Fatal("a measured transfer must create the timing block")
	}
	if receipt.Timing.ArtifactDownloadBytes != int64(len(payload)) {
		t.Fatalf("artifact_download_bytes = %d, want %d", receipt.Timing.ArtifactDownloadBytes, len(payload))
	}
	if receipt.Timing.ArtifactDownloadReused {
		t.Fatal("a first transfer must not be marked reused")
	}
	if receipt.Timing.ArtifactDownloadMS < serverDelay.Milliseconds() {
		t.Fatalf("artifact_download_ms = %d, want the measured transfer to cover the %d ms server delay", receipt.Timing.ArtifactDownloadMS, serverDelay.Milliseconds())
	}

	run.Finish()
	report := run.Report()
	found := false
	for _, op := range report.Operations {
		if op.Stage != string(kernobs.StageRunFinalJob) || op.Component != string(kernobs.ComponentRenderQueue) || op.Operation != string(kernobs.OperationDownload) {
			continue
		}
		found = true
		if op.Bytes != int64(len(payload)) || op.CacheStatus != "miss" {
			t.Fatalf("download operation = bytes %d cache %q, want %d/miss", op.Bytes, op.CacheStatus, len(payload))
		}
	}
	if !found {
		t.Fatalf("no remote_final_job/render_queue/download operation recorded: %+v", report.Operations)
	}

	// The deterministic temp path must report the reuse instead of a second
	// transfer, and still be measured (the local SHA-256 of 891 MB is work).
	reused, err := adapter.withDownloadedArtifact(ctx, scriptgen.RemoteFinalJobResult{
		JobID:       "job-transfer",
		Status:      "SUCCEEDED",
		ArtifactURL: server.URL,
		SHA256:      wantSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reused.Timing == nil || !reused.Timing.ArtifactDownloadReused {
		t.Fatalf("reuse timing = %+v, want artifact_download_reused=true", reused.Timing)
	}
}

// TestWithDownloadedArtifactRequiresArtifactToken pins the fail-closed boundary:
// a SUCCEEDED HTTP artifact without the admin bearer must fail here, with a
// typed error, instead of returning an untouched receipt that only the
// persister would reject later.
func TestWithDownloadedArtifactRequiresArtifactToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("never-fetched"))
	}))
	defer server.Close()

	adapter := &RemoteFinalJobAdapter{client: &remotejob.Client{HTTP: server.Client()}}
	receipt, err := adapter.withDownloadedArtifact(context.Background(), scriptgen.RemoteFinalJobResult{
		JobID:       "job-no-token",
		Status:      "SUCCEEDED",
		ArtifactURL: server.URL,
	})
	if !errors.Is(err, ErrRemoteFinalArtifactTokenMissing) {
		t.Fatalf("err = %v, want ErrRemoteFinalArtifactTokenMissing", err)
	}
	if receipt.ArtifactPath != "" {
		t.Fatalf("artifact path = %q, want empty on a configuration failure", receipt.ArtifactPath)
	}
}

// TestDownloadFinalArtifactRemovesPartialOnFailure pins that a rejected
// transfer leaves no .part file behind: the download path is a deterministic
// temp file reused across retries, and a leftover partial would be resumed from
// corrupt bytes.
func TestDownloadFinalArtifactRemovesPartialOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer server.Close()
	adapter := &RemoteFinalJobAdapter{
		artifactToken: "test-admin-token",
		client:        &remotejob.Client{HTTP: server.Client()},
	}
	wantSHA := strings.Repeat("0", 64)
	jobID := "job-part-cleanup"
	seed := digest.SHA256Sum([]byte(jobID + "|" + wantSHA + "|" + server.URL))
	partPath := filepath.Join(os.TempDir(), "pipelinegen-final-"+hex.EncodeToString(seed[:8])+".mp4.part")

	if _, _, _, _, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, jobID); err == nil {
		t.Fatal("expected sha256 mismatch to fail closed")
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind at %s: %v", partPath, err)
	}
}

func TestDownloadFinalArtifactRejectsHashMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer server.Close()
	adapter := &RemoteFinalJobAdapter{
		artifactToken: "test-admin-token",
		client:        &remotejob.Client{HTTP: server.Client()},
	}
	wantSHA := strings.Repeat("0", 64)
	if _, _, _, _, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, "job-hash-mismatch"); err == nil {
		t.Fatal("expected sha256 mismatch to fail closed")
	}
}

func TestDownloadFinalArtifactUsesParallelRanges(t *testing.T) {
	payload := make([]byte, 256<<10)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])
	var active, maxActive atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Header().Set("Accept-Ranges", "bytes")
			return
		}
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			http.Error(w, "range required", http.StatusBadRequest)
			return
		}
		start, end, ok := parseTestRange(rangeHeader, len(payload))
		if !ok {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		current := active.Add(1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()

	t.Setenv("VELOX_ARTIFACT_DOWNLOAD_CONNECTIONS", "4")
	t.Setenv("VELOX_ARTIFACT_DOWNLOAD_PARALLEL_MIN_BYTES", "1024")
	adapter := &RemoteFinalJobAdapter{
		artifactToken: "test-admin-token",
		client:        &remotejob.Client{HTTP: server.Client()},
	}
	path, size, gotSHA, reused, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, "job-parallel-range")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if size != int64(len(payload)) || gotSHA != wantSHA || reused {
		t.Fatalf("size=%d sha=%s reused=%v, want %d/%s/false", size, gotSHA, reused, len(payload), wantSHA)
	}
	if got := maxActive.Load(); got < 2 {
		t.Fatalf("max concurrent range requests = %d, want at least 2", got)
	}
}

func TestDownloadFinalArtifactResumesPartialFile(t *testing.T) {
	payload := make([]byte, 128<<10)
	for i := range payload {
		payload[i] = byte((i * 7) % 253)
	}
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])
	var mu sync.Mutex
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Header().Set("Accept-Ranges", "bytes")
			return
		}
		rangeHeader := r.Header.Get("Range")
		mu.Lock()
		ranges = append(ranges, rangeHeader)
		mu.Unlock()
		start, end, ok := parseTestRange(rangeHeader, len(payload))
		if !ok {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()

	t.Setenv("VELOX_ARTIFACT_DOWNLOAD_CONNECTIONS", "1")
	jobID := "job-resume-part"
	seed := digest.SHA256Sum([]byte(jobID + "|" + wantSHA + "|" + server.URL))
	path := filepath.Join(os.TempDir(), "pipelinegen-final-"+hex.EncodeToString(seed[:8])+".mp4")
	partPath := path + ".part"
	half := len(payload) / 2
	if err := os.WriteFile(partPath, payload[:half], 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path); _ = os.Remove(partPath) })

	adapter := &RemoteFinalJobAdapter{
		artifactToken: "test-admin-token",
		client:        &remotejob.Client{HTTP: server.Client()},
	}
	gotPath, size, gotSHA, reused, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path || size != int64(len(payload)) || gotSHA != wantSHA || reused {
		t.Fatalf("path=%q size=%d sha=%s reused=%v", gotPath, size, gotSHA, reused)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ranges) != 1 || ranges[0] != fmt.Sprintf("bytes=%d-", half) {
		t.Fatalf("range requests = %v, want the resumed bytes=%d-", ranges, half)
	}
}

func parseTestRange(header string, size int) (int, int, bool) {
	header = strings.TrimSpace(strings.TrimPrefix(header, "bytes="))
	parts := strings.SplitN(header, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if strings.TrimSpace(parts[1]) != "" {
		end, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, 0, false
		}
	}
	if end < start || end >= size {
		return 0, 0, false
	}
	return start, end, true
}
