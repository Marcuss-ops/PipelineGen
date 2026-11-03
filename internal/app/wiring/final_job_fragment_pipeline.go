package wiring

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// Fragment pipeline: what one handoff measured while producing fragments, and
// the ffmpeg commands that cut a certified window or encode one.

// Handoff-scoped attribution: the statistics a receipt reports and the scope that
// keeps two concurrent handoffs from reading each other's work.
// NewHandoffScope allocates the identity of one handoff. Everything recorded
// under it is readable through Timing(scope) and belongs to that handoff alone.
func (p *finalJobSegmentPreparer) NewHandoffScope() uint64 {
	if p == nil {
		return 0
	}
	return p.scopeSeq.Add(1)
}

// Timing returns the receipt block of ONE handoff: its own counters and its own
// windows. It is deliberately not a difference of process-wide snapshots, so a
// concurrent handoff finishing between two reads cannot leak into this receipt.
func (p *finalJobSegmentPreparer) Timing(scope uint64) scriptgen.RemoteFinalJobSegmentPrepTiming {
	if p == nil || scope == 0 {
		return scriptgen.RemoteFinalJobSegmentPrepTiming{}
	}
	p.mu.Lock()
	stats, ok := p.scoped[scope]
	p.mu.Unlock()
	if !ok {
		return scriptgen.RemoteFinalJobSegmentPrepTiming{}
	}
	return stats.timing()
}

func (p *finalJobSegmentPreparer) recordWindow(ctx context.Context, window finalJobSegmentWindow) {
	if p == nil {
		return
	}
	window.scope = finalJobHandoffScope(ctx)
	p.record(ctx, func(s *finalJobSegmentStats) {
		s.windows = append(s.windows, window)
		if over := len(s.windows) - finalJobMaxReportedWindows; over > 0 {
			s.windows = append([]finalJobSegmentWindow(nil), s.windows[over:]...)
		}
	})
}

// timing projects one accumulator onto the receipt block. The windows were
// recorded under this scope, so there is nothing to filter out here.
func (s finalJobSegmentStats) timing() scriptgen.RemoteFinalJobSegmentPrepTiming {
	timing := scriptgen.RemoteFinalJobSegmentPrepTiming{
		Segments:        s.segments,
		Reused:          s.reused,
		Cached:          s.cached,
		Derived:         s.derived,
		Copied:          s.copied,
		EncodeFallbacks: s.encodeFallbacks,
		DownloadMS:      s.downloadMS,
		EncodeMS:        s.encodeMS,
		VerifyMS:        s.verifyMS,
		PublishMS:       s.publishMS,
		Bytes:           s.bytes,
		PaddedFrames:    s.paddedFrames,
	}
	for _, window := range s.windows {
		timing.Windows = append(timing.Windows, scriptgen.RemoteFinalJobSegmentPrepWindow{
			SegmentID:    window.segmentID,
			SourceSHA256: window.sourceSHA,
			Normalized:   window.normalized,
			Path:         window.path,
			Frames:       window.frames,
			DownloadMS:   window.downloadMS,
			CopyMS:       window.copyMS,
			EncodeMS:     window.encodeMS,
			VerifyMS:     window.verifyMS,
			PublishMS:    window.publishMS,
			Bytes:        window.bytes,
			PaddedFrames: window.paddedFrames,
			CopyFailure:  window.copyFailure,
		})
	}
	// Deterministic receipt: four lanes finish in whatever order the CPU
	// decides, but the same work must serialize to the same payload.
	sort.SliceStable(timing.Windows, func(i, j int) bool {
		if timing.Windows[i].SegmentID != timing.Windows[j].SegmentID {
			return timing.Windows[i].SegmentID < timing.Windows[j].SegmentID
		}
		return timing.Windows[i].Path < timing.Windows[j].Path
	})
	return timing
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

// record applies one update to the process aggregate AND to the handoff that
// produced it. The aggregate is the log surface; the scoped entry is what a
// receipt reads, so the two can never disagree about whose work this was.
func (p *finalJobSegmentPreparer) record(ctx context.Context, update func(*finalJobSegmentStats)) {
	scope := finalJobHandoffScope(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	update(&p.stats)
	if scope == 0 {
		return
	}
	if p.scoped == nil {
		p.scoped = make(map[uint64]finalJobSegmentStats)
	}
	entry := p.scoped[scope]
	update(&entry)
	p.scoped[scope] = entry
	p.evictScopesLocked(scope)
}

// evictScopesLocked drops the oldest handoff that is not the one being written,
// so the map stays bounded without a slow handoff losing its own receipt.
func (p *finalJobSegmentPreparer) evictScopesLocked(current uint64) {
	if len(p.scoped) <= finalJobMaxScopedHandoffs {
		return
	}
	var victim uint64
	found := false
	for scope := range p.scoped {
		if scope == current {
			continue
		}
		if !found || scope < victim {
			victim, found = scope, true
		}
	}
	if found {
		delete(p.scoped, victim)
	}
}

const finalJobMaxScopedHandoffs = 64

// finalJobSegmentStats is one handoff's accumulator behind the receipt's
// segment_prep block (and, as the process aggregate, the log surface): every
// field is only ever added to by work recorded under that scope, so two
// concurrent runs can never report each other's fragments as their own.
type finalJobSegmentStats struct {
	segments int
	reused   int
	cached   int
	derived  int
	copied   int
	// encodeFallbacks counts GOP-aligned windows with a certified source that
	// were encoded after copy failed or produced incompatible bytes. Unaligned
	// and uncertified windows are planned encoder work, described per window.
	encodeFallbacks int
	downloadMS      int64
	encodeMS        int64
	verifyMS        int64
	publishMS       int64
	bytes           int64
	// paddedFrames counts tail frames cloned to reach a window the source could
	// not supply. It stays zero when every requested frame existed.
	paddedFrames int64
	// windows is the per-fragment attribution behind the aggregate above.
	windows []finalJobSegmentWindow
}

// finalJobSegmentWindow is one fragment's measured cost, tagged with the handoff
// scope that asked for it. The aggregate accumulator cannot answer "was this
// window copied or encoded?": `encode_ms` sums both, because a packet copy is
// reported there to keep one meaning per field. These records keep the two
// apart without changing that meaning.
type finalJobSegmentWindow struct {
	scope     uint64
	segmentID string
	// sourceSHA names the bytes this window was cut from, so a slow window can
	// be tied back to a source instead of only to its own duration. The receipt
	// is the only place that mapping exists once the temp files are gone.
	sourceSHA string
	// normalized records that the encoder rebuilt the delivery geometry for this
	// window (resolution, frame rate, chroma). It distinguishes a slow scale from
	// a slow cut without a second probe.
	normalized   bool
	path         string
	frames       int64
	downloadMS   int64
	copyMS       int64
	encodeMS     int64
	verifyMS     int64
	publishMS    int64
	bytes        int64
	paddedFrames int64
	// copyFailure names why an eligible window was encoded; empty when no copy
	// was attempted or the copy succeeded.
	copyFailure string
}

// finalJobMaxReportedWindows bounds the per-window list a single receipt can
// carry. It is a diagnostic surface: a run with more wins than this keeps the
// most recent ones instead of growing the receipt without limit.
const finalJobMaxReportedWindows = 4096

// finalJobHandoffScopeKey carries the handoff identity to the prepare calls, so
// a window is attributed to the run that asked for it rather than to whichever
// run happened to snapshot the accumulator next.
type finalJobHandoffScopeKey struct{}

func withFinalJobHandoffScope(ctx context.Context, scope uint64) context.Context {
	if scope == 0 || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, finalJobHandoffScopeKey{}, scope)
}

func finalJobHandoffScope(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	scope, _ := ctx.Value(finalJobHandoffScopeKey{}).(uint64)
	return scope
}

// Fragment production: the ffmpeg commands that cut a certified window by packet copy
// or encode one into an assembly-ready fragment.
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
		"-video_track_timescale", strconv.Itoa(finalJobDelivery.VideoTimeBase.Den),
		"-map_metadata", "-1",
		"-metadata", "chronon_placement=" + tag,
		// MP4 only writes a non-standard key when use_metadata_tags is set;
		// without it the tag is silently dropped and the copy would certify the
		// SAME bytes as its source, which the plan refuses.
		"-avoid_negative_ts", "make_zero",
		"-movflags", "+faststart+use_metadata_tags",
		output,
	}
	outputBytes, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("derive duplicate placement by packet copy: %w: %s", err, strings.TrimSpace(string(outputBytes)))
	}
	return nil
}

func (p *finalJobSegmentPreparer) fragmentEncoderSettings() (string, string, error) {
	codec := strings.TrimSpace(p.policy.Codec)
	preset := strings.TrimSpace(p.policy.Preset)
	if !strings.Contains(strings.ToLower(codec), "264") || preset == "" || p.policy.CRF <= 0 {
		return "", "", fmt.Errorf("final-job fragment requires a resolved H.264 encoder policy")
	}
	// One canonical encoder, not a family of "H.264" producers. The certified
	// avcC is libx264's exact SPS/PPS; NVENC emits a different parameter-set
	// family with the same profile/level, which is precisely the defect this
	// contract exists to stop. Fail before any expensive work.
	if !strings.EqualFold(codec, "libx264") {
		return "", "", fmt.Errorf("final-job fragments require the canonical libx264 encoder, got %q", codec)
	}
	if !strings.EqualFold(preset, "veryfast") {
		return "", "", fmt.Errorf("final-job fragments require the certified veryfast preset, got %q", preset)
	}
	return codec, preset, nil
}

// finalJobFragmentVideoFilter is the ONE composition of the encoder's video
// filters. Geometry normalization is optional; tpad is added ONLY for the frames
// the source does not have, so a window fully inside the source is encoded with
// no tail-cloning filter at all. It is pure so the padding contract can be
// pinned without running ffmpeg.
func finalJobFragmentVideoFilter(normalizeGeometry bool, padFrames int64) string {
	filters := make([]string, 0, 2)
	if normalizeGeometry {
		filters = append(filters, fmt.Sprintf("fps=%d/%d,scale=%d:%d:force_original_aspect_ratio=decrease:flags=lanczos,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,format=%s",
			finalJobDelivery.FPS.Num, finalJobDelivery.FPS.Den,
			finalJobDelivery.Width, finalJobDelivery.Height,
			finalJobDelivery.Width, finalJobDelivery.Height,
			finalJobDelivery.PixelFormat))
	}
	if padFrames > 0 {
		filters = append(filters, "tpad=stop_mode=clone:stop_duration="+
			strconv.FormatFloat(float64(padFrames)/float64(finalJobSegmentFPS), 'f', 6, 64))
	}
	return strings.Join(filters, ",")
}

func (p *finalJobSegmentPreparer) encodeFragment(ctx context.Context, source, output string, request scriptgen.FinalJobVideoSegmentRequest, normalizeGeometry bool, padFrames int64) error {
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
	start := float64(request.SourceInUS) / 1_000_000
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-ss", strconv.FormatFloat(start, 'f', 6, 64), "-i", source,
	}
	if videoFilter := finalJobFragmentVideoFilter(normalizeGeometry, padFrames); videoFilter != "" {
		args = append(args, "-vf", videoFilter)
	}
	args = append(args, "-map", "0:v:0", "-an")
	args = append(args,
		// Bound output by the plan's exact frame count. Do not use -t here:
		// timestamp rounding at a short source boundary can cut the last planned
		// frame. tpad, when present, only supplies the MEASURED shortfall (see
		// finalJobFragmentVideoFilter); the cap prevents any longer freeze.
		"-frames:v", strconv.FormatInt(request.FrameCount, 10), "-vsync", "cfr",
		"-c:v", codec, "-preset", preset,
	)
	args = append(args, "-crf", strconv.Itoa(p.policy.CRF))
	args = append(args,
		"-profile:v", finalJobDelivery.VideoProfile, "-level:v", finalJobDelivery.VideoLevel,
		"-pix_fmt", finalJobDelivery.PixelFormat, "-r", strconv.Itoa(finalJobDelivery.FPS.Num),
		// The canonical avcC carries bt709 VUI. Force it from the contract
		// instead of inheriting whatever colour tags a source happens to carry.
		"-color_primaries", finalJobDelivery.ColorPrimaries, "-color_trc", finalJobDelivery.ColorTransfer,
		"-colorspace", finalJobDelivery.ColorSpace, "-color_range", finalJobDelivery.ColorRange,
		"-g", strconv.FormatInt(finalJobSegmentGOP, 10),
	)
	args = append(args,
		"-keyint_min", strconv.FormatInt(finalJobSegmentGOP, 10), "-sc_threshold", "0",
		// ref=1 is part of the certified SPS (max_num_ref_frames). Leaving it
		// to the preset changes the avcC while all nominal facts stay equal.
		"-bf", "0", "-refs", "1", "-flags", "+cgop",
		"-video_track_timescale", strconv.Itoa(finalJobDelivery.VideoTimeBase.Den), "-map_metadata", "-1",
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
		"-video_track_timescale", strconv.Itoa(finalJobDelivery.VideoTimeBase.Den), "-map_metadata", "-1",
		// The certificate is derived, so the zero start it promises is forced
		// by the muxer instead of being re-probed afterwards.
		"-avoid_negative_ts", "make_zero", "-movflags", "+faststart", output}
	out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy certified final-job window: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// finalJobWindowOnGOPGrid reports whether the requested window starts exactly on
// a GOP boundary. Only the geometry is checked here, because whether that
// boundary is a real keyframe is a property of the bytes, not of the request:
// it is answered by the source census immediately before the copy.
func finalJobWindowOnGOPGrid(request scriptgen.FinalJobVideoSegmentRequest) bool {
	return request.SourceInUS >= 0 && request.SourceInUS%2_000_000 == 0 && request.FrameCount > 0
}
