package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"golang.org/x/sync/singleflight"
)

const (
	// A fragment is a video-only assembly input: the final audio is a separate
	// asset, so VELOX_ASSEMBLY_READY_V1 (which pins one audio stream) would be a
	// certificate nobody can honour. The registered video-only profile describes
	// these bytes exactly; plan.Output keeps V1 for the assembled FILM.
	// The assembler's copy gate binds a prepared fragment to the profile IT
	// certifies against (VELOX_ASSEMBLY_READY_V1). r90 proved the coupling: with
	// VELOX_OVERLAY_VIDEO_ONLY_V1 on the fragment, the remote rejected the plan
	// (`segment "segment-0000" manifest binding is not exact`) even though the
	// bytes were exact (86/86 frame counts, durations, zero start). The
	// video-only profile keeps describing the RENDER ARTIFACT (plan
	// output_profile_id); the fragment certificate stays V1 until the gate
	// learns the video-only profile.
	finalJobSegmentProfile = kernelmedia.AssemblyMediaContractID
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
	// v8: copy and remux force a zero start timestamp, so the same window can
	// produce different container bytes than v7.
	// v9: distinguish a copy-enabled request from encode-only work so an A/B
	// cannot silently return a cached artifact produced by the other path.
	finalJobFragmentFormatVersion = 9
)

// finalJobDelivery is the ONE declaration of a fragment's delivery geometry.
// The encoder filters, the source census, the reuse gate and the receipt all
// read it from the kernel media contract instead of restating 1920x1080 / 24 /
// yuv420p: a second copy of that rule is exactly how a window and the
// certificate that describes it drift apart.
var finalJobDelivery = kernelmedia.DefaultAssemblyMediaContract()

// The frame math derives from that same contract, so a change to the contract
// moves the GOP grid and the microsecond conversion with it.
var (
	finalJobSegmentFPS = int64(finalJobDelivery.FPS.Num)
	finalJobSegmentGOP = int64(finalJobDelivery.KeyframeInterval)
)

// finalJobDeliveryMatches reports whether an observed stream already carries the
// contract's delivery geometry. The source census and the fragment probe share
// this ONE comparison, so the encoder cannot "normalize" a source that the
// reuse gate already accepted as conformant.
// finalJobDeliveryLevelCode is the H.264 level as ffprobe reports it (40 for
// "4.0"), derived from the contract so the census and the muxer share one level.
func finalJobDeliveryLevelCode() int {
	code, err := strconv.Atoi(strings.ReplaceAll(finalJobDelivery.VideoLevel, ".", ""))
	if err != nil {
		return 0
	}
	return code
}

func finalJobDeliveryMatches(width, height int, avgFPS, pixelFmt string) bool {
	return width == finalJobDelivery.Width && height == finalJobDelivery.Height &&
		avgFPS == fmt.Sprintf("%d/%d", finalJobDelivery.FPS.Num, finalJobDelivery.FPS.Den) &&
		pixelFmt == finalJobDelivery.PixelFormat
}

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
	geometryMu      sync.Mutex
	geometry        map[string]bool
	geometryFlights singleflight.Group
	// certificates memoizes the source census by content hash. Every window of
	// one source asks the same question ("are these bytes really
	// VELOX_ASSEMBLY_READY_V1 with a closed fixed GOP?") and the answer cannot
	// change within a run: the source is content-addressed. Probing it once per
	// source is what makes a copied window cheaper than an encoded one.
	certificatesMu sync.Mutex
	certificates   map[string]finalJobSourceCensus
	// censusFlights makes the first probe for one content identity shared by
	// all lanes. The map above handles completed results; this group handles
	// callers that arrive while the same ffprobe is still running.
	censusFlights singleflight.Group
	// verifier is the shared digest policy applied to this preparer's
	// local artifacts. It avoids rereading an immutable cache hit for every
	// placement while invalidating whenever size or mtime changes.
	verifier *digest.Verifier
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
	// scopeSeq allocates the identity of one handoff, so the counters a receipt
	// publishes cannot mix in another run's work when two handoffs share this
	// preparer.
	scopeSeq atomic.Uint64
	// scoped holds one accumulator per live handoff. The aggregate above stays a
	// log surface only: a receipt reads its own entry, never the difference of
	// two process-wide snapshots that a concurrent handoff can move in between.
	scoped map[uint64]finalJobSegmentStats
}

// The producing lanes a window can take. They are the receipt's vocabulary, so
// they are constants instead of free strings.
const (
	finalJobWindowReuse   = "reuse"
	finalJobWindowCache   = "cache"
	finalJobWindowCopy    = "copy"
	finalJobWindowEncode  = "encode"
	finalJobWindowDerived = "derived"
)

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
		verifier: digest.NewVerifier(nil),
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
		// Reuse costs no phase time, but it is counted so a handoff that
		// prepared nothing cannot be mistaken for one that encoded cheaply.
		return p.finishFragment(ctx, request, ref, stringValue(source["role"]), inputSHA, inputSize, durationUS,
			finalJobSegmentWindow{
				segmentID: request.SegmentID, sourceSHA: inputSHA, path: finalJobWindowReuse,
				frames: request.FrameCount, bytes: inputSize,
			},
			func(s *finalJobSegmentStats) { s.segments++; s.reused++ }), nil
	}
	// Cache: the same source bytes, window, encoder policy and requested
	// preparation path are the same fragment. Keeping copy-enabled and
	// encode-only identities separate prevents a warm cache from masking an A/B
	// change in the operation being measured.
	codec, _, err := p.fragmentEncoderSettings()
	if err != nil {
		return nil, err
	}
	copyCandidate := finalJobWindowOnGOPGrid(request) && finalJobCopyWindowsEnabled()
	cacheKey := p.fragmentWorkKey(request, inputSHA, codec)
	if p.cacheDir != "" {
		unlock, err := p.lockFragment(ctx, cacheKey)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	cacheStarted := time.Now()
	if cachedPath, cachedSHA, cachedSize, ok := p.cachedFragment(cacheKey, request.FrameCount, durationUS); ok {
		// cachedFragment already obtained the identity from the kernel verifier;
		// publishing must not read and digest the same bytes a second time.
		if ref, publishErr := scriptgen.PublishFinalJobLocalObjectVerified(ctx, assetID, cachedPath, cachedSHA, cachedSize); publishErr == nil {
			elapsed := time.Since(cacheStarted).Milliseconds()
			// The bytes are on this disk: a duplicate placement later in the
			// same run derives its object from them, not from the network.
			p.rememberFragmentPath(cachedSHA, cachedPath)
			return p.finishFragment(ctx, request, ref, stringValue(source["role"]), cachedSHA, cachedSize, durationUS,
				finalJobSegmentWindow{
					segmentID: request.SegmentID, sourceSHA: inputSHA, path: finalJobWindowCache,
					frames: request.FrameCount, publishMS: elapsed, bytes: cachedSize,
				},
				func(s *finalJobSegmentStats) { s.segments++; s.cached++; s.publishMS += elapsed }), nil
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
	var paddedFrames int64
	copied := false
	normalized := false
	// The census is both the copy gate and the frame authority for padding, so
	// it is asked once per source whether or not a packet copy is considered.
	// Declarations are not proof: the bytes decide, not the metadata block.
	censusStarted := time.Now()
	census, censusOK := p.sourceCensus(ctx, inputSHA, sourcePath)
	verified += time.Since(censusStarted)
	if copyCandidate {
		start := (request.SourceInUS / 2_000_000) * finalJobSegmentGOP
		if censusOK && census.certified && start+request.FrameCount <= census.frames {
			// The certificate of a copied window is DERIVED, never re-probed:
			// the census already proved the closed fixed GOP, the first keyframe
			// and the packet count of these bytes, and the window starts on a
			// GOP boundary, so a packet copy cannot change the pictures, the
			// cadence or the frame count. A second ffprobe here would just
			// repeat the census it ran one line above.
			started := time.Now()
			copyErr := p.copyCertifiedWindow(ctx, sourcePath, outPath, request)
			encoded += time.Since(started)
			copied = copyErr == nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if !copied {
		// The delivery geometry is inspected once per source (the census above
		// already answered it when it ran).
		normalizeGeometry, err := p.sourceNeedsNormalization(ctx, sourcePath, inputSHA)
		if err != nil {
			return nil, err
		}
		normalized = normalizeGeometry
		// Pad only what the source cannot supply. A window fully inside the
		// source is encoded without tpad, so reaching the frame cap can no
		// longer freeze a tail that was already there.
		paddedFrames = finalJobSourceShortfall(census, censusOK, request)
		encodeStarted := time.Now()
		if err := p.encodeFragment(ctx, sourcePath, outPath, request, normalizeGeometry, paddedFrames); err != nil {
			return nil, err
		}
		encoded += time.Since(encodeStarted)
		// No per-fragment probe: the command itself pins the contract (High 4.0,
		// 1920x1080, yuv420p, 24/1, bf=0, closed GOP 48, PTS 0) and the tests
		// certify a real encoded fragment. Probing every encode at runtime
		// repeats a build-time proof on our own output.
	}
	sha, size, err := p.verifyLocalFile(outPath)
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
	// One record per produced window. The producing lane is what separates the
	// copy lever from the encoder; `encoded` above deliberately counts both so
	// the aggregate keeps one meaning per field.
	window := finalJobSegmentWindow{
		segmentID: request.SegmentID, sourceSHA: inputSHA, normalized: normalized, path: finalJobWindowEncode,
		frames: request.FrameCount, downloadMS: downloaded.Milliseconds(),
		verifyMS: verified.Milliseconds(), publishMS: published.Milliseconds(), bytes: size,
		paddedFrames: paddedFrames,
	}
	if copied {
		window.path = finalJobWindowCopy
		window.copyMS = encoded.Milliseconds()
	} else {
		window.encodeMS = encoded.Milliseconds()
	}
	return p.finishFragment(ctx, request, ref, stringValue(source["role"]), sha, size, durationUS, window,
		func(s *finalJobSegmentStats) {
			s.segments++
			if copied {
				s.copied++
			}
			s.paddedFrames += paddedFrames
			s.downloadMS += downloaded.Milliseconds()
			s.encodeMS += encoded.Milliseconds()
			s.verifyMS += verified.Milliseconds()
			s.publishMS += published.Milliseconds()
			s.bytes += size
		}), nil
}

// finishFragment is the ONE exit of every fragment-producing lane: it stamps the
// plan reference, names the producing role, and attributes the work to the
// handoff that asked for it. Keeping reuse, cache, derived placement and fresh
// production on this path is what stops the reference shape and the receipt
// from drifting when one lane changes.
func (p *finalJobSegmentPreparer) finishFragment(
	ctx context.Context,
	request scriptgen.FinalJobVideoSegmentRequest,
	base map[string]any,
	role, sha string,
	size, durationUS int64,
	window finalJobSegmentWindow,
	update func(*finalJobSegmentStats),
) map[string]any {
	prepared := finalJobPreparedRef(base, sha, size, durationUS, request)
	prepared["role"] = role
	p.record(ctx, update)
	p.recordWindow(ctx, window)
	return prepared
}

// finalJobSourceShortfall is how many tail frames a window asks for that the
// source cannot supply. It is zero for a window fully inside the source, which
// is the normal case; only a boundary window is padded, and only by its deficit.
// Without a census the source length is unknown, so the historical full-length
// tail is kept rather than cutting the plan's frame cap.
func finalJobSourceShortfall(census finalJobSourceCensus, ok bool, request scriptgen.FinalJobVideoSegmentRequest) int64 {
	if !ok {
		return request.FrameCount
	}
	startFrame := (request.SourceInUS*finalJobSegmentFPS + 500_000) / 1_000_000
	available := census.frames - startFrame
	if available < 0 {
		available = 0
	}
	if available >= request.FrameCount {
		return 0
	}
	return request.FrameCount - available
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

// --- per-source census: one probe per content identity ---

// The per-source census: the one probe that answers whether a window of these bytes can
// be cut by packet copy, shared by every window of the same source.
// sourceCensus answers "can a window of this source be cut by
// packet copy?" and it is derived, never declared: the frame count comes from
// the packets, the GOP cadence from the packet flags, and the stream contract
// from the stream header. A source whose metadata claims VELOX_ASSEMBLY_READY_V1
// but whose bytes do not is not copyable, and a source that declares nothing
// but whose bytes are conformant is.
func (p *finalJobSegmentPreparer) sourceCensus(ctx context.Context, sha, path string) (finalJobSourceCensus, bool) {
	key := strings.ToLower(strings.TrimSpace(sha))
	p.certificatesMu.Lock()
	census, ok := p.certificates[key]
	p.certificatesMu.Unlock()
	if ok {
		return census, true
	}
	value, err, _ := p.censusFlights.Do(key, func() (any, error) {
		// Another lane may have completed the census after our first lookup.
		p.certificatesMu.Lock()
		cached, found := p.certificates[key]
		p.certificatesMu.Unlock()
		if found {
			return cached, nil
		}
		probe, probeErr := probeFinalJobFragment(ctx, path)
		if probeErr != nil {
			return nil, probeErr
		}
		if err := ctx.Err(); err != nil {
			// Cancellation says nothing about the bytes; do not memoize it.
			return nil, err
		}
		result := finalJobSourceCensus{
			frames: probe.frames(), certified: probe.certify(probe.frames()) == nil,
			needsNormalization: probe.needsDeliveryGeometry(),
		}
		p.certificatesMu.Lock()
		if p.certificates == nil {
			p.certificates = make(map[string]finalJobSourceCensus)
		}
		p.certificates[key] = result
		p.certificatesMu.Unlock()
		return result, nil
	})
	if err != nil {
		return finalJobSourceCensus{}, false
	}
	census, ok = value.(finalJobSourceCensus)
	if !ok {
		return finalJobSourceCensus{}, false
	}
	return census, true
}

type finalJobSourceCensus struct {
	frames             int64
	certified          bool
	needsNormalization bool
}
