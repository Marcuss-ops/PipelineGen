package wiring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestFinalJobRememberedFragmentRejectsTampering(t *testing.T) {
	p := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{}, t.TempDir(), 1)
	path := filepath.Join(t.TempDir(), "fragment.mp4")
	body := []byte("certified fragment")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sha := digest.SHA256String(string(body))
	p.rememberFragmentPath(sha, path)
	if got := p.fragmentPath(sha); got != path {
		t.Fatalf("verified path = %q, want %q", got, path)
	}
	if err := os.WriteFile(path, []byte("tampered fragment!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := p.fragmentPath(sha); got != "" {
		t.Fatalf("tampered path %q accepted for certified digest", got)
	}
}

func TestFinalJobExactFragmentReuseRequiresZeroRequestedInPoint(t *testing.T) {
	source := map[string]any{
		"profile_id": finalJobSegmentProfile, "copy_eligible": true, "first_frame_keyframe": true, "closed_gop": true,
		"video_extradata_sha256": kernelmedia.CanonicalVideoExtradataSHA256,
		"source_in_us":           int64(0), "duration_us": int64(2_000_000), "frame_count": int64(48),
		"width": 1920, "height": 1080, "fps_num": int64(24), "fps_den": int64(1),
	}
	request := scriptgen.FinalJobVideoSegmentRequest{SourceInUS: 0, FrameCount: 48}
	if !finalJobSourceIsExactPreparedFragment(source, request, 2_000_000) {
		t.Fatal("exact whole-source window should be eligible")
	}
	request.SourceInUS = 1_000_000
	if finalJobSourceIsExactPreparedFragment(source, request, 2_000_000) {
		t.Fatal("nonzero requested in-point must not reuse the whole source")
	}
	request.SourceInUS = 0
	source["closed_gop"] = false
	if finalJobSourceIsExactPreparedFragment(source, request, 2_000_000) {
		t.Fatal("uncertified source must not be reused")
	}
}

func TestFinalJobCopyWindowEligibility(t *testing.T) {
	request := scriptgen.FinalJobVideoSegmentRequest{SourceInUS: 2_000_000, FrameCount: 24}
	if !finalJobWindowOnGOPGrid(request) {
		t.Fatal("aligned window should be eligible for the census")
	}
	for _, tc := range []struct{ start, frames int64 }{{2_000_001, 24}, {-1, 24}, {0, 0}} {
		request.SourceInUS, request.FrameCount = tc.start, tc.frames
		if finalJobWindowOnGOPGrid(request) {
			t.Fatalf("off-grid window accepted: %+v", request)
		}
	}
}

// The census is derived from the bytes, so a conformant source is copyable even
// when it declares nothing, and a source that declares conformance it does not
// have is not copyable.
func TestFinalJobSourceCensusIsDerivedNotDeclared(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	encode := func(name string, extra ...string) string {
		path := filepath.Join(dir, name)
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-frames:v", "96", "-an",
			"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-level:v", "4.0",
			"-pix_fmt", "yuv420p", "-r", "24", "-video_track_timescale", "90000"}
		args = append(args, extra...)
		args = append(args, path)
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
		return path
	}
	certified := encode("certified.mp4", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-bf", "0", "-flags", "+cgop")
	open := encode("open.mp4", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-bf", "2")

	p := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{}, "", 1)
	census, ok := p.sourceCensus(context.Background(), digest.SHA256String(certified), certified)
	if !ok || census.frames != 96 || !census.certified {
		t.Fatalf("conformant source census = %+v, certified=%v", census, ok)
	}
	if openCensus, ok := p.sourceCensus(context.Background(), digest.SHA256String(open), open); !ok || openCensus.certified {
		t.Fatal("a source with B-frames must not be certified for packet copy")
	}
	// The memoized answer is keyed by content hash, not by path.
	if again, ok := p.sourceCensus(context.Background(), digest.SHA256String(certified), open); !ok || again.frames != 96 || !again.certified {
		t.Fatalf("census was not memoized: %+v, certified=%v", again, ok)
	}
}

// A source is not less copyable because its container uses x264's time base:
// the fragment is remuxed onto the 1/90000 delivery grid before publication.
func TestFinalJobSourceTimeBaseCertified(t *testing.T) {
	for _, timeBase := range []string{"1/90000", "1/12288"} {
		if !finalJobSourceTimeBaseCertified(timeBase) {
			t.Fatalf("time base %q must certify a 24 fps assembly source", timeBase)
		}
	}
	for _, timeBase := range []string{"", "1/1000", "1/12287", "1/180000", "24/1", "1/90000 extras"} {
		if finalJobSourceTimeBaseCertified(timeBase) {
			t.Fatalf("time base %q must fail closed", timeBase)
		}
	}
}

// The packet table is the frame-count authority: MP4 writers are allowed to
// leave nb_frames absent (fragmented or concatenated overlays do), and a
// probe of the bytes still proves every frame. A DECLARED count that
// disagrees stays a rejection.
func TestFinalJobCertificateAcceptsUnknownDeclaredFrameCount(t *testing.T) {
	probe := finalJobFragmentProbe{
		streamOK:    true,
		startAtZero: true,
		durationUS:  framesDurationUS(96),
		keyframeAt:  map[int64]bool{0: true, 48: true},
		packets:     96,
	}
	if err := probe.certify(96); err != nil {
		t.Fatalf("an absent declared frame count must not reject certified bytes: %v", err)
	}
	probe.declaredKnown, probe.declaredCount = true, 95
	if err := probe.certify(96); err == nil {
		t.Fatal("a declared frame count that disagrees with the packets must still be rejected")
	}
}

// The census must accept the real x264-container source shape (1/12288), not
// only a source already written on the delivery grid.
func TestFinalJobCensusAcceptsX264TimeBase(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "x264-timebase.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-frames:v", "96", "-an",
		"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-level:v", "4.0",
		"-pix_fmt", "yuv420p", "-r", "24", "-refs", "1",
		"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-bf", "0", "-flags", "+cgop", source}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("create x264-timebase source: %v: %s", err, out)
	}
	p := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{}, "", 1)
	census, ok := p.sourceCensus(context.Background(), digest.SHA256String(source), source)
	if !ok || census.frames != 96 || !census.certified {
		t.Fatalf("x264-timebase source census = %+v, certified=%v", census, ok)
	}
}

// Regression pin on the artifact that actually cost the r-series its copy
// window: the real overlay batch (1607 frames, bf=0, GOP 48, 1/12288). It is
// skipped in checkouts that never ran the job on this host.
func TestFinalJobRealOverlayArtifactIsCopyable(t *testing.T) {
	matches, _ := filepath.Glob("../../../data/final-job-fragments/sources/b1/b1486eecefc5*.mp4")
	if len(matches) != 1 {
		t.Skip("local r64/r65 overlay artifact not present")
	}
	probe, err := probeFinalJobFragment(context.Background(), matches[0])
	if err != nil {
		t.Fatalf("probe real overlay artifact: %v", err)
	}
	if got, want := probe.frames(), int64(1607); got != want {
		t.Fatalf("real overlay artifact frames = %d, want %d", got, want)
	}
	if err := probe.certify(probe.frames()); err != nil {
		t.Fatalf("real overlay artifact must be copyable: %v", err)
	}
}

func TestFinalJobCopyWindowPreservesFramesAndCertificate(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-frames:v", "96",
		"-an", "-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-level:v", "4.0",
		"-pix_fmt", "yuv420p", "-r", "24", "-refs", "1",
		"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-bf", "0", "-flags", "+cgop", "-video_track_timescale", "90000", "-movflags", "+faststart", source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create source: %v: %s", err, out)
	}
	if err := verifyFinalJobFragment(context.Background(), source, 96); err != nil {
		t.Fatalf("source certificate: %v", err)
	}
	p := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{}, "", 1)
	window := filepath.Join(dir, "window.mp4")
	request := scriptgen.FinalJobVideoSegmentRequest{SourceInUS: 2_000_000, FrameCount: 24}
	if err := p.copyCertifiedWindow(context.Background(), source, window, request); err != nil {
		t.Fatal(err)
	}
	if err := verifyFinalJobFragment(context.Background(), window, request.FrameCount); err != nil {
		t.Fatalf("copied window certificate: %v", err)
	}
	decode := func(path string, start string) string {
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
		if start != "" {
			args = append(args, "-ss", start)
		}
		args = append(args, "-i", path, "-map", "0:v:0", "-frames:v", "24", "-f", "framemd5", "-")
		out, err := exec.Command(ffmpeg, args...).Output()
		if err != nil {
			t.Fatalf("decode frames: %v", err)
		}
		var frames []string
		for _, line := range strings.Split(string(out), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				parts := strings.Split(line, ",")
				frames = append(frames, strings.TrimSpace(parts[len(parts)-1]))
			}
		}
		if len(frames) != 24 {
			t.Fatalf("decoded %d frames, want 24", len(frames))
		}
		return strings.Join(frames, ",")
	}
	if a, b := decode(source, "2"), decode(window, ""); a != b {
		t.Fatal("copy changed the source window frames")
	}

	// Exercise the production preparer, including source verification, upload,
	// the per-placement binding and the diagnostic count.
	sourceBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := digest.SHA256String(string(sourceBytes))
	sourceServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(sourceBytes)
	}))
	defer sourceServer.Close()
	var stored []byte
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			if len(stored) == 0 || r.URL.Path != "/objects/"+digest.SHA256String(string(stored)) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(stored)))
		case http.MethodPut:
			stored, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer store.Close()
	t.Setenv("RENDERINGGEN_STORE_URL", store.URL)
	t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example")
	t.Setenv("OBJECTSTORE_SIGNING_SECRET", strings.Repeat("s", 32))
	t.Setenv("OBJECTSTORE_TOKEN", "")
	t.Setenv("VELOX_FINAL_JOB_FRAGMENT_CODEC", "policy")
	// No copy lever is set: copying an aligned certified window is the default.
	if !finalJobCopyWindowsEnabled() {
		t.Fatal("certified-window copy must be enabled by default")
	}
	p = newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	p.client = sourceServer.Client()
	request.SourceDurationUS = 1_000_000
	request.TimelineStartFrame = 72
	request.TimelineRevision = 1
	request.TimelineSHA256 = strings.Repeat("a", 64)
	sourceRef := map[string]any{
		"asset_id": "source", "sha256": sourceSHA, "size_bytes": int64(len(sourceBytes)), "source_uri": sourceServer.URL,
		"profile_id": finalJobSegmentProfile, "copy_eligible": true, "first_frame_keyframe": true, "closed_gop": true,
		"duration_us": int64(4_000_000), "frame_count": int64(96), "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
	}
	ref, err := p.PrepareFinalJobVideoSegment(context.Background(), sourceRef, request)
	if err != nil {
		t.Fatalf("prepare copied window: %v", err)
	}
	if p.Totals().copied != 1 || int64Value(ref["timeline_start_frame"]) != 72 || len(stored) == 0 {
		t.Fatalf("copied=%d ref=%v uploaded=%d", p.Totals().copied, ref, len(stored))
	}
	uploaded := filepath.Join(dir, "uploaded.mp4")
	if err := os.WriteFile(uploaded, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	if a, b := decode(source, "2"), decode(uploaded, ""); a != b {
		t.Fatal("published fragment differs from the source window")
	}
	// An off-GOP request cannot use packet copy, so it is re-encoded to the
	// certified delivery profile and records why the copy lane was unavailable.
	request.SourceInUS = 500_000
	request.SegmentID = "segment-off-grid"
	encodedRef, err := p.PrepareFinalJobVideoSegment(context.Background(), sourceRef, request)
	if err != nil {
		t.Fatalf("prepare off-GOP window through certified encoder: %v", err)
	}
	if p.Totals().segments != 2 || p.Totals().copied != 1 || int64Value(encodedRef["timeline_start_frame"]) != 72 {
		t.Fatalf("off-GOP encoded window totals=%+v ref=%v", p.Totals(), encodedRef)
	}
	// Certificates are derived: with every declaration stripped from the source
	// the aligned window is still copied, because the bytes are conformant.
	request.SourceInUS = 0
	request.SegmentID = "segment-undeclared"
	undeclared := map[string]any{
		"asset_id": "source", "sha256": sourceSHA, "size_bytes": int64(len(sourceBytes)), "source_uri": sourceServer.URL,
	}
	if _, err := p.PrepareFinalJobVideoSegment(context.Background(), undeclared, request); err != nil {
		t.Fatalf("derived certificate copy: %v", err)
	}
	if p.Totals().segments != 3 || p.Totals().copied != 2 {
		t.Fatalf("undeclared but conformant source did not copy: %+v", p.Totals())
	}
	// The copy lever is an optimization: disabling it selects the certified
	// encoder lane and leaves the reason visible in the receipt.
	t.Setenv("VELOX_FINAL_JOB_COPY_CERTIFIED_WINDOWS", "0")
	if finalJobCopyWindowsEnabled() {
		t.Fatal("explicit disable must win")
	}
	request.SourceInUS = 2_000_000
	request.SegmentID = "segment-copy-disabled"
	if _, err := p.PrepareFinalJobVideoSegment(context.Background(), sourceRef, request); err != nil {
		t.Fatalf("disabled copy lane should use the certified encoder: %v", err)
	}
	if p.Totals().segments != 4 || p.Totals().copied != 2 {
		t.Fatalf("disabled copy lane did not record its encoded window: %+v", p.Totals())
	}
}

func TestFinalJobCopyWindowLever(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"", true}, {"1", true}, {"on", true}, {"0", false}, {"off", false}, {"garbage", false}} {
		t.Setenv("VELOX_FINAL_JOB_COPY_CERTIFIED_WINDOWS", tc.value)
		if got := finalJobCopyWindowsEnabled(); got != tc.want {
			t.Fatalf("lever %q = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestFinalJobFragmentEncoderSettings(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_FRAGMENT_CODEC", "")
	p := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	codec, preset, err := p.fragmentEncoderSettings()
	if err != nil || codec != "libx264" || preset != "veryfast" {
		t.Fatalf("settings = %q, %q, %v; want canonical libx264/veryfast", codec, preset, err)
	}
	p = newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "veryfast", CRF: 23}, "", 1)
	if _, _, err := p.fragmentEncoderSettings(); err == nil {
		t.Fatal("NVENC must be refused: it cannot produce the certified avcC")
	}
}

func TestFinalJobFragmentLockCancellationAndIndependence(t *testing.T) {
	p := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{}, t.TempDir(), 2)
	unlock, err := p.lockFragment(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	otherUnlock, err := p.lockFragment(context.Background(), "different")
	if err != nil {
		t.Fatal(err)
	}
	otherUnlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.lockFragment(ctx, "same"); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting on held lock = %v, want context canceled", err)
	}
	unlock()
	nextUnlock, err := p.lockFragment(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	nextUnlock()
	if len(p.fragmentLocks) != 0 {
		t.Fatalf("fragment locks retained after release: %d", len(p.fragmentLocks))
	}
}

func TestFinalJobFragmentConcurrentWorkReusesCertifiedBytes(t *testing.T) {
	dir := t.TempDir()
	p := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, dir, 2)
	t.Setenv("VELOX_FINAL_JOB_FRAGMENT_CODEC", "policy")
	body := []byte("already certified fragment")
	sha := digest.SHA256String(string(body))
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/objects/"+sha {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	}))
	defer store.Close()
	t.Setenv("RENDERINGGEN_STORE_URL", store.URL)
	t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example")
	t.Setenv("OBJECTSTORE_SIGNING_SECRET", strings.Repeat("s", 32))
	t.Setenv("OBJECTSTORE_TOKEN", "")
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "first", SourceInUS: 0, SourceDurationUS: 2_000_000, FrameCount: 48,
		TimelineRevision: 1, TimelineSHA256: strings.Repeat("a", 64),
	}
	sourceSHA := strings.Repeat("b", 64)
	source := map[string]any{"asset_id": "source", "sha256": sourceSHA, "size_bytes": int64(42), "source_uri": "https://objects.example/source", "role": "stock"}
	key := p.fragmentWorkKey(request, sourceSHA, "libx264")
	unlock, err := p.lockFragment(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		ref map[string]any
		err error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			placement := request
			placement.TimelineStartFrame = int64(i * 48)
			ref, err := p.PrepareFinalJobVideoSegment(context.Background(), source, placement)
			results <- outcome{ref, err}
		}(i)
	}
	// The held work lock stands in for the first encoder. Both requests must
	// wait until its verified bytes and certificate are in the cache.
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.fragmentLocksMu.Lock()
		users := p.fragmentLocks[key].users
		p.fragmentLocksMu.Unlock()
		if users == 3 {
			break
		}
		if time.Now().After(deadline) {
			unlock()
			wg.Wait()
			t.Fatalf("only %d callers reached the work lock", users)
		}
		time.Sleep(time.Millisecond)
	}
	path := filepath.Join(dir, "fixture.mp4")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		unlock()
		t.Fatal(err)
	}
	p.storeFragment(key, path, sha, int64(len(body)), 48, 2_000_000)
	unlock()
	wg.Wait()
	close(results)
	starts := make(map[int64]bool)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.ref["sha256"] != sha {
			t.Fatalf("wrong reused bytes: %v", result.ref["sha256"])
		}
		starts[int64Value(result.ref["timeline_start_frame"])] = true
	}
	if !starts[0] || !starts[48] || p.Totals().cached != 2 {
		t.Fatalf("placements = %v, cache hits = %d", starts, p.Totals().cached)
	}
}

func TestFinalJobFragmentCacheConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	body := []byte("certified bytes")
	sha := digest.SHA256String(string(body))
	path := filepath.Join(dir, "input.mp4")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	p1 := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{}, dir, 1)
	p2 := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{}, dir, 1)
	key := strings.Repeat("c", 64)
	var wg sync.WaitGroup
	for _, p := range []*finalJobSegmentPreparer{p1, p2} {
		wg.Add(1)
		go func(p *finalJobSegmentPreparer) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				p.storeFragment(key, path, sha, int64(len(body)), 48, 2_000_000)
			}
		}(p)
	}
	wg.Wait()
	cached, _, _, ok := p1.cachedFragment(key, 48, 2_000_000)
	if !ok {
		t.Fatal("concurrent writers lost or corrupted the cache entry")
	}
	f, err := os.Open(cached)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || string(got) != string(body) {
		t.Fatalf("cached bytes = %q, error = %v", got, err)
	}
}

func TestFinalJobSegmentPreparerEncodesOneAssemblyReadyFragment(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	output := filepath.Join(dir, "fragment.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-t", "2",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-0000", SourceAssetID: "source-1", SourceSHA256: "sha",
		SourceInUS: 0, SourceDurationUS: 2_000_000, TimelineStartFrame: 0, FrameCount: 48,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, output, request, true, 0); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), output, request.FrameCount); err != nil {
		t.Fatalf("verifyFinalJobFragment: %v", err)
	}
	if got, hashErr := readAVCDecoderConfigurationSHA256(output); hashErr != nil || got != kernelmedia.CanonicalVideoExtradataSHA256 {
		t.Fatalf("encoded fragment avcC = %q (%v), want canonical %s", got, hashErr, kernelmedia.CanonicalVideoExtradataSHA256)
	}
}

// TestFinalJobFragmentVideoFilterPadsOnlyTheMeasuredShortfall pins the padding
// contract without running ffmpeg: a window the source can supply has no tpad at
// all, and a short window clones exactly its deficit. The old filter always
// carried a full-window tpad, which made a frozen tail indistinguishable from a
// rounding artifact.
func TestFinalJobFragmentVideoFilterPadsOnlyTheMeasuredShortfall(t *testing.T) {
	if got := finalJobFragmentVideoFilter(false, 0); got != "" {
		t.Fatalf("complete window filter = %q, want no filter (and therefore no tpad)", got)
	}
	geometry := finalJobFragmentVideoFilter(true, 0)
	if !strings.Contains(geometry, "scale=1920:1080") || strings.Contains(geometry, "tpad") {
		t.Fatalf("geometry-only filter = %q, want geometry without tpad", geometry)
	}
	short := finalJobFragmentVideoFilter(false, 12)
	if !strings.Contains(short, "tpad=stop_mode=clone:stop_duration=0.500000") {
		t.Fatalf("short window filter = %q, want tpad for exactly 12 frames", short)
	}
	both := finalJobFragmentVideoFilter(true, 12)
	if !strings.Contains(both, "fps=24/1") || !strings.Contains(both, "tpad=stop_mode=clone:stop_duration=0.500000") {
		t.Fatalf("geometry+short filter = %q, want both parts", both)
	}
}

// TestFinalJobSegmentPreparerSkipsRebuildingTheDeliveryGeometry proves the fast
// path is still a CERTIFIED fragment: a source that already is 1920x1080/24/
// yuv420p is only cut and capped, without the fps/scale/pad/setsar/format chain
// that would resample the same pixels, and the result passes the same fail
// closed certificate as the normalized path.
func TestFinalJobSegmentPreparerSkipsRebuildingTheDeliveryGeometry(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "delivery-source.mp4")
	output := filepath.Join(dir, "fragment.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-t", "2",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-r", "24", source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make delivery source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	needs, err := preparer.sourceNeedsNormalization(context.Background(), source, strings.Repeat("c", 64))
	if err != nil {
		t.Fatalf("sourceNeedsNormalization: %v", err)
	}
	if needs {
		t.Fatal("sourceNeedsNormalization = true for a 1920x1080/24/yuv420p source")
	}
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-delivery", SourceAssetID: "source-delivery", SourceSHA256: "sha",
		SourceInUS: 500_000, SourceDurationUS: 1_500_000, TimelineStartFrame: 12, FrameCount: 36,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, output, request, needs, 0); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), output, request.FrameCount); err != nil {
		t.Fatalf("verifyFinalJobFragment: %v", err)
	}
}

func TestFinalJobSegmentPreparerPadsShortBoundaryToExactFrameCount(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "short-source.mp4")
	output := filepath.Join(dir, "fragment.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-frames:v", "65",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make short source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-boundary", SourceAssetID: "source-short", SourceSHA256: "sha",
		SourceInUS: 0, SourceDurationUS: 2_750_000, TimelineStartFrame: 65, FrameCount: 66,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, output, request, true, 1); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), output, request.FrameCount); err != nil {
		t.Fatalf("verifyFinalJobFragment: %v", err)
	}
}

// TestVerifyFinalJobFragmentRejectsAnOpenGOP pins the certificate on the packet
// flags: the check must still refuse a fragment whose keyframes are not the
// fixed closed-GOP layout, even though it no longer decodes the frames.
func TestVerifyFinalJobFragmentRejectsAnOpenGOP(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "broken.mp4")
	// 72-frame GOP instead of the certified 48.
	cmd := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-frames:v", "96",
		"-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-profile:v", "high", "-level:v", "4.0", "-pix_fmt", "yuv420p", "-r", "24",
		"-g", "72", "-keyint_min", "72", "-sc_threshold", "0", "-bf", "0", "-flags", "+cgop",
		"-video_track_timescale", "90000", "-map_metadata", "-1", "-movflags", "+faststart", output,
	)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("make open-GOP fixture: %v: %s", runErr, out)
	}
	if err := verifyFinalJobFragment(context.Background(), output, 96); err == nil {
		t.Fatal("verifyFinalJobFragment = nil error, want the open GOP refused")
	}
}

// TestDerivePlacementCopiesTheCertifiedBitstream proves the duplicate-placement
// path is a packet copy and not a second render: the derived object certifies
// the same 48-frame assembly-ready stream, the coded video packets are
// byte-identical to the fragment it repeats, and only the container differs —
// which is exactly what the plan requires of a second placement (distinct bytes,
// one canonical identity each).
func TestDerivePlacementCopiesTheCertifiedBitstream(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	fragment := filepath.Join(dir, "fragment.mp4")
	derived := filepath.Join(dir, "placement.mp4")
	create := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-t", "2",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "48", "-sc_threshold", "0",
		source,
	)
	if out, runErr := create.CombinedOutput(); runErr != nil {
		t.Fatalf("make source fixture: %v: %s", runErr, out)
	}
	preparer := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-0000", SourceAssetID: "source-1", SourceSHA256: "sha",
		SourceInUS: 0, SourceDurationUS: 2_000_000, TimelineStartFrame: 0, FrameCount: 48,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := preparer.encodeFragment(context.Background(), source, fragment, request, false, 0); err != nil {
		t.Fatalf("encodeFragment: %v", err)
	}
	if err := preparer.remuxTaggedFragment(context.Background(), fragment, derived, "segment-0001"); err != nil {
		t.Fatalf("remuxTaggedFragment: %v", err)
	}
	if err := verifyFinalJobFragment(context.Background(), derived, request.FrameCount); err != nil {
		t.Fatalf("derived placement must certify the same assembly-ready stream: %v", err)
	}
	originalSHA, _, err := digest.SHA256File(fragment)
	if err != nil {
		t.Fatalf("hash fragment: %v", err)
	}
	derivedSHA, _, err := digest.SHA256File(derived)
	if err != nil {
		t.Fatalf("hash derived placement: %v", err)
	}
	if strings.EqualFold(originalSHA, derivedSHA) {
		t.Fatal("derived placement certifies the same bytes as its source, which the plan refuses")
	}
	// The coded video packets must be untouched: extract both elementary
	// streams by copy and compare them. A re-encode would change them.
	extract := func(from, to string) []byte {
		cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", from, "-map", "0:v:0", "-c", "copy", "-f", "h264", to)
		if out, runErr := cmd.CombinedOutput(); runErr != nil {
			t.Fatalf("extract elementary stream from %s: %v: %s", from, runErr, out)
		}
		data, readErr := os.ReadFile(to)
		if readErr != nil {
			t.Fatalf("read elementary stream %s: %v", to, readErr)
		}
		return data
	}
	original := extract(fragment, filepath.Join(dir, "original.h264"))
	copied := extract(derived, filepath.Join(dir, "derived.h264"))
	if digest.SHA256String(string(original)) != digest.SHA256String(string(copied)) {
		t.Fatal("the derived placement carries different coded pictures, want the certified bitstream copied verbatim")
	}
	// The tag is what makes the bytes distinct, and it must travel in the
	// container the assembler probes.
	probe := exec.Command(ffmpeg, "-hide_banner", "-i", derived, "-f", "ffmetadata", "-")
	metadata, probeErr := probe.Output()
	if probeErr != nil {
		t.Fatalf("read derived metadata: %v", probeErr)
	}
	if !strings.Contains(string(metadata), "chronon_placement=segment-0001") {
		t.Fatalf("derived container lost its placement tag: %s", metadata)
	}
}

func TestFinalJobFragmentCacheReusesTheSameWorkOnly(t *testing.T) {
	dir := t.TempDir()
	policy := mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}
	preparer := newFinalJobSegmentPreparer("", policy, dir, 1)
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-0003", SourceAssetID: "source-1", SourceSHA256: "sha-source",
		SourceInUS: 1_500_000, SourceDurationUS: 3_000_000, TimelineStartFrame: 36, FrameCount: 72,
		TimelineRevision: 1, TimelineSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	sourceSHA := strings.Repeat("a", 64)
	key := preparer.fragmentCacheKey(request, sourceSHA, "libx264")
	if _, _, _, ok := preparer.cachedFragment(key, request.FrameCount, 3_000_000); ok {
		t.Fatal("cache hit on an empty cache")
	}

	path := filepath.Join(dir, "fragment.mp4")
	body := []byte("certified fragment bytes")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	sha := digest.SHA256String(string(body))
	preparer.storeFragment(key, path, sha, int64(len(body)), request.FrameCount, 3_000_000)
	cachedPath, cachedSHA, cachedSize, ok := preparer.cachedFragment(key, request.FrameCount, 3_000_000)
	if !ok || cachedSHA != sha || cachedSize != int64(len(body)) || cachedPath == "" {
		t.Fatalf("cache hit = %q %q %d %v, want the stored fragment back", cachedPath, cachedSHA, cachedSize, ok)
	}

	// The key is the WORK identity: the same window of the same bytes is the
	// same fragment even when it lands at another timeline position or in
	// another revision — that is what makes a re-run cheap.
	moved := request
	moved.SegmentID = "segment-0009"
	moved.TimelineStartFrame = 240
	moved.TimelineRevision = 7
	moved.TimelineSHA256 = strings.Repeat("f", 64)
	if movedKey := preparer.fragmentCacheKey(moved, sourceSHA, "libx264"); movedKey != key {
		t.Fatalf("cache key moved with the timeline (%s != %s), want work identity only", movedKey, key)
	}
	// A different window, a different source or a different encoder policy is a
	// different fragment.
	other := request
	other.SourceInUS = 2_000_000
	if otherKey := preparer.fragmentCacheKey(other, sourceSHA, "libx264"); otherKey == key {
		t.Fatal("cache key ignored the source in-point")
	}
	otherPolicy := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{Codec: "libx264", Preset: "medium", CRF: 20}, dir, 1)
	if otherKey := otherPolicy.fragmentCacheKey(request, sourceSHA, "libx264"); otherKey == key {
		t.Fatal("cache key ignored the encoder policy")
	}
	if gpuKey := preparer.fragmentCacheKey(request, sourceSHA, "h264_nvenc"); gpuKey == key {
		t.Fatal("cache key ignored the effective encoder codec")
	}
	// Wrong frame count is a miss, not a guess.
	if _, _, _, ok := preparer.cachedFragment(key, request.FrameCount+1, 3_000_000); ok {
		t.Fatal("cache hit with the wrong frame count")
	}
	// Corrupted cache bytes are a miss: the cache can never certify itself.
	if err := os.WriteFile(cachedPath, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper cached fragment: %v", err)
	}
	if tamperedPath, _, _, ok := preparer.cachedFragment(key, request.FrameCount, 3_000_000); ok {
		t.Fatalf("cache hit on tampered bytes (%q), want a miss", tamperedPath)
	}
}

// TestFinalJobFragmentCertifiesTheProfileTheAssemblerBinds pins the profile the
// prepared fragment declares. It is NOT the video-only artifact profile: r90
// sent fragments certified VELOX_OVERLAY_VIDEO_ONLY_V1 and the remote assemble
// gate rejected the plan (`segment "segment-0000" manifest binding is not
// exact`) while the bytes were exact (86/86 frame counts, durations, zero start,
// 1920x1080 yuv420p High 4.0). The fragment certificate therefore stays on the
// profile that gate binds against, and the video-only profile keeps describing
// the render artifact (plan output_profile_id).
func TestFinalJobFragmentCertifiesTheProfileTheAssemblerBinds(t *testing.T) {
	request := scriptgen.FinalJobVideoSegmentRequest{
		FrameCount: 48, TimelineRevision: 1, TimelineSHA256: strings.Repeat("a", 64),
	}
	ref := finalJobPreparedRef(map[string]any{}, strings.Repeat("b", 64), 1024, 2_000_000, request)
	if got := ref["profile_id"]; got != kernelmedia.AssemblyMediaContractID {
		t.Fatalf("profile_id = %v, want %s", got, kernelmedia.AssemblyMediaContractID)
	}
	if ref["profile_id"] == kernelmedia.OverlayVideoOnlyProfileID {
		t.Fatal("the fragment certificate must not declare the artifact profile the assemble gate rejects")
	}
}

func TestFinalJobGeometryIsReadFromTheCensusWhenItRan(t *testing.T) {
	p := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{}, "", 1)
	sha := strings.Repeat("a", 64)
	p.certificates = map[string]finalJobSourceCensus{
		sha: {frames: 96, certified: true, needsNormalization: true},
	}
	absent := filepath.Join(t.TempDir(), "absent.mp4")
	needs, err := p.sourceNeedsNormalization(context.Background(), absent, sha)
	if err != nil {
		t.Fatalf("sourceNeedsNormalization: %v", err)
	}
	if !needs {
		t.Fatal("census geometry was not reused")
	}
	p.certificates[sha] = finalJobSourceCensus{frames: 96, certified: true}
	needs, err = p.sourceNeedsNormalization(context.Background(), absent, sha)
	if err != nil {
		t.Fatalf("sourceNeedsNormalization: %v", err)
	}
	if needs {
		t.Fatal("a census that matches the delivery geometry asked for a rebuild")
	}
}

// verifyFinalJobFragment is the TEST-side certificate. Production no longer
// re-probes a fragment it just produced: a copied window inherits the source
// census, an encoded one is pinned by these tests, and the boundary is proven
// by the request. These tests still assert that the produced bytes honour the
// contract, so removing the runtime probe cannot hide a regression.
func verifyFinalJobFragment(ctx context.Context, path string, frameCount int64) error {
	probe, err := probeFinalJobFragment(ctx, path)
	if err != nil {
		return err
	}
	return probe.certify(frameCount)
}

// TestFinalJobSegmentWindowsStayInTheirHandoff pins the scoping rule: two
// handoffs share one preparer, and a receipt may only report the counters and
// windows its own scope produced. It also pins the copy/encode separation the
// aggregate field cannot make (encode_ms sums both lanes) and the deterministic
// order.
func TestFinalJobSegmentWindowsStayInTheirHandoff(t *testing.T) {
	p := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{}, "", 1)
	scopeA := p.NewHandoffScope()
	scopeB := p.NewHandoffScope()
	ctxA := withFinalJobHandoffScope(context.Background(), scopeA)
	ctxB := withFinalJobHandoffScope(context.Background(), scopeB)

	// Scalars, not just windows: a receipt that reports the difference of two
	// process-wide snapshots would let handoff B's work appear in A's totals.
	p.record(ctxA, func(s *finalJobSegmentStats) { s.segments++; s.copied++; s.encodeMS += 320; s.bytes += 8436 })
	p.recordWindow(ctxA, finalJobSegmentWindow{segmentID: "segment-0002", path: finalJobWindowCopy, frames: 72, copyMS: 320, bytes: 8436})
	p.record(ctxB, func(s *finalJobSegmentStats) { s.segments++; s.encodeMS += 1210; s.bytes += 32020; s.paddedFrames += 2 })
	p.recordWindow(ctxB, finalJobSegmentWindow{segmentID: "segment-0001", path: finalJobWindowEncode, frames: 48, encodeMS: 1210, bytes: 32020, paddedFrames: 2})
	p.record(ctxA, func(s *finalJobSegmentStats) { s.segments++; s.encodeMS += 1500; s.verifyMS += 40; s.bytes += 41000 })
	p.recordWindow(ctxA, finalJobSegmentWindow{segmentID: "segment-0001", path: finalJobWindowEncode, frames: 96, encodeMS: 1500, verifyMS: 40, bytes: 41000})

	prepA := p.Timing(scopeA)
	if prepA.Segments != 2 || prepA.Copied != 1 || prepA.EncodeMS != 1820 || prepA.VerifyMS != 40 || prepA.Bytes != 49436 {
		t.Fatalf("handoff A totals = %+v, want only its own two fragments", prepA)
	}
	if len(prepA.Windows) != 2 {
		t.Fatalf("handoff A windows = %d, want 2 (handoff B must not leak in): %+v", len(prepA.Windows), prepA.Windows)
	}
	if prepA.Windows[0].SegmentID != "segment-0001" || prepA.Windows[1].SegmentID != "segment-0002" {
		t.Fatalf("windows are not sorted by segment identity: %+v", prepA.Windows)
	}
	copied := prepA.Windows[1]
	if copied.Path != finalJobWindowCopy || copied.CopyMS != 320 || copied.EncodeMS != 0 {
		t.Fatalf("copied window must report copy time and no encode time: %+v", copied)
	}
	encoded := prepA.Windows[0]
	if encoded.Path != finalJobWindowEncode || encoded.EncodeMS != 1500 || encoded.CopyMS != 0 {
		t.Fatalf("encoded window must report encode time and no copy time: %+v", encoded)
	}

	prepB := p.Timing(scopeB)
	if len(prepB.Windows) != 1 || prepB.Windows[0].SegmentID != "segment-0001" || prepB.Windows[0].EncodeMS != 1210 {
		t.Fatalf("handoff B windows = %+v, want only its own encoded window", prepB.Windows)
	}
	if prepB.Segments != 1 || prepB.Copied != 0 || prepB.EncodeMS != 1210 || prepB.Bytes != 32020 || prepB.PaddedFrames != 2 {
		t.Fatalf("handoff B totals = %+v, want only its own fragment with its padding", prepB)
	}
	if len(prepB.Windows) != 1 || prepB.Windows[0].PaddedFrames != 2 {
		t.Fatalf("handoff B windows = %+v, want the measured padding", prepB.Windows)
	}
	// The aggregate still sees everything; only the receipts are scoped.
	if total := p.Totals(); total.segments != 3 || total.copied != 1 || total.encodeMS != 3030 {
		t.Fatalf("process aggregate = %+v, want 3 fragments", total)
	}
}

// TestFinalJobCopyFallbackIsReported pins the relaxed no-reencode contract: a
// window that was eligible for a packet copy and reached the encoder anyway is
// counted and named. Before this, the run looked uniformly successful and the
// only trace of the re-encode was a larger encode_ms, which sums both lanes.
func TestFinalJobCopyFallbackIsReported(t *testing.T) {
	p := newFinalJobSegmentPreparer("", mediaexec.EncoderPolicy{}, "", 1)
	scope := p.NewHandoffScope()
	ctx := withFinalJobHandoffScope(context.Background(), scope)
	reason := finalJobCopyFailureReason(fmt.Errorf("copied fragment avcC is not canonical"))
	p.record(ctx, func(s *finalJobSegmentStats) { s.segments++; s.encodeFallbacks++; s.encodeMS += 900 })
	p.recordWindow(ctx, finalJobSegmentWindow{
		segmentID: "segment-0003", path: finalJobWindowEncode, frames: 48,
		encodeMS: 900, bytes: 32020, copyFailure: reason,
	})

	timing := p.Timing(scope)
	if timing.Copied != 0 {
		t.Fatalf("a fallback window must not be reported as copied: %+v", timing)
	}
	if timing.EncodeFallbacks != 1 {
		t.Fatalf("encode_fallbacks = %d, want 1: an eligible window that was encoded is a relaxed contract", timing.EncodeFallbacks)
	}
	if len(timing.Windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(timing.Windows))
	}
	window := timing.Windows[0]
	if window.Path != finalJobWindowEncode || window.CopyFailure != reason {
		t.Fatalf("window = %+v, want path=encode and the measured copy failure", window)
	}
	if got := finalJobCopyFailureReason(nil); got == "" {
		t.Fatal("a copy that produced no certified output must still carry a reason")
	}
}

// TestFinalJobFragmentGeometryComesFromTheContract pins the single source of
// truth: the encoder filters, the source census and the reuse gate must all
// describe the SAME delivery geometry the kernel media contract declares. A
// second copy of 1920x1080/yuv420p/24 is how a certified fragment and the
// certificate that describes it drift apart.
func TestFinalJobFragmentGeometryComesFromTheContract(t *testing.T) {
	contract := kernelmedia.DefaultAssemblyMediaContract()
	if finalJobSegmentFPS != int64(contract.FPS.Num) || contract.FPS.Den != 1 {
		t.Fatalf("fragment fps = %d, contract = %d/%d: the frame math assumes the contract's integer rate",
			finalJobSegmentFPS, contract.FPS.Num, contract.FPS.Den)
	}
	if finalJobSegmentGOP != int64(contract.KeyframeInterval) {
		t.Fatalf("fragment GOP = %d, contract = %d", finalJobSegmentGOP, contract.KeyframeInterval)
	}
	if finalJobSegmentProfile != contract.ID {
		t.Fatalf("fragment profile = %q, contract = %q", finalJobSegmentProfile, contract.ID)
	}
	avgFPS := fmt.Sprintf("%d/%d", contract.FPS.Num, contract.FPS.Den)
	if !finalJobDeliveryMatches(contract.Width, contract.Height, avgFPS, contract.PixelFormat, "1:1") {
		t.Fatal("the contract's own geometry is reported as non-conformant")
	}
	for _, tc := range []struct {
		width, height   int
		fps, pixel, sar string
	}{
		{contract.Width - 2, contract.Height, avgFPS, contract.PixelFormat, "1:1"},
		{contract.Width, contract.Height - 2, avgFPS, contract.PixelFormat, "1:1"},
		{contract.Width, contract.Height, "30/1", contract.PixelFormat, "1:1"},
		{contract.Width, contract.Height, avgFPS, "yuv422p", "1:1"},
		{contract.Width, contract.Height, avgFPS, contract.PixelFormat, "20630:20633"},
	} {
		if finalJobDeliveryMatches(tc.width, tc.height, tc.fps, tc.pixel, tc.sar) {
			t.Fatalf("non-conformant geometry accepted: %dx%d %s %s sar=%s", tc.width, tc.height, tc.fps, tc.pixel, tc.sar)
		}
	}
	if got := finalJobDeliveryLevelCode(); got != 40 {
		t.Fatalf("contract level %q maps to %d, want 40", contract.VideoLevel, got)
	}
}

// TestFinalJobCopiedWindowTimingReceipt runs the real preparer: an aligned
// certified window lands in the receipt as a copy with a zero encode time,
// while an off-grid window uses the certified encoder and records the reason.
// This is the measurement the copy-window criterion was missing: the aggregate
// encode_ms cannot distinguish packet-copy from encode work.
func TestFinalJobCopiedWindowTimingReceipt(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-frames:v", "96", "-an",
		"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-level:v", "4.0",
		"-pix_fmt", "yuv420p", "-refs", "1",
		"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv",
		"-bf", "0", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-force_key_frames", "expr:gte(t,n_forced*2)", "-video_track_timescale", "12288",
		source}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("encode source: %v: %s", err, out)
	}
	sourceBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := digest.SHA256String(string(sourceBytes))
	currentSourceBytes := sourceBytes
	sourceServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(currentSourceBytes)
	}))
	defer sourceServer.Close()
	var storedObject string
	var storedSize int
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			if storedObject == "" || r.URL.Path != "/objects/"+storedObject {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(storedSize))
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			storedObject, storedSize = digest.SHA256String(string(body)), len(body)
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer store.Close()
	t.Setenv("RENDERINGGEN_STORE_URL", store.URL)
	t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example")
	t.Setenv("OBJECTSTORE_SIGNING_SECRET", strings.Repeat("s", 32))
	t.Setenv("OBJECTSTORE_TOKEN", "")
	t.Setenv("VELOX_FINAL_JOB_FRAGMENT_CODEC", "policy")
	t.Setenv("VELOX_FINAL_JOB_COPY_CERTIFIED_WINDOWS", "1")

	p := newFinalJobSegmentPreparer(ffmpeg, mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23}, "", 1)
	p.client = sourceServer.Client()
	sourceRef := map[string]any{
		"asset_id": "source", "sha256": sourceSHA, "size_bytes": int64(len(sourceBytes)), "source_uri": sourceServer.URL,
		"profile_id": finalJobSegmentProfile, "copy_eligible": true, "first_frame_keyframe": true, "closed_gop": true,
		"duration_us": int64(4_000_000), "frame_count": int64(96), "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
	}
	request := scriptgen.FinalJobVideoSegmentRequest{
		SegmentID: "segment-0007", SourceAssetID: "source", SourceSHA256: sourceSHA,
		SourceInUS: 0, SourceDurationUS: 1_000_000, FrameCount: 24,
		TimelineStartFrame: 0, TimelineRevision: 1, TimelineSHA256: strings.Repeat("a", 64),
	}
	scope := p.NewHandoffScope()
	ctx := withFinalJobHandoffScope(context.Background(), scope)

	if _, err := p.PrepareFinalJobVideoSegment(ctx, sourceRef, request); err != nil {
		t.Fatalf("prepare copied window: %v", err)
	}
	// An off-GOP window is not copy-eligible, so prepare it with the canonical
	// encoder and retain the reason in the receipt.
	offGrid := request
	offGrid.SourceInUS = 500_000
	offGrid.TimelineStartFrame = 24
	if _, err := p.PrepareFinalJobVideoSegment(ctx, sourceRef, offGrid); err != nil {
		t.Fatalf("prepare off-GOP encoded window: %v", err)
	}

	prep := p.Timing(scope)
	if len(prep.Windows) != 2 {
		t.Fatalf("windows = %d, want copied and encoded paths: %+v", len(prep.Windows), prep.Windows)
	}
	byPath := map[string]scriptgen.RemoteFinalJobSegmentPrepWindow{}
	for _, window := range prep.Windows {
		byPath[window.Path] = window
	}
	copied, ok := byPath[finalJobWindowCopy]
	if !ok {
		t.Fatalf("no copied window in the receipt: %+v", prep.Windows)
	}
	if copied.EncodeMS != 0 {
		t.Fatalf("copied window reported %d ms of encode: the lane must not reach the encoder: %+v", copied.EncodeMS, copied)
	}
	if copied.CopyMS <= 0 || copied.Bytes <= 0 || copied.SegmentID != "segment-0007" {
		t.Fatalf("copied window is not attributed: %+v", copied)
	}
	encoded, ok := byPath[finalJobWindowEncode]
	if !ok {
		t.Fatalf("no GOP-misaligned encode in the receipt: %+v", prep.Windows)
	}
	if encoded.EncodeMS <= 0 || !strings.Contains(encoded.CopyFailure, "GOP boundary") || encoded.SegmentID != "segment-0007" {
		t.Fatalf("encoded off-GOP window is not fully attributed: %+v", encoded)
	}

	// Stock assets can be GOP-aligned yet fail the packet-copy census (for
	// example, B-frames). They must take the same certified encode lane rather
	// than fail an otherwise valid final job.
	openSource := filepath.Join(dir, "open-source.mp4")
	openArgs := append([]string(nil), args[:len(args)-1]...)
	for i := range openArgs {
		if openArgs[i] == "-bf" {
			openArgs[i+1] = "2"
		}
	}
	openArgs = append(openArgs, openSource)
	if out, err := exec.Command(ffmpeg, openArgs...).CombinedOutput(); err != nil {
		t.Fatalf("make uncertified stock source: %v: %s", err, out)
	}
	currentSourceBytes, err = os.ReadFile(openSource)
	if err != nil {
		t.Fatal(err)
	}
	openSHA := digest.SHA256String(string(currentSourceBytes))
	openRef := make(map[string]any, len(sourceRef))
	for key, value := range sourceRef {
		openRef[key] = value
	}
	openRef["asset_id"], openRef["sha256"], openRef["size_bytes"] = "stock-open-gop", openSHA, int64(len(currentSourceBytes))
	openRequest := request
	openRequest.SegmentID = "segment-uncertified-stock"
	openRequest.SourceInUS = 0
	openRequest.TimelineStartFrame = 48
	if _, err := p.PrepareFinalJobVideoSegment(ctx, openRef, openRequest); err != nil {
		t.Fatalf("prepare uncertified stock source through canonical encoder: %v", err)
	}
	prep = p.Timing(scope)
	if len(prep.Windows) != 3 {
		t.Fatalf("windows = %d, want copy, off-grid encode, and uncertified-source encode: %+v", len(prep.Windows), prep.Windows)
	}
	var uncertified *scriptgen.RemoteFinalJobSegmentPrepWindow
	for i := range prep.Windows {
		if prep.Windows[i].SegmentID == "segment-uncertified-stock" {
			uncertified = &prep.Windows[i]
		}
	}
	if uncertified == nil || uncertified.Path != finalJobWindowEncode || uncertified.EncodeMS <= 0 || !strings.Contains(uncertified.CopyFailure, "not certified") {
		t.Fatalf("uncertified stock encode is not attributed: %+v", prep.Windows)
	}
}
