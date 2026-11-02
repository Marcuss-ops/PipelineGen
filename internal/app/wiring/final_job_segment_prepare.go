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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	finalJobSegmentProfile = "VELOX_ASSEMBLY_READY_V1"
	finalJobSegmentFPS     = int64(24)
	finalJobSegmentGOP     = int64(48)
	finalJobSegmentMaxSize = int64(2 << 30)
	// Four lanes keep the fragment encode inside the CPU budget the map
	// compositor and the GPU lanes already use on the same host while cutting
	// the serial handoff to roughly a quarter.
	defaultFinalJobFragmentLanes = 4
	maxFinalJobFragmentLanes     = 16
	// finalJobFragmentFormatVersion is part of every cache key. It changes when
	// the BYTES of a fragment can change for the same work — the container of a
	// tagged placement did (v2) — so a cache written by an older build can never
	// answer for the newer layout. Bumping it costs one re-encode of the corpus.
	// v4: a duplicate placement is now DERIVED from the certified object by a
	// packet-copy remux instead of being encoded a second time.
	// v5 distinguishes the actual encoder selected by the codec override.
	// v6 separates optional packet-copy windows from encoded fragments.
	// v7: the key no longer carries the producing path (copy vs encode), which
	// was a distinction without a difference for the certified bytes.
	finalJobFragmentFormatVersion = 7
)

// finalJobSegmentPreparer creates only timeline fragments, never the final
// video. Inputs that already carry a complete, trusted assembly certificate
// are reused; incompatible windows are cut and encoded once locally, then
// uploaded to Objectstore. Worker 51 receives only HTTPS, copy-ready pieces.
type finalJobSegmentPreparer struct {
	ffmpeg string
	policy mediaexec.EncoderPolicy
	client *http.Client
	// cacheDir holds certified fragments keyed by their WORK identity (source
	// bytes + source in-point + frame count + encoder policy). Empty disables
	// reuse, which is how an A/B run measures the cache itself.
	cacheDir string
	// lanes bounds how many fragments may be prepared at once.
	lanes int

	// stats accumulates the measured phase cost of every fragment this
	// preparer produced, so the receipt can say whether the local handoff is
	// dominated by transferring sources or by encoding windows instead of
	// leaving one opaque number. It is diagnostics only: no decision reads it.
	mu    sync.Mutex
	stats finalJobSegmentStats
	// sources serializes the one download of a given source hash. Several
	// fragments are windows of the SAME asset, and downloading the whole source
	// again for each window is a repeated identical transfer, not work.
	sourcesMu sync.Mutex
	sources   map[string]*sync.Mutex
	// geometry memoizes whether a source already matches the delivery geometry
	// (1920x1080, 24 fps, yuv420p). Probing it once per source is what lets the
	// encoder skip rebuilding a picture that is already built.
	geometryMu sync.Mutex
	geometry   map[string]bool
	// certificates memoizes the source census by content hash. Every window of
	// one source asks the same question ("are these bytes really
	// VELOX_ASSEMBLY_READY_V1 with a closed fixed GOP?") and the answer cannot
	// change within a run: the source is content-addressed. Probing it once per
	// source is what makes a copied window cheaper than an encoded one.
	certificatesMu sync.Mutex
	certificates   map[string]finalJobSourceCensus
	// fragmentLocks coalesces identical cacheable work within this preparer.
	// Entries are removed when the last holder or waiter leaves.
	fragmentLocksMu sync.Mutex
	fragmentLocks   map[string]*finalJobFragmentLock
	// paths maps a certified fragment's SHA-256 to the local file that holds
	// its bytes in this process. A duplicate placement is normally derived
	// from a fragment produced seconds earlier by the same run, so the bytes
	// never have to cross the network a second time.
	pathsMu sync.Mutex
	paths   map[string]string
}

// finalJobSegmentStats is the raw accumulator behind the receipt's
// segment_prep block: it is only ever added to, and a handoff reports the
// difference between the totals before and after its own payload build, so two
// concurrent runs can never report each other's fragments as their own.
type finalJobSegmentStats struct {
	segments   int
	reused     int
	cached     int
	derived    int
	copied     int
	downloadMS int64
	encodeMS   int64
	verifyMS   int64
	publishMS  int64
	bytes      int64
}

func (s finalJobSegmentStats) minus(base finalJobSegmentStats) scriptgen.RemoteFinalJobSegmentPrepTiming {
	return scriptgen.RemoteFinalJobSegmentPrepTiming{
		Segments:   s.segments - base.segments,
		Reused:     s.reused - base.reused,
		Cached:     s.cached - base.cached,
		Derived:    s.derived - base.derived,
		Copied:     s.copied - base.copied,
		DownloadMS: s.downloadMS - base.downloadMS,
		EncodeMS:   s.encodeMS - base.encodeMS,
		VerifyMS:   s.verifyMS - base.verifyMS,
		PublishMS:  s.publishMS - base.publishMS,
		Bytes:      s.bytes - base.bytes,
	}
}

// Totals snapshots the accumulator (copy-on-read: the caller keeps a value).
func (p *finalJobSegmentPreparer) Totals() finalJobSegmentStats {
	if p == nil {
		return finalJobSegmentStats{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

func (p *finalJobSegmentPreparer) record(update func(*finalJobSegmentStats)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	update(&p.stats)
}

// finalJobFragmentLanes reads VELOX_FINAL_JOB_PREPARE_LANES: how many fragments
// are prepared at once. The work is independent per fragment, so lanes only
// trade wall time for CPU; 0 or a malformed value is a configuration error, not
// a silent default.
func finalJobFragmentLanes() (int, error) {
	raw := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_PREPARE_LANES"))
	if raw == "" {
		return defaultFinalJobFragmentLanes, nil
	}
	lanes, err := strconv.Atoi(raw)
	if err != nil || lanes < 1 || lanes > maxFinalJobFragmentLanes {
		return 0, fmt.Errorf("VELOX_FINAL_JOB_PREPARE_LANES must be an integer between 1 and %d, got %q", maxFinalJobFragmentLanes, raw)
	}
	return lanes, nil
}

// finalJobCopyWindowsEnabled reports whether an aligned certified window may be
// extracted by packet copy instead of being encoded again. Copy is the default
// because it produces the same pictures for a fraction of the cost and every
// copy is still verified before it is published; VELOX_FINAL_JOB_COPY_CERTIFIED_WINDOWS
// exists only to measure the two paths against each other in an A/B run.
// A malformed value is not a silent enable: it falls back to the encoded path.
func finalJobCopyWindowsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_COPY_CERTIFIED_WINDOWS"))) {
	case "", "1", "true", "on", "enabled", "yes":
		return true
	default:
		return false
	}
}

// finalJobSourceCensus memoizes, per source hash, the only two facts the copy
// path needs: how many frames the bytes hold and whether those bytes are
// VELOX_ASSEMBLY_READY_V1 with a closed fixed GOP.
type finalJobSourceCensus struct {
	frames    int64
	certified bool
}

// sourceCensus answers "can a window of this source be cut by
// packet copy?" and it is derived, never declared: the frame count comes from
// the packets, the GOP cadence from the packet flags, and the stream contract
// from the stream header. A source whose metadata claims VELOX_ASSEMBLY_READY_V1
// but whose bytes do not is not copyable, and a source that declares nothing
// but whose bytes are conformant is.
func (p *finalJobSegmentPreparer) sourceCensus(ctx context.Context, sha, path string) (int64, bool) {
	key := strings.ToLower(strings.TrimSpace(sha))
	p.certificatesMu.Lock()
	census, ok := p.certificates[key]
	p.certificatesMu.Unlock()
	if ok {
		return census.frames, census.certified
	}
	probe, err := probeFinalJobFragment(ctx, path)
	if err != nil {
		return 0, false
	}
	if err := ctx.Err(); err != nil {
		// A cancelled context says nothing about the bytes: never memoize it.
		return 0, false
	}
	census.frames, census.certified = probe.frames(), probe.certify(probe.frames()) == nil
	p.certificatesMu.Lock()
	if p.certificates == nil {
		p.certificates = make(map[string]finalJobSourceCensus)
	}
	p.certificates[key] = census
	p.certificatesMu.Unlock()
	return census.frames, census.certified
}

// finalJobFragmentCacheDir resolves where certified fragments are kept between
// runs. "off" (or an explicit empty value) disables reuse: an A/B run needs a
// way to measure the cache instead of trusting it.
func finalJobFragmentCacheDir(dataDir string) string {
	raw := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_FRAGMENT_CACHE_DIR"))
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

func newFinalJobSegmentPreparer(ffmpeg string, policy mediaexec.EncoderPolicy, cacheDir string, lanes int) *finalJobSegmentPreparer {
	if lanes < 1 {
		lanes = 1
	}
	return &finalJobSegmentPreparer{
		ffmpeg:   strings.TrimSpace(ffmpeg),
		policy:   policy,
		client:   &http.Client{Timeout: 20 * time.Minute},
		cacheDir: strings.TrimSpace(cacheDir),
		lanes:    lanes,
	}
}

// Parallelism lets the plan builder prepare independent fragments concurrently;
// it never changes WHICH fragments are prepared or in what order they bind.
func (p *finalJobSegmentPreparer) Parallelism() int {
	if p == nil || p.lanes < 1 {
		return 1
	}
	return p.lanes
}

// finalJobFragmentCacheEntry is the sidecar of a cached fragment: the certified
// identity of the bytes next to it. A missing or mismatched sidecar is a cache
// miss, never a guess.
type finalJobFragmentCacheEntry struct {
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	FrameCount int64  `json:"frame_count"`
	DurationUS int64  `json:"duration_us"`
	ProfileID  string `json:"profile_id"`
}

type finalJobFragmentLock struct {
	ready chan struct{}
	users int
}

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
func (p *finalJobSegmentPreparer) fragmentCacheKey(request scriptgen.FinalJobVideoSegmentRequest, sourceSHA, codec string) string {
	policy := strings.ToLower(strings.TrimSpace(codec)) + "|" +
		strings.ToLower(strings.TrimSpace(p.policy.Preset)) + "|" + strconv.Itoa(p.policy.CRF)
	// The placement tag is part of the work identity: a tagged re-prepare must
	// produce its own object, never the untagged bytes that caused the
	// collision. Without it, the r58-r62 retry was answered by the cache and the
	// "same bytes, another placement" guard fired every time.
	return digest.SHA256String(fmt.Sprintf("v%d|%s|%s|%d|%d|%s|%s", finalJobFragmentFormatVersion, finalJobSegmentProfile, strings.ToLower(sourceSHA), request.SourceInUS, request.FrameCount, policy, request.PlacementTag))
}

// cachedFragment returns the path and certified identity of a previously
// prepared fragment. The bytes are re-hashed on every hit: a cache is only a
// cache, it can never certify itself.
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
	if entry.ProfileID != finalJobSegmentProfile || len(entry.SHA256) != 64 || entry.SizeBytes <= 0 ||
		entry.FrameCount != frameCount || entry.DurationUS != durationUS {
		return "", "", 0, false
	}
	path := base + ".mp4"
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.SizeBytes {
		return "", "", 0, false
	}
	sha, size, err := digest.SHA256File(path)
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
	source, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = source.Close() }()
	dest, err := os.CreateTemp(dir, key+".mp4-*.tmp")
	if err != nil {
		return
	}
	tmp := dest.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := dest.Close(); err != nil {
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, filepath.Join(dir, key+".mp4")); err != nil {
		_ = os.Remove(tmp)
		return
	}
	payload, err := json.Marshal(finalJobFragmentCacheEntry{
		SHA256: strings.ToLower(sha), SizeBytes: size, FrameCount: frameCount, DurationUS: durationUS, ProfileID: finalJobSegmentProfile,
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, key+".json"), payload, 0o644)
}

func (p *finalJobSegmentPreparer) PrepareFinalJobVideoSegment(ctx context.Context, source map[string]any, request scriptgen.FinalJobVideoSegmentRequest) (map[string]any, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("local fragment encoder is unavailable")
	}
	if request.FrameCount <= 0 || request.SourceInUS < 0 || request.SourceDurationUS <= 0 || request.TimelineStartFrame < 0 || request.TimelineRevision <= 0 || len(request.TimelineSHA256) != 64 {
		return nil, fmt.Errorf("segment has invalid source or timeline bounds")
	}
	inputID := strings.TrimSpace(stringValue(source["asset_id"]))
	inputSHA := strings.ToLower(strings.TrimSpace(stringValue(source["sha256"])))
	inputSize := int64Value(source["size_bytes"])
	sourceURL := strings.TrimSpace(stringValue(source["source_uri"]))
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(stringValue(source["url"]))
	}
	if inputID == "" || len(inputSHA) != 64 || inputSize <= 0 || !strings.HasPrefix(strings.ToLower(sourceURL), "https://") {
		return nil, fmt.Errorf("source asset is missing an immutable HTTPS identity")
	}
	assetID := finalJobPreparedAssetID(request, inputSHA)
	durationUS := framesDurationUS(request.FrameCount)
	if strings.TrimSpace(request.PlacementTag) != "" && len(strings.TrimSpace(request.ReuseFromSHA256)) == 64 {
		// A second placement of bytes this run already certified. Encoding the
		// same window again produces the same pictures at full encode cost, so
		// the placement is DERIVED: the certified bitstream is copied into a
		// distinct container. Same frames, new bytes, no second render.
		return p.derivePlacement(ctx, request, assetID, durationUS, stringValue(source["role"]))
	}
	if request.PlacementTag == "" && finalJobSourceIsExactPreparedFragment(source, request, durationUS) {
		// The existing source already has a signed HTTPS locator and a verified
		// content hash. Change only the per-segment identity; the signed URL is
		// bound to the immutable hash-addressed object, not to asset_id.
		ref := make(map[string]any, len(source)+1)
		for key, value := range source {
			if key == "drive_file_id" || key == "url" || key == "local_path" {
				continue
			}
			ref[key] = value
		}
		ref["asset_id"] = assetID
		prepared := finalJobPreparedRef(ref, inputSHA, inputSize, durationUS, request)
		prepared["role"] = stringValue(source["role"])
		// Reuse costs no phase time, but it is counted so a handoff that
		// prepared nothing cannot be mistaken for one that encoded cheaply.
		p.record(func(s *finalJobSegmentStats) { s.segments++; s.reused++ })
		return prepared, nil
	}
	// Cache: the same source bytes, the same window and the same encoder policy
	// are the same fragment. Encoding it again is the most repeated useless
	// operation of this stage — a retry or an editorial revision of an unmoved
	// window must not pay for it a second time.
	codec, _, err := p.fragmentEncoderSettings()
	if err != nil {
		return nil, err
	}
	copyCandidate := finalJobWindowOnGOPGrid(request) && finalJobCopyWindowsEnabled()
	// The cache key is the WORK identity, not the producing path: a copied and an
	// encoded fragment are the same pictures, so whichever bytes were certified
	// first answer for both. Splitting the key would encode the same window twice
	// the first time an A/B toggles the lever.
	cacheKey := p.fragmentCacheKey(request, inputSHA, codec)
	if p.cacheDir != "" {
		unlock, err := p.lockFragment(ctx, cacheKey)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	cacheStarted := time.Now()
	if cachedPath, cachedSHA, cachedSize, ok := p.cachedFragment(cacheKey, request.FrameCount, durationUS); ok {
		// cachedFragment already re-hashed these exact bytes: publishing must
		// not read and digest them a second time.
		if ref, publishErr := scriptgen.PublishFinalJobLocalObjectVerified(ctx, assetID, cachedPath, cachedSHA, cachedSize); publishErr == nil {
			prepared := finalJobPreparedRef(ref, cachedSHA, cachedSize, durationUS, request)
			prepared["role"] = stringValue(source["role"])
			elapsed := time.Since(cacheStarted).Milliseconds()
			// The bytes are on this disk: a duplicate placement later in the
			// same run derives its object from them, not from the network.
			p.rememberFragmentPath(cachedSHA, cachedPath)
			p.record(func(s *finalJobSegmentStats) { s.segments++; s.cached++; s.publishMS += elapsed })
			return prepared, nil
		}
	}

	tmpDir, err := os.MkdirTemp("", "velox-final-fragment-")
	if err != nil {
		return nil, fmt.Errorf("create fragment staging directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	sourcePath, downloaded, err := p.stagedSource(ctx, sourceURL, inputSHA, inputSize, tmpDir)
	if err != nil {
		return nil, err
	}
	outPath := filepath.Join(tmpDir, "fragment.mp4")
	var encoded, verified time.Duration
	copied := false
	if copyCandidate {
		// Declarations are not proof. The source is probed once per content
		// hash and the census — not its metadata block — decides whether a
		// window at a GOP boundary is the same pictures with fewer packets.
		// If the source is not certified, or the window does not fit, the
		// existing encoder path runs instead of claiming copy safety.
		probeStarted := time.Now()
		frames, certified := p.sourceCensus(ctx, inputSHA, sourcePath)
		start := (request.SourceInUS / 2_000_000) * finalJobSegmentGOP
		if certified && start+request.FrameCount <= frames {
			started := time.Now()
			if err := p.copyCertifiedWindow(ctx, sourcePath, outPath, request); err == nil {
				encoded += time.Since(started)
				if err := verifyFinalJobFragment(ctx, outPath, request.FrameCount); err == nil {
					copied = true
				}
			} else {
				encoded += time.Since(started)
			}
		}
		verified += time.Since(probeStarted) - encoded
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if !copied {
		// The delivery geometry is inspected once per source.
		normalizeGeometry, err := p.sourceNeedsNormalization(ctx, sourcePath, inputSHA)
		if err != nil {
			return nil, err
		}
		encodeStarted := time.Now()
		if err := p.encodeFragment(ctx, sourcePath, outPath, request, normalizeGeometry); err != nil {
			return nil, err
		}
		encoded += time.Since(encodeStarted)
		verifyStarted := time.Now()
		if err := verifyFinalJobFragment(ctx, outPath, request.FrameCount); err != nil {
			return nil, err
		}
		verified += time.Since(verifyStarted)
	}
	sha, size, err := digest.SHA256File(outPath)
	if err != nil {
		return nil, fmt.Errorf("hash prepared fragment: %w", err)
	}
	publishStarted := time.Now()
	// sha/size were computed from this exact file one line above.
	ref, err := scriptgen.PublishFinalJobLocalObjectVerified(ctx, assetID, outPath, sha, size)
	if err != nil {
		return nil, fmt.Errorf("publish prepared fragment to HTTPS Objectstore: %w", err)
	}
	published := time.Since(publishStarted)
	// A certified fragment is worth keeping: the publish above already proved
	// the bytes, so the next run only has to hash them again.
	p.storeFragment(cacheKey, outPath, sha, size, request.FrameCount, durationUS)
	if p.cacheDir != "" {
		p.rememberFragmentPath(sha, filepath.Join(p.cacheDir, cacheKey[:2], cacheKey+".mp4"))
	}
	p.record(func(s *finalJobSegmentStats) {
		s.segments++
		if copied {
			s.copied++
		}
		s.downloadMS += downloaded.Milliseconds()
		s.encodeMS += encoded.Milliseconds()
		s.verifyMS += verified.Milliseconds()
		s.publishMS += published.Milliseconds()
		s.bytes += size
	})
	prepared := finalJobPreparedRef(ref, sha, size, durationUS, request)
	prepared["role"] = stringValue(source["role"])
	return prepared, nil
}

// sourceNeedsNormalization probes a source ONCE and reports whether the encode
// still has to rebuild the delivery geometry (resolution, frame rate, chroma)
// or only has to cut the window. The answer is memoized by content hash: a
// fragment is a window of bytes we already looked at, so paying a probe per
// fragment would redo the same inspection N times.
func (p *finalJobSegmentPreparer) sourceNeedsNormalization(ctx context.Context, path, sha string) (bool, error) {
	if path == "" || sha == "" {
		return true, nil
	}
	p.geometryMu.Lock()
	if p.geometry != nil {
		if needs, ok := p.geometry[sha]; ok {
			p.geometryMu.Unlock()
			return needs, nil
		}
	}
	p.geometryMu.Unlock()
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return false, fmt.Errorf("ffprobe is required to inspect final-job sources: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,avg_frame_rate,pix_fmt", "-of", "json", path)
	data, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("probe final-job source: %w", err)
	}
	var probe struct {
		Streams []struct {
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			AvgFPS   string `json:"avg_frame_rate"`
			PixelFmt string `json:"pix_fmt"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false, fmt.Errorf("decode final-job source probe: %w", err)
	}
	if len(probe.Streams) != 1 {
		return false, fmt.Errorf("final-job source must expose exactly one video stream")
	}
	stream := probe.Streams[0]
	needs := !(stream.Width == 1920 && stream.Height == 1080 && stream.AvgFPS == "24/1" && stream.PixelFmt == "yuv420p")
	p.geometryMu.Lock()
	if p.geometry == nil {
		p.geometry = make(map[string]bool)
	}
	p.geometry[sha] = needs
	p.geometryMu.Unlock()
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
		// A cached source is only trusted after its bytes are re-hashed: the
		// cache can never certify itself.
		if sha, size, err := digest.SHA256File(cached); err == nil && size == expectedSize && strings.EqualFold(sha, expectedSHA) {
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
	sha, size, err := digest.SHA256File(target)
	if err != nil {
		return fmt.Errorf("verify staged source identity: %w", err)
	}
	if !strings.EqualFold(sha, expectedSHA) || size != expectedSize {
		return fmt.Errorf("staged source bytes do not match the declared SHA-256 and size")
	}
	return nil
}

// derivePlacement certifies a second placement of already-certified bytes. The
// assembler binds every placement to its OWN asset with the exact frame span
// (r66: `manifest binding is not exact` when two placements shared one), and
// the plan refuses two assets with one canonical identity (r55), so a duplicate
// placement needs distinct bytes. It does NOT need a second encode: the
// pictures already exist in the fragment this placement repeats, and copying
// them into a tagged container costs a remux instead of a render.
func (p *finalJobSegmentPreparer) derivePlacement(ctx context.Context, request scriptgen.FinalJobVideoSegmentRequest, assetID string, durationUS int64, role string) (map[string]any, error) {
	reuseSHA := strings.ToLower(strings.TrimSpace(request.ReuseFromSHA256))
	tag := strings.TrimSpace(request.PlacementTag)
	// The certified input SHA fully identifies copied packets; no encoder runs.
	cacheKey := p.fragmentCacheKey(request, reuseSHA, "packet-copy")
	if p.cacheDir != "" {
		unlock, err := p.lockFragment(ctx, cacheKey)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	if cachedPath, cachedSHA, cachedSize, ok := p.cachedFragment(cacheKey, request.FrameCount, durationUS); ok {
		ref, publishErr := scriptgen.PublishFinalJobLocalObjectVerified(ctx, assetID, cachedPath, cachedSHA, cachedSize)
		if publishErr == nil {
			p.rememberFragmentPath(cachedSHA, cachedPath)
			p.record(func(s *finalJobSegmentStats) { s.segments++; s.derived++ })
			prepared := finalJobPreparedRef(ref, cachedSHA, cachedSize, durationUS, request)
			prepared["role"] = role
			return prepared, nil
		}
	}
	tmpDir, err := os.MkdirTemp("", "velox-final-derived-")
	if err != nil {
		return nil, fmt.Errorf("create derived placement staging directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	downloaded := time.Duration(0)
	sourcePath := p.fragmentPath(reuseSHA)
	if sourcePath == "" {
		// The reusable fragment came from an earlier run: its certified bytes
		// are already published, so they are fetched once instead of encoded.
		if strings.TrimSpace(request.ReuseFromURI) == "" {
			return nil, fmt.Errorf("placement %q cannot reuse fragment %q: no local copy and no certified URI", tag, reuseSHA)
		}
		sourcePath = filepath.Join(tmpDir, "reused.mp4")
		started := time.Now()
		if err := p.downloadReusedFragment(ctx, request.ReuseFromURI, reuseSHA, sourcePath); err != nil {
			return nil, err
		}
		downloaded = time.Since(started)
	}
	outPath := filepath.Join(tmpDir, "placement.mp4")
	encodeStarted := time.Now()
	if err := p.remuxTaggedFragment(ctx, sourcePath, outPath, tag); err != nil {
		return nil, err
	}
	remuxed := time.Since(encodeStarted)
	verifyStarted := time.Now()
	if err := verifyFinalJobFragment(ctx, outPath, request.FrameCount); err != nil {
		return nil, err
	}
	verified := time.Since(verifyStarted)
	sha, size, err := digest.SHA256File(outPath)
	if err != nil {
		return nil, fmt.Errorf("hash derived placement: %w", err)
	}
	if strings.EqualFold(sha, reuseSHA) {
		return nil, fmt.Errorf("derived placement %q still certifies the bytes of %q; the container tag did not reach the object", tag, reuseSHA)
	}
	publishStarted := time.Now()
	ref, err := scriptgen.PublishFinalJobLocalObjectVerified(ctx, assetID, outPath, sha, size)
	if err != nil {
		return nil, fmt.Errorf("publish derived placement to HTTPS Objectstore: %w", err)
	}
	published := time.Since(publishStarted)
	p.storeFragment(cacheKey, outPath, sha, size, request.FrameCount, durationUS)
	if p.cacheDir != "" {
		p.rememberFragmentPath(sha, filepath.Join(p.cacheDir, cacheKey[:2], cacheKey+".mp4"))
	}
	p.record(func(s *finalJobSegmentStats) {
		s.segments++
		s.derived++
		s.downloadMS += downloaded.Milliseconds()
		// The remux is reported as encode time so a run's receipt keeps one
		// meaning per field: "time spent producing fragment bytes".
		s.encodeMS += remuxed.Milliseconds()
		s.verifyMS += verified.Milliseconds()
		s.publishMS += published.Milliseconds()
		s.bytes += size
	})
	prepared := finalJobPreparedRef(ref, sha, size, durationUS, request)
	prepared["role"] = role
	return prepared, nil
}

// remuxTaggedFragment copies the certified bitstream into a distinct container.
// No pixel is decoded: the tag lives in the container metadata, so the pictures,
// the keyframes and the closed GOP are exactly the ones the source certified.
func (p *finalJobSegmentPreparer) remuxTaggedFragment(ctx context.Context, source, output, tag string) error {
	ffmpeg := p.ffmpeg
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return fmt.Errorf("ffmpeg is required to derive a duplicate placement: %w", err)
		}
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", source,
		"-map", "0:v:0", "-an", "-c", "copy",
		// Same timing grid as the encoder path, so a downstream probe sees the
		// certified fraction-of-a-second timing and not a rounded one.
		"-video_track_timescale", "90000",
		"-map_metadata", "-1",
		"-metadata", "chronon_placement=" + tag,
		// MP4 only writes a non-standard key when use_metadata_tags is set;
		// without it the tag is silently dropped and the copy would certify the
		// SAME bytes as its source, which the plan refuses.
		"-movflags", "+faststart+use_metadata_tags",
		output,
	}
	outputBytes, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("derive duplicate placement by packet copy: %w: %s", err, strings.TrimSpace(string(outputBytes)))
	}
	return nil
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
	// The remembered path can outlive a cache write or be modified by another
	// process. Never derive a certificate from bytes that no longer match the
	// claimed fragment identity; the caller falls back to verified download.
	actualSHA, _, err := digest.SHA256File(path)
	if err != nil || !strings.EqualFold(actualSHA, sha) {
		return ""
	}
	return path
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
	sha, _, err := digest.SHA256File(target)
	if err != nil {
		return fmt.Errorf("hash staged fragment: %w", err)
	}
	if !strings.EqualFold(sha, expectedSHA) {
		return fmt.Errorf("staged fragment bytes do not match the certified SHA-256")
	}
	return nil
}

func (p *finalJobSegmentPreparer) fragmentEncoderSettings() (string, string, error) {
	codec := strings.TrimSpace(p.policy.Codec)
	preset := strings.TrimSpace(p.policy.Preset)
	// VELOX_FINAL_JOB_FRAGMENT_CODEC moves ONE boundary — the local fragment
	// encoder — without touching the media policy every other stage resolves.
	// It exists so the hardware path is measurable and reversible: an empty or
	// "policy" value keeps the resolved codec, and any other token is a
	// configuration error instead of a silent substitution.
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_FRAGMENT_CODEC"))) {
	case "", "policy", "default":
	case "nvenc", "h264_nvenc", "gpu":
		codec = "h264_nvenc"
	case "x264", "libx264", "cpu":
		codec = "libx264"
	default:
		return "", "", fmt.Errorf("VELOX_FINAL_JOB_FRAGMENT_CODEC must be policy, nvenc or x264, got %q", os.Getenv("VELOX_FINAL_JOB_FRAGMENT_CODEC"))
	}
	if !strings.Contains(strings.ToLower(codec), "264") || preset == "" || p.policy.CRF <= 0 {
		return "", "", fmt.Errorf("final-job fragment requires a resolved H.264 encoder policy")
	}
	if strings.Contains(strings.ToLower(codec), "nvenc") {
		preset = nvencPresetFor(preset)
	}
	return codec, preset, nil
}

func (p *finalJobSegmentPreparer) encodeFragment(ctx context.Context, source, output string, request scriptgen.FinalJobVideoSegmentRequest, normalizeGeometry bool) error {
	ffmpeg := p.ffmpeg
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return fmt.Errorf("ffmpeg is required to prepare local final-job fragments: %w", err)
		}
	}
	codec, preset, err := p.fragmentEncoderSettings()
	if err != nil {
		return err
	}
	hardware := strings.Contains(strings.ToLower(codec), "nvenc")
	start := float64(request.SourceInUS) / 1_000_000
	padDuration := float64(request.FrameCount) / float64(finalJobSegmentFPS)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-ss", strconv.FormatFloat(start, 'f', 6, 64), "-i", source,
	}
	tail := "tpad=stop_mode=clone:stop_duration=" + strconv.FormatFloat(padDuration, 'f', 6, 64)
	videoFilter := tail
	if normalizeGeometry {
		videoFilter = "fps=24/1,scale=1920:1080:force_original_aspect_ratio=decrease:flags=lanczos,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,format=yuv420p," + tail
	}
	args = append(args, "-map", "0:v:0", "-an", "-vf", videoFilter)
	args = append(args,
		// Bound output by the plan's exact frame count. Do not use -t here:
		// timestamp rounding at a short source boundary can cut the last planned
		// frame even with tpad enabled. tpad supplies enough cloned tail for the
		// encoder to reach the frame cap; the cap prevents any longer freeze.
		"-frames:v", strconv.FormatInt(request.FrameCount, 10), "-vsync", "cfr",
		"-c:v", codec, "-preset", preset,
	)
	if hardware {
		args = append(args, "-rc", "vbr", "-cq", strconv.Itoa(p.policy.CRF), "-b:v", "0")
	} else {
		args = append(args, "-crf", strconv.Itoa(p.policy.CRF))
	}
	args = append(args,
		"-profile:v", "high", "-level:v", "4.0",
		"-pix_fmt", "yuv420p", "-r", "24", "-g", strconv.FormatInt(finalJobSegmentGOP, 10),
	)
	// sc_threshold and keyint_min are libx264 private options; NVENC has its own
	// scene-cut switch. The certified stream needs the same fixed GOP either way.
	if hardware {
		args = append(args, "-no-scenecut", "1")
	} else {
		args = append(args, "-keyint_min", strconv.FormatInt(finalJobSegmentGOP, 10), "-sc_threshold", "0")
	}
	args = append(args,
		"-bf", "0", "-flags", "+cgop",
		"-video_track_timescale", "90000", "-map_metadata", "-1",
	)
	movflags := "+faststart"
	if tag := strings.TrimSpace(request.PlacementTag); tag != "" {
		// Same pictures, distinct container: a placement tag gives a second
		// placement its own certified object without touching a pixel. The
		// metadata flag is required, or MP4 drops the key and the copy would
		// certify the source bytes.
		args = append(args, "-metadata", "chronon_placement="+tag)
		movflags = "+faststart+use_metadata_tags"
	}
	args = append(args, "-movflags", movflags, output)
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	outputBytes, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("encode assembly-ready fragment: %w: %s", err, strings.TrimSpace(string(outputBytes)))
	}
	return nil
}

// Encoder presets are not portable between libx264 and NVENC. The resolved
// media policy uses x264-style names, so translate those names at the local
// fragment boundary where the configured codec is known to be NVENC.
func nvencPresetFor(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "ultrafast", "superfast", "veryfast", "faster":
		return "p2"
	case "fast", "medium", "slow":
		return "p4"
	case "slower", "veryslow":
		return "p6"
	default:
		return strings.TrimSpace(preset)
	}
}

// finalJobWindowOnGOPGrid reports whether the requested window starts exactly on
// a GOP boundary. Only the geometry is checked here, because whether that
// boundary is a real keyframe is a property of the bytes, not of the request:
// it is answered by the source census immediately before the copy.
func finalJobWindowOnGOPGrid(request scriptgen.FinalJobVideoSegmentRequest) bool {
	return request.SourceInUS >= 0 && request.SourceInUS%2_000_000 == 0 && request.FrameCount > 0
}

func (p *finalJobSegmentPreparer) copyCertifiedWindow(ctx context.Context, source, output string, request scriptgen.FinalJobVideoSegmentRequest) error {
	ffmpeg := p.ffmpeg
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return fmt.Errorf("ffmpeg is required to copy final-job windows: %w", err)
		}
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-ss", strconv.FormatFloat(float64(request.SourceInUS)/1_000_000, 'f', 6, 64), "-i", source,
		"-map", "0:v:0", "-an", "-c", "copy", "-frames:v", strconv.FormatInt(request.FrameCount, 10),
		"-video_track_timescale", "90000", "-map_metadata", "-1", "-movflags", "+faststart", output}
	out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy certified final-job window: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func finalJobSourceIsExactPreparedFragment(source map[string]any, request scriptgen.FinalJobVideoSegmentRequest, durationUS int64) bool {
	return strings.EqualFold(strings.TrimSpace(stringValue(source["profile_id"])), finalJobSegmentProfile) &&
		boolFromAny(source["copy_eligible"]) && boolFromAny(source["first_frame_keyframe"]) && boolFromAny(source["closed_gop"]) &&
		request.SourceInUS == 0 && int64Value(source["source_in_us"]) == 0 && int64Value(source["duration_us"]) == durationUS &&
		int64Value(source["frame_count"]) == request.FrameCount &&
		int64Value(source["width"]) == 1920 && int64Value(source["height"]) == 1080 &&
		int64Value(source["fps_num"]) == finalJobSegmentFPS && int64Value(source["fps_den"]) == 1
}

func finalJobPreparedRef(base map[string]any, sha string, size, durationUS int64, request scriptgen.FinalJobVideoSegmentRequest) map[string]any {
	ref := make(map[string]any, len(base)+13)
	for key, value := range base {
		ref[key] = value
	}
	ref["sha256"], ref["size_bytes"], ref["duration_us"] = strings.ToLower(sha), size, durationUS
	ref["duration_ms"], ref["frame_count"] = (durationUS+999)/1000, request.FrameCount
	ref["mime"], ref["kind"] = "video/mp4", "video"
	ref["profile_id"], ref["copy_eligible"] = finalJobSegmentProfile, true
	ref["first_frame_keyframe"], ref["closed_gop"] = true, true
	ref["width"], ref["height"] = 1920, 1080
	ref["fps_num"], ref["fps_den"] = finalJobSegmentFPS, int64(1)
	ref["timeline_revision"], ref["timeline_sha256"] = request.TimelineRevision, strings.ToLower(request.TimelineSHA256)
	ref["timeline_start_frame"] = request.TimelineStartFrame
	return ref
}

// finalJobPreparedAssetID is the WORK identity of the published fragment: the
// source bytes, the exact window inside them and the number of frames. It
// deliberately excludes the segment id and the timeline position, because the
// assembler binds a placement to the SEGMENT (unique segment_id + position) and
// the asset to the BYTES (unique asset_id + asset_key). Two placements of the
// same pictures are one asset referenced twice, not two assets that happen to
// carry the same hash: the remote resolver rejects two ids sharing one asset_key
// and the render plan refuses two assets with one canonical identity.
func finalJobPreparedAssetID(request scriptgen.FinalJobVideoSegmentRequest, sourceSHA string) string {
	material := fmt.Sprintf("%s\n%d\n%d", strings.ToLower(sourceSHA), request.SourceInUS, request.FrameCount)
	if tag := strings.TrimSpace(request.PlacementTag); tag != "" {
		// A tagged placement certifies DIFFERENT bytes, so it is a different
		// object and must never reuse the untagged asset id.
		material += "\nplacement:" + tag
	}
	return "final-fragment-" + digest.SHA256String(material)[:32]
}

func framesDurationUS(frames int64) int64 {
	return (frames*1_000_000 + finalJobSegmentFPS/2) / finalJobSegmentFPS
}

// finalJobSourceTimeBaseCertified reports whether a source's video-track time
// base is one a 24 fps assembly-ready source may legitimately carry. Two
// authorities are accepted: the delivery grid (1/90000) and x264's container
// time base (1/(fps*512) = 1/12288 at 24 fps), which is what the overlay batch
// encoder writes unless a timescale override is requested. The track timescale
// is metadata, not a copy-safety fact: every fragment is remuxed with
// -video_track_timescale 90000 before it is published, while GOP, B-frames,
// start time and duration remain the gate. Any other time base fails closed.
func finalJobSourceTimeBaseCertified(timeBase string) bool {
	timeBase = strings.TrimSpace(timeBase)
	return timeBase == "1/90000" || timeBase == fmt.Sprintf("1/%d", finalJobSegmentFPS*512)
}

// finalJobFragmentProbe is the raw material of a certificate: one ffprobe run
// over the container, without decoding a single frame. Every field is read from
// the bytes, which is why the census derived from it can be trusted where a
// metadata declaration cannot.
//
// ONE probe for the whole certificate. The keyframe layout is read from the
// packet flags, not from decoded frames: `frame=key_frame` decodes every frame
// of the fragment (~0,9 s for a 3 s clip) while the packet flags are container
// metadata (~0,3 s). Same invariants, same fail-closed verdict, a fraction of
// the cost — and the fragment bytes are never touched.
type finalJobFragmentProbe struct {
	streamOK       bool
	startAtZero    bool
	declaredFrames string
	declaredCount  int64
	declaredKnown  bool
	durationUS     int64
	keyframeAt     map[int64]bool
	packets        int64
}

// frames is the frame count the bytes prove: the certified encoder contract (no
// B-frames) makes packet order equal display order, so the packet count IS the
// frame count.
func (p finalJobFragmentProbe) frames() int64 { return p.packets }

func (p finalJobFragmentProbe) certify(frameCount int64) error {
	if !p.streamOK {
		return fmt.Errorf("prepared fragment stream does not match VELOX_ASSEMBLY_READY_V1")
	}
	if !p.startAtZero {
		return fmt.Errorf("prepared fragment does not start at timestamp zero")
	}
	if p.declaredKnown && p.declaredCount != frameCount {
		return fmt.Errorf("prepared fragment has %q frames; expected %d", p.declaredFrames, frameCount)
	}
	if p.durationUS < framesDurationUS(frameCount)-1_000 || p.durationUS > framesDurationUS(frameCount)+1_000 {
		return fmt.Errorf("prepared fragment duration does not match its frame count")
	}
	for index := int64(0); index < frameCount; index++ {
		shouldKey := index == 0 || index%finalJobSegmentGOP == 0
		if p.keyframeAt[index] != shouldKey {
			return fmt.Errorf("prepared fragment GOP is not closed and fixed at frame %d", index)
		}
	}
	if p.packets != frameCount {
		return fmt.Errorf("prepared fragment keyframe probe returned %d frames; expected %d", p.packets, frameCount)
	}
	return nil
}

func probeFinalJobFragment(ctx context.Context, path string) (finalJobFragmentProbe, error) {
	var probe finalJobFragmentProbe
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return probe, fmt.Errorf("ffprobe is required to certify prepared final-job fragments: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_streams", "-show_format", "-show_entries", "packet=flags", "-of", "json", path)
	data, err := cmd.Output()
	if err != nil {
		return probe, fmt.Errorf("probe prepared fragment: %w", err)
	}
	var decoded struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
			Profile   string `json:"profile"`
			Level     int    `json:"level"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			PixelFmt  string `json:"pix_fmt"`
			AvgFPS    string `json:"avg_frame_rate"`
			Frames    string `json:"nb_frames"`
			BFrames   int    `json:"has_b_frames"`
			TimeBase  string `json:"time_base"`
			StartTime string `json:"start_time"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Packets []struct {
			Flags string `json:"flags"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return probe, fmt.Errorf("decode prepared fragment probe: %w", err)
	}
	if len(decoded.Streams) != 1 {
		return probe, fmt.Errorf("prepared fragment must contain one video stream and no audio streams")
	}
	s := decoded.Streams[0]
	probe.streamOK = s.CodecName == "h264" && s.Profile == "High" && s.Level == 40 && s.Width == 1920 && s.Height == 1080 &&
		s.PixelFmt == "yuv420p" && s.AvgFPS == "24/1" && s.BFrames == 0 && finalJobSourceTimeBaseCertified(s.TimeBase)
	probe.startAtZero = strings.TrimSpace(s.StartTime) == "0.000000"
	probe.declaredFrames = strings.TrimSpace(s.Frames)
	if count, parseErr := strconv.ParseInt(probe.declaredFrames, 10, 64); parseErr == nil {
		probe.declaredCount, probe.declaredKnown = count, true
	}
	if seconds, parseErr := strconv.ParseFloat(strings.TrimSpace(decoded.Format.Duration), 64); parseErr == nil {
		probe.durationUS = int64(seconds * float64(time.Second) / float64(time.Microsecond))
	}
	probe.keyframeAt = make(map[int64]bool, len(decoded.Packets))
	for index, packet := range decoded.Packets {
		if strings.Contains(strings.ToUpper(strings.TrimSpace(packet.Flags)), "K") {
			probe.keyframeAt[int64(index)] = true
		}
	}
	probe.packets = int64(len(decoded.Packets))
	return probe, nil
}

func verifyFinalJobFragment(ctx context.Context, path string, frameCount int64) error {
	probe, err := probeFinalJobFragment(ctx, path)
	if err != nil {
		return err
	}
	return probe.certify(frameCount)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func int64Value(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		v, _ := n.Int64()
		return v
	case string:
		v, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return v
	default:
		return 0
	}
}

func boolFromAny(value any) bool {
	b, _ := value.(bool)
	return b
}

var _ scriptgen.FinalJobVideoSegmentPreparer = (*finalJobSegmentPreparer)(nil)
