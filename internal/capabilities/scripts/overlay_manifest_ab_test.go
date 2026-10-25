package scriptgeneration

// overlay_manifest_ab_test.go is the P5 A/B harness
// (docs/operations/TICKET-MANIFEST-IDENTITY.md, gate 1): the same plan measured
// through BOTH render projections — the per-item MP4 path and the
// manifest+timeline path.
//
// Two parity levels, deliberately separated:
//
//   - PIPELINE parity: both paths see the same plan and agree on the item
//     order and on every entry's timeline window (the semantic placement the
//     final worker consumes).
//   - MEDIA parity: real ffmpeg-generated fixture clips projected onto ONE
//     timeline artifact through the manifest (track/z-order, in_us/out_us) and
//     compared byte-exactly (lossless round trip) against the per-item clips
//     at LITERAL windows, so a placement bug can never hide behind
//     self-consistent math.
//
// The Chronon-vs-Chronon comparison on the golden plan (the ticket's full
// gate 1) needs live GPU renders and is out of this checkout's reach; this
// harness proves the projection and identity layers those measurements rest
// on.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

const (
	abWidth      = 320
	abHeight     = 180
	abFPS        = 25
	abSampleRate = 24000
)

// abFixtureClip generates one real fixture clip (solid color video + sine
// audio, lossless) with ffmpeg and returns its path.
func abFixtureClip(t *testing.T, dir, name, color string, freqHz int, frames int) string {
	t.Helper()
	path := filepath.Join(dir, name+".mkv")
	seconds := float64(frames) / abFPS
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:s=%dx%d:r=%d:d=%g", color, abWidth, abHeight, abFPS, seconds),
		// 0.25 amplitude keeps sums of overlapping entries well inside int16.
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:sample_rate=%d:duration=%g,volume=0.25", freqHz, abSampleRate, seconds),
		"-map", "0:v", "-map", "1:a",
		"-c:v", "ffv1", "-pix_fmt", "gbrp", "-c:a", "pcm_s16le",
		path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture %s: %v: %s", name, err, out)
	}
	return path
}

// abDecodeRGB decodes a video into packed RGB frames with ffmpeg.
func abDecodeRGB(t *testing.T, path string) [][]byte {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	frameBytes := abWidth * abHeight * 3
	if len(out) == 0 || len(out)%frameBytes != 0 {
		t.Fatalf("decode %s: %d bytes is not a whole number of %d-byte frames", path, len(out), frameBytes)
	}
	frames := make([][]byte, 0, len(out)/frameBytes)
	for offset := 0; offset < len(out); offset += frameBytes {
		frame := make([]byte, frameBytes)
		copy(frame, out[offset:offset+frameBytes])
		frames = append(frames, frame)
	}
	return frames
}

// abDecodePCM decodes audio into mono 24 kHz int16 samples with ffmpeg.
func abDecodePCM(t *testing.T, path string) []int16 {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "s16le", "-ac", "1", "-ar", fmt.Sprint(abSampleRate), "-").Output()
	if err != nil {
		t.Fatalf("decode audio %s: %v", path, err)
	}
	if len(out)%2 != 0 {
		t.Fatalf("decode audio %s: odd byte count %d", path, len(out))
	}
	samples := make([]int16, len(out)/2)
	for i := range samples {
		samples[i] = int16(uint16(out[2*i]) | uint16(out[2*i+1])<<8)
	}
	return samples
}

// abEncodeTimeline writes one composed timeline artifact (lossless video and
// audio) with ffmpeg, exactly the bytes the projector produced.
func abEncodeTimeline(t *testing.T, path string, frames [][]byte, pcm []int16) {
	t.Helper()
	dir := filepath.Dir(path)
	rawVideo := filepath.Join(dir, "timeline.rgb")
	rawAudio := filepath.Join(dir, "timeline.s16le")
	var video bytes.Buffer
	for _, frame := range frames {
		video.Write(frame)
	}
	if err := os.WriteFile(rawVideo, video.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	audio := make([]byte, len(pcm)*2)
	for i, sample := range pcm {
		audio[2*i] = byte(uint16(sample))
		audio[2*i+1] = byte(uint16(sample) >> 8)
	}
	if err := os.WriteFile(rawAudio, audio, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "rawvideo", "-pixel_format", "rgb24",
		"-video_size", fmt.Sprintf("%dx%d", abWidth, abHeight), "-framerate", fmt.Sprint(abFPS), "-i", rawVideo,
		"-f", "s16le", "-ar", fmt.Sprint(abSampleRate), "-ac", "1", "-i", rawAudio,
		"-map", "0:v", "-map", "1:a",
		"-c:v", "ffv1", "-pix_fmt", "gbrp", "-c:a", "pcm_s16le",
		path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("encode timeline: %v: %s", err, out)
	}
}

// abEntry is one manifest entry with its decoded per-item clip content.
type abEntry struct {
	manifest capoverlay.OverlayManifestEntry
	frames   [][]byte
	pcm      []int16
}

// abProjectTimeline paints the manifest onto one timeline: every entry
// contributes its clip at (in_us, out_us) on its track — higher tracks draw
// on top — and every entry contributes audio at its window (entries sum).
func abProjectTimeline(entries []abEntry, totalFrames int) ([][]byte, []int16) {
	frameBytes := abWidth * abHeight * 3
	samplesPerFrame := abSampleRate / abFPS
	frames := make([][]byte, totalFrames)
	for i := range frames {
		frames[i] = make([]byte, frameBytes)
	}
	pcm := make([]int16, totalFrames*samplesPerFrame)
	ordered := append([]abEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].manifest.Track < ordered[j].manifest.Track
	})
	for _, entry := range ordered {
		startFrame := int(entry.manifest.InUS * abFPS / 1_000_000)
		endFrame := int(entry.manifest.OutUS * abFPS / 1_000_000)
		startSample := int(entry.manifest.InUS * abSampleRate / 1_000_000)
		endSample := int(entry.manifest.OutUS * abSampleRate / 1_000_000)
		for frame := startFrame; frame < endFrame && frame < totalFrames; frame++ {
			copy(frames[frame], entry.frames[frame-startFrame])
		}
		for sample := startSample; sample < endSample && sample < len(pcm); sample++ {
			pcm[sample] += entry.pcm[sample-startSample]
		}
	}
	return frames, pcm
}

func TestManifestTimelineABMediaParityOnRealFixtures(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatalf("the A/B media harness needs ffmpeg: %v", err)
	}
	dir := t.TempDir()
	// Literal windows: 25 fps ⇒ 40 ms frames; 24 kHz ⇒ 24 samples/ms.
	clipA := abFixtureClip(t, dir, "clip-a", "red", 440, 20)
	clipB := abFixtureClip(t, dir, "clip-b", "green", 660, 20)
	clipC := abFixtureClip(t, dir, "clip-c", "blue", 880, 20)
	entries := []abEntry{
		{manifest: capoverlay.OverlayManifestEntry{ItemID: "a", Track: 0, InUS: 0, OutUS: 800_000, ContentHash: strings.Repeat("aa", 32)},
			frames: abDecodeRGB(t, clipA), pcm: abDecodePCM(t, clipA)},
		{manifest: capoverlay.OverlayManifestEntry{ItemID: "b", Track: 0, InUS: 800_000, OutUS: 1_600_000, ContentHash: strings.Repeat("bb", 32)},
			frames: abDecodeRGB(t, clipB), pcm: abDecodePCM(t, clipB)},
		{manifest: capoverlay.OverlayManifestEntry{ItemID: "c", Track: 0, InUS: 1_600_000, OutUS: 2_400_000, ContentHash: strings.Repeat("cc", 32)},
			frames: abDecodeRGB(t, clipC), pcm: abDecodePCM(t, clipC)},
	}
	for _, entry := range entries {
		if err := entry.manifest.Validate(); err != nil {
			t.Fatalf("fixture entry %q: %v", entry.manifest.ItemID, err)
		}
	}
	manifest := capoverlay.OverlayManifest{
		SchemaVersion: capoverlay.SchemaVersionManifest,
		PlanID:        "ab-harness", VideoID: "ab-video",
		Entries: []capoverlay.OverlayManifestEntry{entries[0].manifest, entries[1].manifest, entries[2].manifest},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("fixture manifest: %v", err)
	}
	frames, pcm := abProjectTimeline(entries, 60)
	timeline := filepath.Join(dir, "timeline.mkv")
	abEncodeTimeline(t, timeline, frames, pcm)

	// MEDIA parity: the decoded timeline equals each per-item clip at its
	// LITERAL window and nothing else.
	gotFrames := abDecodeRGB(t, timeline)
	gotPCM := abDecodePCM(t, timeline)
	if len(gotFrames) != 60 {
		t.Fatalf("timeline frames = %d, want 60", len(gotFrames))
	}
	if len(gotPCM) != 60*abSampleRate/abFPS {
		t.Fatalf("timeline samples = %d, want %d", len(gotPCM), 60*abSampleRate/abFPS)
	}
	frameWindows := map[string][2]int{"a": {0, 20}, "b": {20, 40}, "c": {40, 60}}
	sampleWindows := map[string][2]int{"a": {0, 19200}, "b": {19200, 38400}, "c": {38400, 57600}}
	for _, entry := range entries {
		window := frameWindows[entry.manifest.ItemID]
		for frame := window[0]; frame < window[1]; frame++ {
			if !bytes.Equal(gotFrames[frame], entry.frames[frame-window[0]]) {
				t.Fatalf("entry %q frame %d differs from its per-item clip", entry.manifest.ItemID, frame-window[0])
			}
		}
		sWindow := sampleWindows[entry.manifest.ItemID]
		for sample := sWindow[0]; sample < sWindow[1]; sample++ {
			if gotPCM[sample] != entry.pcm[sample-sWindow[0]] {
				t.Fatalf("entry %q sample %d differs from its per-item clip", entry.manifest.ItemID, sample-sWindow[0])
			}
		}
	}
}

func TestManifestTimelineABZOrderParityOnOverlap(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatalf("the A/B media harness needs ffmpeg: %v", err)
	}
	dir := t.TempDir()
	clipA := abFixtureClip(t, dir, "clip-a", "red", 440, 20)
	clipB := abFixtureClip(t, dir, "clip-b", "green", 660, 20)
	entries := []abEntry{
		{manifest: capoverlay.OverlayManifestEntry{ItemID: "a", Track: 0, InUS: 0, OutUS: 800_000, ContentHash: strings.Repeat("aa", 32)},
			frames: abDecodeRGB(t, clipA), pcm: abDecodePCM(t, clipA)},
		{manifest: capoverlay.OverlayManifestEntry{ItemID: "b", Track: 1, InUS: 400_000, OutUS: 1_200_000, ContentHash: strings.Repeat("bb", 32)},
			frames: abDecodeRGB(t, clipB), pcm: abDecodePCM(t, clipB)},
	}
	frames, pcm := abProjectTimeline(entries, 30)
	timeline := filepath.Join(dir, "timeline.mkv")
	abEncodeTimeline(t, timeline, frames, pcm)
	gotFrames := abDecodeRGB(t, timeline)
	gotPCM := abDecodePCM(t, timeline)

	// Pixels: the higher track draws on top in the overlap, the lower track
	// owns its exclusive window, the top track owns the tail.
	for frame := 0; frame < 10; frame++ {
		if !bytes.Equal(gotFrames[frame], entries[0].frames[frame]) {
			t.Fatalf("frame %d must show the track-0 clip", frame)
		}
	}
	for frame := 10; frame < 30; frame++ {
		if !bytes.Equal(gotFrames[frame], entries[1].frames[frame-10]) {
			t.Fatalf("frame %d must show the track-1 clip", frame)
		}
	}
	// Audio: every entry contributes at its window, so the overlap is the
	// literal sum of both clips at their offsets.
	for sample := 0; sample < 28_800; sample++ {
		var want int16
		if sample < 19_200 {
			want += entries[0].pcm[sample]
		}
		if sample >= 9_600 {
			want += entries[1].pcm[sample-9_600]
		}
		if gotPCM[sample] != want {
			t.Fatalf("audio sample %d = %d, want %d", sample, gotPCM[sample], want)
		}
	}
}

// TestManifestTimelineABPathsAgreeOnOrderAndWindows drives the same plan
// through BOTH pipeline paths and compares their identity projections: the
// per-item path and the manifest path must agree on the item order and on the
// golden plan's literal placement windows.
func TestManifestTimelineABPathsAgreeOnOrderAndWindows(t *testing.T) {
	plan := overlayPlanWithPhrases(2)
	wantIDs := []string{"important_phrase", "important_word", "image_overlay", "phrase_extra_1", "phrase_extra_2"}
	wantIn := []int64{667_000, 2_167_000, 3_000_000, 1_000_000, 2_000_000}
	wantOut := []int64{2_033_000, 3_200_000, 4_533_000, 1_500_000, 2_500_000}

	perItem, _ := newManifestIdentityTestEnqueuer(t)
	perItem.client = &countingRenderQueueClient{artifact: certifiedOverlayArtifact()}
	perItem.SetSeparateItemRenders(true)
	refA, err := perItem.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(refA.Items) != len(wantIDs) {
		t.Fatalf("per-item path items = %d, want %d", len(refA.Items), len(wantIDs))
	}
	for i, row := range refA.Items {
		if row.ItemID != wantIDs[i] {
			t.Fatalf("per-item path item[%d] = %q, want %q", i, row.ItemID, wantIDs[i])
		}
	}

	t.Setenv(OverlayManifestTimelineEnv, "1")
	manifestPath, _ := newManifestIdentityTestEnqueuer(t)
	manifestPath.client = &countingRenderQueueClient{artifact: certifiedOverlayArtifact()}
	refB, err := manifestPath.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if refB.Manifest == nil {
		t.Fatal("manifest path must return its manifest")
	}
	if len(refB.Manifest.Entries) != len(wantIDs) {
		t.Fatalf("manifest entries = %d, want %d", len(refB.Manifest.Entries), len(wantIDs))
	}
	for i, entry := range refB.Manifest.Entries {
		if entry.ItemID != wantIDs[i] {
			t.Fatalf("manifest entry[%d] = %q, want %q (both paths must share plan order)", i, entry.ItemID, wantIDs[i])
		}
		if entry.InUS != wantIn[i] || entry.OutUS != wantOut[i] {
			t.Fatalf("manifest entry %q window = [%d,%d), want the per-item placement [%d,%d)",
				entry.ItemID, entry.InUS, entry.OutUS, wantIn[i], wantOut[i])
		}
	}
}
