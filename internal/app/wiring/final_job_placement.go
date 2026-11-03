package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Placement identity: a second placement of already-certified bytes is a remux, not a
// render, and it carries its own container identity.
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
			return p.finishFragment(ctx, request, ref, role, cachedSHA, cachedSize, durationUS,
				finalJobSegmentWindow{
					segmentID: request.SegmentID, sourceSHA: reuseSHA, path: finalJobWindowDerived,
					frames: request.FrameCount, bytes: cachedSize,
				},
				func(s *finalJobSegmentStats) { s.segments++; s.derived++ }), nil
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
	if got, hashErr := readAVCDecoderConfigurationSHA256(outPath); hashErr != nil {
		return nil, fmt.Errorf("certify derived placement avcC: %w", hashErr)
	} else if got != finalJobDelivery.VideoExtradataSHA256 {
		return nil, fmt.Errorf("derived placement avcC %s != canonical %s", got, finalJobDelivery.VideoExtradataSHA256)
	}
	remuxed := time.Since(encodeStarted)
	// The remux copies certified packets: the container is new, the frames are
	// not. A probe here would re-certify the source it was copied from.
	sha, size, err := p.verifyLocalFile(outPath)
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
	return p.finishFragment(ctx, request, ref, role, sha, size, durationUS,
		finalJobSegmentWindow{
			segmentID: request.SegmentID, sourceSHA: reuseSHA, path: finalJobWindowDerived,
			frames: request.FrameCount, downloadMS: downloaded.Milliseconds(),
			// A remux is not an encode of new pictures, and saying so here is
			// the point of the lane: new bytes, same frames.
			encodeMS: remuxed.Milliseconds(), publishMS: published.Milliseconds(), bytes: size,
		},
		func(s *finalJobSegmentStats) {
			s.segments++
			s.derived++
			s.downloadMS += downloaded.Milliseconds()
			// The remux is reported as encode time so a run's receipt keeps one
			// meaning per field: "time spent producing fragment bytes".
			s.encodeMS += remuxed.Milliseconds()
			s.publishMS += published.Milliseconds()
			s.bytes += size
		}), nil
}

func finalJobSourceIsExactPreparedFragment(source map[string]any, request scriptgen.FinalJobVideoSegmentRequest, durationUS int64) bool {
	return strings.EqualFold(strings.TrimSpace(stringValue(source["profile_id"])), finalJobSegmentProfile) &&
		strings.EqualFold(strings.TrimSpace(stringValue(source["video_extradata_sha256"])), finalJobDelivery.VideoExtradataSHA256) &&
		boolFromAny(source["copy_eligible"]) && boolFromAny(source["first_frame_keyframe"]) && boolFromAny(source["closed_gop"]) &&
		request.SourceInUS == 0 && int64Value(source["source_in_us"]) == 0 && int64Value(source["duration_us"]) == durationUS &&
		int64Value(source["frame_count"]) == request.FrameCount &&
		int64Value(source["width"]) == int64(finalJobDelivery.Width) && int64Value(source["height"]) == int64(finalJobDelivery.Height) &&
		int64Value(source["fps_num"]) == finalJobSegmentFPS && int64Value(source["fps_den"]) == int64(finalJobDelivery.FPS.Den)
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
	ref["video_extradata_sha256"] = finalJobDelivery.VideoExtradataSHA256
	ref["first_frame_keyframe"], ref["closed_gop"] = true, true
	ref["width"], ref["height"] = finalJobDelivery.Width, finalJobDelivery.Height
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
	return timeBase == fmt.Sprintf("1/%d", finalJobDelivery.VideoTimeBase.Den) || timeBase == fmt.Sprintf("1/%d", finalJobSegmentFPS*512)
}

// --- fragment probe: the facts the copy and reuse gates read ---

// Fragment probe: read the facts the reuse gate and the copy gate need off the
// container itself, without trusting what its metadata claims.
// frames is the frame count the bytes prove: the certified encoder contract (no
// B-frames) makes packet order equal display order, so the packet count IS the
// frame count.
func (p finalJobFragmentProbe) frames() int64 { return p.packets }

// needsDeliveryGeometry reports whether the encode still has to rebuild the
// delivery geometry (1920x1080, 24/1, yuv420p, square pixels) or only has to cut the window.
func (p finalJobFragmentProbe) needsDeliveryGeometry() bool {
	return !finalJobDeliveryMatches(p.width, p.height, p.avgFPS, p.pixelFmt, p.sampleAspectRatio)
}

func (p finalJobFragmentProbe) certify(frameCount int64) error {
	if !p.streamOK {
		return fmt.Errorf("prepared fragment stream does not match %s", finalJobSegmentProfile)
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

type finalJobFragmentProbe struct {
	streamOK          bool
	startAtZero       bool
	declaredFrames    string
	declaredCount     int64
	declaredKnown     bool
	durationUS        int64
	keyframeAt        map[int64]bool
	packets           int64
	width             int
	height            int
	avgFPS            string
	pixelFmt          string
	sampleAspectRatio string
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
			SAR       string `json:"sample_aspect_ratio"`
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
	probe.width, probe.height = s.Width, s.Height
	probe.avgFPS, probe.pixelFmt = s.AvgFPS, s.PixelFmt
	probe.sampleAspectRatio = s.SAR
	probe.streamOK = strings.EqualFold(s.CodecName, finalJobDelivery.VideoCodec) &&
		strings.EqualFold(s.Profile, finalJobDelivery.VideoProfile) && s.Level == finalJobDeliveryLevelCode() &&
		finalJobDeliveryMatches(s.Width, s.Height, s.AvgFPS, s.PixelFmt, s.SAR) && s.BFrames == 0 &&
		finalJobSourceTimeBaseCertified(s.TimeBase)
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
