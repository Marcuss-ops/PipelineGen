package wiring

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func testMP4Box(typ string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], typ)
	copy(box[8:], payload)
	return box
}

func TestReadAVCDecoderConfigurationSHA256(t *testing.T) {
	avcC := []byte{
		0x01, 0x64, 0x00, 0x28, 0xff, 0xe1, 0x00, 0x1e,
		0x67, 0x64, 0x00, 0x28, 0xac, 0xd9, 0x40, 0x78,
		0x02, 0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00,
		0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60,
		0xc6, 0x58, 0x01, 0x00, 0x05, 0x68, 0xeb, 0xec,
		0xb2, 0x2c,
	}
	sampleEntry := make([]byte, 78)
	sampleEntry = append(sampleEntry, testMP4Box("avcC", avcC)...)
	stsdPayload := append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, testMP4Box("avc1", sampleEntry)...)
	stsd := testMP4Box("stsd", stsdPayload)
	stbl := testMP4Box("stbl", stsd)
	minf := testMP4Box("minf", stbl)
	hdlrPayload := append(make([]byte, 8), []byte("vide")...)
	hdlrPayload = append(hdlrPayload, make([]byte, 12)...)
	mdia := testMP4Box("mdia", append(testMP4Box("hdlr", hdlrPayload), minf...))
	trak := testMP4Box("trak", mdia)
	moov := testMP4Box("moov", trak)
	fileBytes := append(testMP4Box("ftyp", []byte("isom\x00\x00\x02\x00isom")), moov...)
	path := filepath.Join(t.TempDir(), "fragment.mp4")
	if err := os.WriteFile(path, fileBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readAVCDecoderConfigurationSHA256(path)
	if err != nil {
		t.Fatalf("read avcC: %v", err)
	}
	sum := sha256.Sum256(avcC)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("avcC hash = %s, want %s", got, want)
	}
}

// TestReadAVCDecoderConfigurationSHA256OnCanonicalFragment verifies the parser
// against the real A fragment used to establish the canonical hash. It is a
// skipped check on a clean checkout (the fragment cache is runtime data), but
// when the evidence is present it proves the parser and the constant name the
// same bytes.
func TestReadAVCDecoderConfigurationSHA256OnCanonicalFragment(t *testing.T) {
	path := filepath.Join("..", "..", "..", "data", "final-job-fragments", "92",
		"92be7b8e512249a4bc079de665ce3ed0bbd7a1d31818b356b6d83e0f51db0ed3.mp4")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("canonical fragment not present: %v", err)
	}
	got, err := readAVCDecoderConfigurationSHA256(path)
	if err != nil {
		t.Fatalf("read canonical avcC: %v", err)
	}
	if got != kernelmedia.CanonicalVideoExtradataSHA256 {
		t.Fatalf("canonical fragment avcC = %s, want %s", got, kernelmedia.CanonicalVideoExtradataSHA256)
	}
}

func TestReadAVCDecoderConfigurationRejectsNonH264(t *testing.T) {
	sampleEntry := make([]byte, 78)
	sampleEntry = append(sampleEntry, testMP4Box("avcC", []byte{1, 2, 3})...)
	stsdPayload := append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, testMP4Box("encv", sampleEntry)...)
	stsd := testMP4Box("stsd", stsdPayload)
	hdlrPayload := append(make([]byte, 8), []byte("vide")...)
	hdlrPayload = append(hdlrPayload, make([]byte, 12)...)
	mdia := testMP4Box("mdia", append(testMP4Box("hdlr", hdlrPayload), testMP4Box("minf", testMP4Box("stbl", stsd))...))
	fileBytes := testMP4Box("moov", testMP4Box("trak", mdia))
	path := filepath.Join(t.TempDir(), "encrypted.mp4")
	if err := os.WriteFile(path, fileBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAVCDecoderConfigurationSHA256(path); err == nil {
		t.Fatal("a non-H.264 sample entry must not produce an avcC identity")
	}
}
