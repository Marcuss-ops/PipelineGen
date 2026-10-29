package audioasset

import (
	"bytes"
	"os"
	"testing"
	"time"
)

// testMP3Frame builds one MPEG Layer III frame with the given header fields
// and zero-filled payload.
func testMP3Frame(versionBits, bitrateIndex, sampleRateIndex, padding byte, frameLen int) []byte {
	frame := make([]byte, frameLen)
	frame[0] = 0xFF
	frame[1] = 0xE0 | versionBits<<3 | 0x01<<1 | 0x01 // Layer III, no CRC
	frame[2] = bitrateIndex<<4 | sampleRateIndex<<2 | padding<<1
	frame[3] = 0x00
	return frame
}

func TestMP3Duration_MPEG1ExactFrames(t *testing.T) {
	// 128 kbps @ 44.1 kHz: floor(144*128000/44100) = 417 bytes, 1152
	// samples per frame. 100 frames = 115200/44100 s.
	const frames = 100
	const frameLen = 417
	var data bytes.Buffer
	for i := 0; i < frames; i++ {
		data.Write(testMP3Frame(0x03, 9, 0, 0, frameLen))
	}
	path := t.TempDir() + "/mpeg1.mp3"
	if err := os.WriteFile(path, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := mp3Duration(path)
	if err != nil {
		t.Fatalf("mp3Duration: %v", err)
	}
	want := time.Duration(int64(frames) * 1152 * 1_000_000 / 44100 * int64(time.Microsecond))
	if got != want {
		t.Fatalf("duration = %v, want %v", got, want)
	}
}

func TestMP3Duration_MatchesEdgeShapeWithID3Tags(t *testing.T) {
	// Edge TTS writes MPEG-2 Layer III 24 kHz / 48 kbps (144-byte frames,
	// 576 samples). 100 frames = 2.4 s exactly.
	const frames = 100
	const frameLen = 144
	var data bytes.Buffer
	// ID3v2 header with a 10-byte body must be skipped.
	data.WriteString("ID3")
	data.Write([]byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0A})
	data.Write(make([]byte, 10))
	// The leading 64 kbps Xing/Info frame carries no samples and must not
	// shift the audio timeline by one frame.
	info := testMP3Frame(0x02, 8, 1, 0, 192)
	info[3] = 0xC0 // mono, so the tag sits after 9 bytes of side info
	copy(info[4+9:], []byte("Info"))
	data.Write(info)
	for i := 0; i < frames; i++ {
		data.Write(testMP3Frame(0x02, 6, 1, 0, frameLen))
	}
	// Trailing ID3v1 tag must not be parsed as a frame.
	data.WriteString("TAG")
	data.Write(make([]byte, 125))

	path := t.TempDir() + "/edge.mp3"
	if err := os.WriteFile(path, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := mp3Duration(path)
	if err != nil {
		t.Fatalf("mp3Duration: %v", err)
	}
	if want := 2400 * time.Millisecond; got != want {
		t.Fatalf("duration = %v, want %v", got, want)
	}
}

func TestMP3Duration_RejectsNonMP3AndTruncatedFiles(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"not-mp3":   []byte("FAKE_MP3_BYTES"),
		"truncated": testMP3Frame(0x02, 6, 1, 0, 144)[:20],
		"empty":     {},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			path := dir + "/" + name + ".mp3"
			if err := os.WriteFile(path, payload, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := mp3Duration(path); err == nil {
				t.Fatal("expected an error so the media probe stays authoritative")
			}
		})
	}
}
