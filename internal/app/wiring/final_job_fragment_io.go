package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Fragment I/O: the work identity of a fragment, the lock that makes identical
// work run once, the certified bytes each key resolves to, and the source
// staging that feeds them (one download and one hash per immutable source).

// Fragment cache: the work identity of a fragment, the lock that makes identical work
// run once, and the certified bytes each key resolves to.
// lockFragment waits only for identical cacheable work and honors cancellation.
// A failed producer leaves no cache entry, so the next waiter retries normally.
func (p *finalJobSegmentPreparer) lockFragment(ctx context.Context, key string) (func(), error) {
	p.fragmentLocksMu.Lock()
	if p.fragmentLocks == nil {
		p.fragmentLocks = make(map[string]*finalJobFragmentLock)
	}
	lock := p.fragmentLocks[key]
	if lock == nil {
		lock = &finalJobFragmentLock{ready: make(chan struct{}, 1)}
		lock.ready <- struct{}{}
		p.fragmentLocks[key] = lock
	}
	lock.users++
	p.fragmentLocksMu.Unlock()
	releaseUser := func() {
		p.fragmentLocksMu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(p.fragmentLocks, key)
		}
		p.fragmentLocksMu.Unlock()
	}
	select {
	case <-ctx.Done():
		releaseUser()
		return nil, ctx.Err()
	case <-lock.ready:
		if err := ctx.Err(); err != nil {
			lock.ready <- struct{}{}
			releaseUser()
			return nil, err
		}
		return func() { lock.ready <- struct{}{}; releaseUser() }, nil
	}
}

// finalJobFragmentCacheKey is the WORK identity of a fragment: the same source
// bytes, the same window inside them, the same encoder policy. It deliberately
// excludes the timeline revision and the segment position, so re-running a film
// after an editorial change reuses every window that did not move instead of
// re-encoding the whole program.
// fragmentWorkKey is the ONE identity of cacheable fragment work: source bytes,
// window, encoder policy and the producing path (copy or encode). The cache, the
// work lock and the tests that pin that contract all ask for it here, so a
// second composition of the key cannot drift from the one production uses.
func (p *finalJobSegmentPreparer) fragmentWorkKey(request scriptgen.FinalJobVideoSegmentRequest, sourceSHA, codec string) string {
	path := "encode-only"
	if finalJobWindowOnGOPGrid(request) && finalJobCopyWindowsEnabled() {
		path = "copy-enabled"
	}
	return p.fragmentCacheKey(request, sourceSHA, path+"|"+codec)
}

func (p *finalJobSegmentPreparer) fragmentCacheKey(request scriptgen.FinalJobVideoSegmentRequest, sourceSHA, codec string) string {
	policy := strings.ToLower(strings.TrimSpace(codec)) + "|" +
		strings.ToLower(strings.TrimSpace(p.policy.Preset)) + "|" + strconv.Itoa(p.policy.CRF)
	// The placement tag is part of the work identity: a tagged re-prepare must
	// produce its own object, never the untagged bytes that caused the
	// collision. Without it, the r58-r62 retry was answered by the cache and the
	// "same bytes, another placement" guard fired every time.
	return digest.SHA256String(fmt.Sprintf("v%d|%s|%s|%s|%d|%d|%s|%s", finalJobFragmentFormatVersion, finalJobSegmentProfile, finalJobDelivery.VideoExtradataSHA256, strings.ToLower(sourceSHA), request.SourceInUS, request.FrameCount, policy, request.PlacementTag))
}

// cachedFragment returns the path and certified identity of a previously
// prepared fragment. Its first use in this process is checked against bytes;
// later checks use the kernel verifier's size/mtime invalidation rule.
func (p *finalJobSegmentPreparer) cachedFragment(key string, frameCount, durationUS int64) (string, string, int64, bool) {
	if p.cacheDir == "" || len(key) < 2 {
		return "", "", 0, false
	}
	base := filepath.Join(p.cacheDir, key[:2], key)
	payload, err := os.ReadFile(base + ".json")
	if err != nil {
		return "", "", 0, false
	}
	var entry finalJobFragmentCacheEntry
	if err := json.Unmarshal(payload, &entry); err != nil {
		return "", "", 0, false
	}
	if entry.ProfileID != finalJobSegmentProfile || entry.VideoExtradataSHA256 != finalJobDelivery.VideoExtradataSHA256 || len(entry.SHA256) != 64 || entry.SizeBytes <= 0 ||
		entry.FrameCount != frameCount || entry.DurationUS != durationUS {
		return "", "", 0, false
	}
	path := base + ".mp4"
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.SizeBytes {
		return "", "", 0, false
	}
	sha, size, err := p.verifyLocalFile(path)
	if err != nil || size != entry.SizeBytes || !strings.EqualFold(sha, entry.SHA256) {
		return "", "", 0, false
	}
	return path, entry.SHA256, entry.SizeBytes, true
}

// storeFragment keeps a certified fragment for the next run. Failures are
// silent: the cache is an optimization and must never fail a render.
func (p *finalJobSegmentPreparer) storeFragment(key, path, sha string, size, frameCount, durationUS int64) {
	if p.cacheDir == "" || len(key) < 2 {
		return
	}
	dir := filepath.Join(p.cacheDir, key[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	dest, err := os.CreateTemp(dir, key+".mp4-*.tmp")
	if err != nil {
		return
	}
	tmp := dest.Name()
	defer func() { _ = os.Remove(tmp) }()
	_ = dest.Close()
	_ = os.Remove(tmp)
	// The freshly hashed output is immutable for the rest of this call. When
	// the staging and cache directories share a filesystem, link the exact inode
	// instead of reading and writing the whole encoded fragment once more.
	if err := os.Link(path, tmp); err != nil {
		// Cross-device caches cannot hardlink the staging output; retain the safe
		// copy path for those deployments.
		source, openErr := os.Open(path)
		if openErr != nil {
			return
		}
		dest, createErr := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			_ = source.Close()
			return
		}
		_, copyErr := io.Copy(dest, source)
		closeDestErr := dest.Close()
		closeSourceErr := source.Close()
		if copyErr != nil || closeDestErr != nil || closeSourceErr != nil {
			return
		}
	}
	if err := os.Rename(tmp, filepath.Join(dir, key+".mp4")); err != nil {
		_ = os.Remove(tmp)
		return
	}
	payload, err := json.Marshal(finalJobFragmentCacheEntry{
		SHA256: strings.ToLower(sha), SizeBytes: size, FrameCount: frameCount, DurationUS: durationUS, ProfileID: finalJobSegmentProfile,
		VideoExtradataSHA256: finalJobDelivery.VideoExtradataSHA256,
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, key+".json"), payload, 0o644)
}

// rememberFragmentPath records where a certified fragment's bytes live so a
// duplicate placement in the same run can reuse them without a transfer.
func (p *finalJobSegmentPreparer) rememberFragmentPath(sha, path string) {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if len(sha) != 64 || strings.TrimSpace(path) == "" {
		return
	}
	p.pathsMu.Lock()
	defer p.pathsMu.Unlock()
	if p.paths == nil {
		p.paths = make(map[string]string)
	}
	p.paths[sha] = path
}

func (p *finalJobSegmentPreparer) fragmentPath(sha string) string {
	p.pathsMu.Lock()
	path := p.paths[strings.ToLower(strings.TrimSpace(sha))]
	p.pathsMu.Unlock()
	if path == "" {
		return ""
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return ""
	}
	// The remembered path can outlive a cache write or be rewritten IN PLACE by
	// another process at the same size inside one filesystem timestamp tick, so
	// the (size, mtime) memo cannot answer this question: a certificate must
	// come from the bytes themselves. The caller falls back to a verified
	// download when they no longer match the claimed fragment identity.
	actualSHA, _, err := digest.SHA256File(path)
	if err != nil || !strings.EqualFold(actualSHA, sha) {
		return ""
	}
	return path
}

func finalJobFragmentCacheDir(dataDir string) string {
	raw := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_FRAGMENT_CACHE_DIR"))
	switch strings.ToLower(raw) {
	case "off", "disabled", "none":
		return ""
	case "":
		if strings.TrimSpace(dataDir) == "" {
			return ""
		}
		return filepath.Join(dataDir, "final-job-fragments")
	default:
		return raw
	}
}

// finalJobFragmentCacheEntry is the sidecar of a cached fragment: the certified
// identity of the bytes next to it. A missing or mismatched sidecar is a cache
// miss, never a guess.
type finalJobFragmentCacheEntry struct {
	SHA256               string `json:"sha256"`
	SizeBytes            int64  `json:"size_bytes"`
	FrameCount           int64  `json:"frame_count"`
	DurationUS           int64  `json:"duration_us"`
	ProfileID            string `json:"profile_id"`
	VideoExtradataSHA256 string `json:"video_extradata_sha256"`
}

type finalJobFragmentLock struct {
	ready chan struct{}
	users int
}

// Source staging: download, verify and lock one immutable source once, then hand every
// window of it the same path instead of re-reading it per fragment.
// sourceNeedsNormalization probes a source ONCE and reports whether the encode
// still has to rebuild the delivery geometry (resolution, frame rate, chroma)
// or only has to cut the window. The answer is memoized by content hash: a
// fragment is a window of bytes we already looked at, so paying a probe per
// fragment would redo the same inspection N times.
func (p *finalJobSegmentPreparer) sourceNeedsNormalization(ctx context.Context, path, sha string) (bool, error) {
	if path == "" || sha == "" {
		return true, nil
	}
	// The census probe already read width/height/fps/pix_fmt. On the copy
	// fallback it has run moments earlier for the same bytes, so reuse its
	// answer instead of spawning a second ffprobe for the same stream header.
	key := strings.ToLower(strings.TrimSpace(sha))
	p.certificatesMu.Lock()
	if census, ok := p.certificates[key]; ok {
		p.certificatesMu.Unlock()
		return census.needsNormalization, nil
	}
	p.certificatesMu.Unlock()
	value, err, _ := p.geometryFlights.Do(key, func() (any, error) {
		p.geometryMu.Lock()
		if needs, ok := p.geometry[key]; ok {
			p.geometryMu.Unlock()
			return needs, nil
		}
		p.geometryMu.Unlock()
		ffprobe, err := exec.LookPath("ffprobe")
		if err != nil {
			return nil, fmt.Errorf("ffprobe is required to inspect final-job sources: %w", err)
		}
		cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
			"-show_entries", "stream=width,height,avg_frame_rate,pix_fmt,sample_aspect_ratio", "-of", "json", path)
		data, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("probe final-job source: %w", err)
		}
		var probe struct {
			Streams []struct {
				Width    int    `json:"width"`
				Height   int    `json:"height"`
				AvgFPS   string `json:"avg_frame_rate"`
				PixelFmt string `json:"pix_fmt"`
				SAR      string `json:"sample_aspect_ratio"`
			} `json:"streams"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			return nil, fmt.Errorf("decode final-job source probe: %w", err)
		}
		if len(probe.Streams) != 1 {
			return nil, fmt.Errorf("final-job source must expose exactly one video stream")
		}
		stream := probe.Streams[0]
		needs := !finalJobDeliveryMatches(stream.Width, stream.Height, stream.AvgFPS, stream.PixelFmt, stream.SAR)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.geometryMu.Lock()
		if p.geometry == nil {
			p.geometry = make(map[string]bool)
		}
		p.geometry[key] = needs
		p.geometryMu.Unlock()
		return needs, nil
	})
	if err != nil {
		return false, err
	}
	needs, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("final-job source geometry probe returned an invalid result")
	}
	return needs, nil
}

// lockSource serializes preparation of the same source bytes across lanes: the
// first fragment downloads them, every other lane waits and reads the verified
// copy instead of asking for the same bytes a second time.
func (p *finalJobSegmentPreparer) lockSource(key string) func() {
	p.sourcesMu.Lock()
	if p.sources == nil {
		p.sources = make(map[string]*sync.Mutex)
	}
	lock := p.sources[key]
	if lock == nil {
		lock = &sync.Mutex{}
		p.sources[key] = lock
	}
	p.sourcesMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// stagedSource returns a verified local copy of the source bytes and how long
// obtaining it took. The bytes are hash-addressed, so they are fetched ONCE per
// source: a source cache hit costs one stat and one hash, never a transfer.
func (p *finalJobSegmentPreparer) stagedSource(ctx context.Context, sourceURL, expectedSHA string, expectedSize int64, workDir string) (string, time.Duration, error) {
	stage := func(target string) (string, time.Duration, error) {
		started := time.Now()
		if err := p.downloadAndVerify(ctx, sourceURL, expectedSHA, expectedSize, target); err != nil {
			return "", 0, err
		}
		return target, time.Since(started), nil
	}
	if p.cacheDir == "" || len(expectedSHA) < 2 {
		return stage(filepath.Join(workDir, "source"))
	}
	dir := filepath.Join(p.cacheDir, "sources", expectedSHA[:2])
	cached := filepath.Join(dir, expectedSHA+".mp4")
	unlock := p.lockSource(expectedSHA)
	defer unlock()
	if info, err := os.Stat(cached); err == nil && info.Mode().IsRegular() && info.Size() == expectedSize {
		// A cached source is only trusted after the shared digest authority has
		// verified it. Subsequent uses reuse that proof while stat identity holds.
		if sha, size, err := p.verifyLocalFile(cached); err == nil && size == expectedSize && strings.EqualFold(sha, expectedSHA) {
			return cached, 0, nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return stage(filepath.Join(workDir, "source"))
	}
	tmp := cached + ".download"
	_ = os.Remove(tmp)
	staged, elapsed, err := stage(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	if err := os.Rename(staged, cached); err != nil {
		// A cache that cannot commit is still a usable local copy for this
		// fragment; the next run simply downloads again.
		return staged, elapsed, nil
	}
	return cached, elapsed, nil
}

func (p *finalJobSegmentPreparer) downloadAndVerify(ctx context.Context, sourceURL, expectedSHA string, expectedSize int64, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("build HTTPS source request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("download source fragment over HTTPS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTPS source returned status %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != expectedSize {
		return fmt.Errorf("HTTPS source length %d does not match declared size %d", resp.ContentLength, expectedSize)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create staged source fragment: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, finalJobSegmentMaxSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("stage source fragment: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged source fragment: %w", closeErr)
	}
	if written != expectedSize || written > finalJobSegmentMaxSize {
		return fmt.Errorf("staged source size %d does not match declared size %d or exceeds limit", written, expectedSize)
	}
	sha, size, err := p.verifyLocalFile(target)
	if err != nil {
		return fmt.Errorf("verify staged source identity: %w", err)
	}
	if !strings.EqualFold(sha, expectedSHA) || size != expectedSize {
		return fmt.Errorf("staged source bytes do not match the declared SHA-256 and size")
	}
	return nil
}

// verifyLocalFile delegates all preparer-side file identity checks to the
// kernel digest verifier. Content-addressed files are immutable after publish;
// a stat change invalidates the memo and forces a new SHA-256 read.
func (p *finalJobSegmentPreparer) verifyLocalFile(path string) (string, int64, error) {
	if p != nil && p.verifier != nil {
		return p.verifier.Verify(path)
	}
	return digest.SHA256File(path)
}

// downloadReusedFragment stages a published fragment by its content address. It
// has no declared size to compare against, so the digest is the only authority:
// bytes that do not hash to the expected SHA-256 never reach the muxer.
func (p *finalJobSegmentPreparer) downloadReusedFragment(ctx context.Context, sourceURL, expectedSHA, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("build HTTPS fragment request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("download certified fragment over HTTPS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("certified fragment returned status %d", resp.StatusCode)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create staged fragment: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, finalJobSegmentMaxSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("stage certified fragment: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged fragment: %w", closeErr)
	}
	if written <= 0 || written > finalJobSegmentMaxSize {
		return fmt.Errorf("staged fragment size %d is outside the accepted range", written)
	}
	sha, _, err := p.verifyLocalFile(target)
	if err != nil {
		return fmt.Errorf("hash staged fragment: %w", err)
	}
	if !strings.EqualFold(sha, expectedSHA) {
		return fmt.Errorf("staged fragment bytes do not match the certified SHA-256")
	}
	return nil
}
