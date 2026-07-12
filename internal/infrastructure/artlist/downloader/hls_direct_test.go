// Package downloader — hls_direct_test.go: focused unit tests for
// the Go-side HLS direct fetcher (Fase 8 / Commit 1, July 2026).
//
// godlike/06 SSOT: these tests pin the CANONICAL behavior of the
// hls_direct.go pipeline. Every test fixture is a real httptest.Server
// (no in-memory mock of net/http) so the test surface is
// observationally equivalent to production HTTP behavior.
//
// godlike/07 fail-closed: the tests verify that the fetcher does
// NOT write partial output on failure (defer-os.Remove on the
// temp file), does NOT concatenate ciphertext (segment write
// happens AFTER decrypt), and DOES honor ctx cancellation at
// every loop iteration.
package downloader

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// helper: make a simple AES-128 key (deterministic, derived from
// seed byte 0x42 so test outputs are reproducible).
func testKey(seed byte) []byte {
	k := make([]byte, 16)
	for i := range k {
		k[i] = seed
	}
	return k
}

// helper: encrypt a plaintext with AES-128-CBC + the given key+iv,
// returning the ciphertext.
func testEncrypt(t *testing.T, key, iv, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("testEncrypt: NewCipher: %v", err)
	}
	if len(plaintext)%aes.BlockSize != 0 {
		t.Fatalf("testEncrypt: plaintext not block-aligned (len=%d)", len(plaintext))
	}
	ct := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, plaintext)
	return ct
}

// helper: pad plaintext to AES block size with PKCS#7.
func testPad(in []byte) []byte {
	padLen := aes.BlockSize - (len(in) % aes.BlockSize)
	pad := make([]byte, padLen)
	for i := range pad {
		pad[i] = byte(padLen)
	}
	return append(in, pad...)
}

// TestParseM3U8_MasterPlaylist pins the master-playlist descent
// (selectHighestBandwidthVariant reads BANDWIDTH).
func TestParseM3U8_MasterPlaylist(t *testing.T) {
	body := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720
720p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080
1080p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480
480p.m3u8
`
	pl, err := ParseM3U8(body)
	if err != nil {
		t.Fatalf("ParseM3U8: %v", err)
	}
	if !pl.IsMaster {
		t.Errorf("IsMaster=false, want true")
	}
	if got := len(pl.Variants); got != 3 {
		t.Fatalf("len(Variants)=%d, want 3", got)
	}
	if pl.Variants[1].Bandwidth != 5000000 {
		t.Errorf("Variants[1].Bandwidth=%d, want 5000000", pl.Variants[1].Bandwidth)
	}
	best := selectHighestBandwidthVariant(pl.Variants)
	if best.URI != "1080p.m3u8" {
		t.Errorf("best.URI=%q, want %q", best.URI, "1080p.m3u8")
	}
}

// TestParseM3U8_MediaPlaylistPlain pins the unencrypted media
// playlist parse (no EXT-X-KEY).
func TestParseM3U8_MediaPlaylistPlain(t *testing.T) {
	body := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:10
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:6.0,
seg0.ts
#EXTINF:6.0,
seg1.ts
#EXTINF:4.5,
seg2.ts
#EXT-X-ENDLIST
`
	pl, err := ParseM3U8(body)
	if err != nil {
		t.Fatalf("ParseM3U8: %v", err)
	}
	if pl.IsMaster {
		t.Errorf("IsMaster=true, want false")
	}
	if got := len(pl.Segments); got != 3 {
		t.Fatalf("len(Segments)=%d, want 3", got)
	}
	if pl.Segments[0].URI != "seg0.ts" {
		t.Errorf("Segments[0].URI=%q, want %q", pl.Segments[0].URI, "seg0.ts")
	}
	if pl.Segments[0].Sequence != 0 {
		t.Errorf("Segments[0].Sequence=%d, want 0", pl.Segments[0].Sequence)
	}
	if pl.Segments[1].Sequence != 1 {
		t.Errorf("Segments[1].Sequence=%d, want 1", pl.Segments[1].Sequence)
	}
	if pl.Segments[0].Key != nil {
		t.Errorf("Segments[0].Key=%+v, want nil (unencrypted)", pl.Segments[0].Key)
	}
	if pl.Segments[0].Duration != 6*time.Second {
		t.Errorf("Segments[0].Duration=%v, want 6s", pl.Segments[0].Duration)
	}
	if !pl.EndList {
		t.Errorf("EndList=false, want true")
	}
}

// TestParseM3U8_MediaPlaylistAES128 pins the AES-128 directive parse.
func TestParseM3U8_MediaPlaylistAES128(t *testing.T) {
	body := `#EXTM3U
#EXT-X-VERSION:5
#EXT-X-TARGETDURATION:10
#EXT-X-MEDIA-SEQUENCE:7
#EXT-X-KEY:METHOD=AES-128,URI="https://key.example/k.bin",IV=0x9c7db8778570d05c3177c349fd9236af
#EXTINF:6.0,
seg7.ts
#EXTINF:6.0,
seg8.ts
#EXT-X-ENDLIST
`
	pl, err := ParseM3U8(body)
	if err != nil {
		t.Fatalf("ParseM3U8: %v", err)
	}
	if pl.MediaSeq != 7 {
		t.Errorf("MediaSeq=%d, want 7", pl.MediaSeq)
	}
	if pl.Segments[0].Key == nil {
		t.Fatalf("Segments[0].Key=nil, want AES-128 key directive")
	}
	if pl.Segments[0].Key.Method != "AES-128" {
		t.Errorf("Key.Method=%q, want AES-128", pl.Segments[0].Key.Method)
	}
	if pl.Segments[0].Key.URI != "https://key.example/k.bin" {
		t.Errorf("Key.URI=%q, want https://key.example/k.bin", pl.Segments[0].Key.URI)
	}
	if pl.Segments[0].Key.IV != "9c7db8778570d05c3177c349fd9236af" {
		t.Errorf("Key.IV=%q, want 9c7db8778570d05c3177c349fd9236af", pl.Segments[0].Key.IV)
	}
	if pl.Segments[0].Sequence != 7 {
		t.Errorf("Segments[0].Sequence=%d, want 7 (MediaSeq)", pl.Segments[0].Sequence)
	}
	if pl.Segments[1].Sequence != 8 {
		t.Errorf("Segments[1].Sequence=%d, want 8 (MediaSeq+1)", pl.Segments[1].Sequence)
	}
	// The second segment should also carry the same key
	// (the directive is sticky until a new KEY directive appears).
	if pl.Segments[1].Key == nil || pl.Segments[1].Key.URI != "https://key.example/k.bin" {
		t.Errorf("Segments[1].Key=%+v, want the same AES-128 directive (sticky)", pl.Segments[1].Key)
	}
}

// TestResolveIV_ExplicitBeatsSequence pins the IV resolution
// precedence (explicit IV attribute wins over media-sequence fallback).
// Sub-cases also cover the "0x"/"0X" prefix-strip path that the
// basher caught as a regression in the prior round.
func TestResolveIV_ExplicitBeatsSequence(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"pure hex (32 chars)", "0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef"},
		{"with 0x prefix (HLS wire format)", "0x0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef"},
		{"with 0X prefix (uppercase)", "0X0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef"},
		{"with whitespace around 0x", " 0x0123456789abcdef0123456789abcdef ", "0123456789abcdef0123456789abcdef"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveIV(tc.in, 0)
			if err != nil {
				t.Fatalf("resolveIV(%q): %v", tc.in, err)
			}
			if hex.EncodeToString(got) != tc.want {
				t.Errorf("resolveIV(%q) = %s, want %s", tc.in, hex.EncodeToString(got), tc.want)
			}
		})
	}

	// Implicit: sequence=42 → 16 bytes, last 8 = 0x000000000000002a.
	implicit := IVFromSequence(42)
	wantImplicit := make([]byte, 16)
	wantImplicit[15] = 42
	if !bytesEqual(implicit, wantImplicit) {
		t.Errorf("implicit IV mismatch: got %x want %x", implicit, wantImplicit)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestResolveRelativeURL pins the URI resolution edge cases
// (absolute wins; relative uses base; empty base fails).
func TestResolveRelativeURL(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		ref     string
		want    string
		wantErr bool
	}{
		{"absolute http", "https://x.example/m.m3u8", "http://other.example/s.ts", "http://other.example/s.ts", false},
		{"absolute https", "http://x.example/m.m3u8", "https://other.example/s.ts", "https://other.example/s.ts", false},
		{"relative path", "https://x.example/dir/m.m3u8", "seg0.ts", "https://x.example/dir/seg0.ts", false},
		{"relative absolute path", "https://x.example/dir/m.m3u8", "/other/seg0.ts", "https://x.example/other/seg0.ts", false},
		{"empty ref", "https://x.example/m.m3u8", "", "", true},
		{"empty base + relative", "", "seg0.ts", "", true},
		{"empty base + absolute", "", "https://x.example/s.ts", "https://x.example/s.ts", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRelativeURL(tc.base, tc.ref)
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

// TestFetchAndCompose_PlainSegments pins the unencrypted HLS
// composition end-to-end with a real httptest.Server serving both
// the playlist and 3 .ts segment files.
func TestFetchAndCompose_PlainSegments(t *testing.T) {
	// 3 fake .ts segments (MPEG-TS would normally start with 0x47;
	// we just write 188 bytes of deterministic data per segment —
	// the HLS fetcher doesn't care about the bytes, only that they
	// land in the output).
	segments := [][]byte{
		make([]byte, 188),
		make([]byte, 188),
		make([]byte, 188),
	}
	for i, s := range segments {
		for j := range s {
			s[j] = byte(i*10 + j)
		}
	}
	playlistBody := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		"#EXTINF:6.0,\nseg0.ts\n#EXTINF:6.0,\nseg1.ts\n#EXTINF:4.0,\nseg2.ts\n#EXT-X-ENDLIST\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, playlistBody)
		case strings.HasSuffix(r.URL.Path, "seg0.ts"):
			_, _ = w.Write(segments[0])
		case strings.HasSuffix(r.URL.Path, "seg1.ts"):
			_, _ = w.Write(segments[1])
		case strings.HasSuffix(r.URL.Path, "seg2.ts"):
			_, _ = w.Write(segments[2])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	outDir := t.TempDir()
	f := NewHLSFetcher(HLSConfig{TotalTimeout: 5 * time.Second}, nil)
	res, err := f.FetchAndCompose(context.Background(), srv.URL+"/master.m3u8", outDir)
	if err != nil {
		t.Fatalf("FetchAndCompose: %v", err)
	}
	defer os.Remove(res.OutputPath)

	if res.SegmentsFetched != 3 {
		t.Errorf("SegmentsFetched=%d, want 3", res.SegmentsFetched)
	}
	if res.BytesWritten != int64(188*3) {
		t.Errorf("BytesWritten=%d, want %d", res.BytesWritten, 188*3)
	}
	if res.Duration != 16*time.Second {
		t.Errorf("Duration=%v, want 16s", res.Duration)
	}
	if res.KeyURL != "" {
		t.Errorf("KeyURL=%q, want empty (unencrypted)", res.KeyURL)
	}

	// Verify the on-disk bytes equal the concatenation of the 3
	// plaintext segments in order (NEVER ciphertext).
	got, err := os.ReadFile(res.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want := append(append(append([]byte{}, segments[0]...), segments[1]...), segments[2]...)
	if !bytesEqual(got, want) {
		t.Errorf("output bytes do not match plaintext concat (got len=%d, want len=%d)", len(got), len(want))
	}
}

// TestFetchAndCompose_AES128DecryptBeforeCompose pins the
// godlike/07 fail-closed contract: every segment is decrypted
// BEFORE being written to the output. The on-disk bytes MUST
// match the plaintext concatenation, NEVER the ciphertext.
func TestFetchAndCompose_AES128DecryptBeforeCompose(t *testing.T) {
	key := testKey(0x42)
	// 3 segments of plaintext, each padded to 16-byte boundary.
	plaintexts := [][]byte{
		testPad([]byte("first segment content ")), // 32 bytes
		testPad([]byte("second segment longer ")), // 32 bytes
		testPad([]byte("third ")),                 // 16 bytes
	}
	// Build a single key URL (shared across all 3 segments per
	// HLS spec — the KEY directive is sticky).
	keyBody := key
	keyURI := "/key.bin"

	// Media-sequence starts at 0; per HLS spec, the implicit IV
	// is the sequence number encoded as a 128-bit big-endian.
	ciphertexts := make([][]byte, 3)
	for i, pt := range plaintexts {
		iv := IVFromSequence(int64(i))
		ciphertexts[i] = testEncrypt(t, key, iv, pt)
	}

	playlistBody := "#EXTM3U\n#EXT-X-VERSION:5\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		// No IV= attribute — exercises the media-sequence-number
		// fallback per HLS spec §4.3.2.4 (when the IV= attribute
		// is absent, the IV is the segment's media sequence
		// number encoded as a 128-bit big-endian integer).
		fmt.Sprintf("#EXT-X-KEY:METHOD=AES-128,URI=%q\n", keyURI) +
		"#EXTINF:6.0,\nseg0.ts\n#EXTINF:6.0,\nseg1.ts\n#EXTINF:3.0,\nseg2.ts\n#EXT-X-ENDLIST\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, playlistBody)
		case r.URL.Path == keyURI:
			_, _ = w.Write(keyBody)
		case strings.HasSuffix(r.URL.Path, "seg0.ts"):
			_, _ = w.Write(ciphertexts[0])
		case strings.HasSuffix(r.URL.Path, "seg1.ts"):
			_, _ = w.Write(ciphertexts[1])
		case strings.HasSuffix(r.URL.Path, "seg2.ts"):
			_, _ = w.Write(ciphertexts[2])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	outDir := t.TempDir()
	f := NewHLSFetcher(HLSConfig{TotalTimeout: 5 * time.Second}, nil)
	res, err := f.FetchAndCompose(context.Background(), srv.URL+"/enc.m3u8", outDir)
	if err != nil {
		t.Fatalf("FetchAndCompose: %v", err)
	}
	defer os.Remove(res.OutputPath)

	if res.SegmentsFetched != 3 {
		t.Errorf("SegmentsFetched=%d, want 3", res.SegmentsFetched)
	}

	// godlike/07 fail-closed: the on-disk bytes MUST equal the
	// plaintext concat, NOT the ciphertext concat. This is the
	// "mai concatenare ciphertext" guarantee.
	got, err := os.ReadFile(res.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want := append(append(append([]byte{}, plaintexts[0]...), plaintexts[1]...), plaintexts[2]...)
	if !bytesEqual(got, want) {
		t.Errorf("output is NOT plaintext (godlike/07 fail-closed: 'mai concatenare ciphertext' violated); got %d bytes, want %d bytes", len(got), len(want))
	}
	// Belt-and-suspenders: also verify it does NOT match the
	// ciphertext concat (defends against a "both happen to be
	// equal" fluke).
	ctConcat := append(append(append([]byte{}, ciphertexts[0]...), ciphertexts[1]...), ciphertexts[2]...)
	if bytesEqual(got, ctConcat) {
		t.Errorf("output matches ciphertext (godlike/07 fail-closed: ciphertext leaked to disk)")
	}

	if res.KeyURL != srv.URL+keyURI {
		t.Errorf("KeyURL=%q, want %q", res.KeyURL, srv.URL+keyURI)
	}
}

// TestFetchAndCompose_ExplicitIV_OverridesSequence pins the
// "rispetta IV" contract: when EXT-X-KEY carries an IV= attribute,
// the fetcher MUST use that IV (not the media-sequence-number
// fallback). Wrong-IV decryption MUST fail or produce wrong
// plaintext; the test verifies the correct plaintext appears.
func TestFetchAndCompose_ExplicitIV_OverridesSequence(t *testing.T) {
	key := testKey(0x99)
	// Explicit IV: 0x00000000000000000000000000000001 (sequence 0
	// would have used 0x00000000000000000000000000000000 — so
	// the plaintext below is the explicit-IV plaintext, not the
	// sequence-0 plaintext).
	explicitIV := make([]byte, 16)
	explicitIV[15] = 0x01
	explicitIVHex := hex.EncodeToString(explicitIV)

	plaintext := testPad([]byte("explicit-IV segment "))
	ciphertext := testEncrypt(t, key, explicitIV, plaintext)

	keyURI := "/k.bin"
	playlistBody := "#EXTM3U\n#EXT-X-VERSION:5\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		fmt.Sprintf("#EXT-X-KEY:METHOD=AES-128,URI=%q,IV=0x%s\n", keyURI, explicitIVHex) +
		"#EXTINF:6.0,\nseg0.ts\n#EXT-X-ENDLIST\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, playlistBody)
		case r.URL.Path == keyURI:
			_, _ = w.Write(key)
		case strings.HasSuffix(r.URL.Path, "seg0.ts"):
			_, _ = w.Write(ciphertext)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	outDir := t.TempDir()
	f := NewHLSFetcher(HLSConfig{TotalTimeout: 5 * time.Second}, nil)
	res, err := f.FetchAndCompose(context.Background(), srv.URL+"/m.m3u8", outDir)
	if err != nil {
		t.Fatalf("FetchAndCompose: %v", err)
	}
	defer os.Remove(res.OutputPath)

	got, err := os.ReadFile(res.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytesEqual(got, plaintext) {
		t.Errorf("output does not match explicit-IV plaintext: got %q want %q", got, plaintext)
	}
}

// TestFetchAndCompose_RedirectHandoff pins the redirect + signed-URL
// flow: the playlist responds with 302 → the fetcher follows up to
// 5 hops and lands on the real playlist.
func TestFetchAndCompose_RedirectHandoff(t *testing.T) {
	segments := [][]byte{make([]byte, 188), make([]byte, 188)}
	for i, s := range segments {
		for j := range s {
			s[j] = byte(0x10 + i*10 + j)
		}
	}
	finalPlaylist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		"#EXTINF:6.0,\nseg0.ts\n#EXTINF:6.0,\nseg1.ts\n#EXT-X-ENDLIST\n"
	var hits int32

	// Final server: serves the actual playlist + segments.
	finalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, finalPlaylist)
		case strings.HasSuffix(r.URL.Path, "seg0.ts"):
			_, _ = w.Write(segments[0])
		case strings.HasSuffix(r.URL.Path, "seg1.ts"):
			_, _ = w.Write(segments[1])
		default:
			http.NotFound(w, r)
		}
	}))
	defer finalSrv.Close()

	// Redirector: 302 -> finalSrv.
	redirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, finalSrv.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirSrv.Close()

	outDir := t.TempDir()
	f := NewHLSFetcher(HLSConfig{TotalTimeout: 5 * time.Second}, nil)
	res, err := f.FetchAndCompose(context.Background(), redirSrv.URL+"/m.m3u8", outDir)
	if err != nil {
		t.Fatalf("FetchAndCompose: %v", err)
	}
	defer os.Remove(res.OutputPath)
	if res.SegmentsFetched != 2 {
		t.Errorf("SegmentsFetched=%d, want 2 (after redirect)", res.SegmentsFetched)
	}
	if atomic.LoadInt32(&hits) < 1 {
		t.Errorf("final server was not hit (redirect not followed)")
	}
}

// TestFetchAndCompose_Cancellation pins the ctx-cancellation
// contract: a cancelled context aborts the pipeline within one
// segment, returning context.Canceled (not a wrapped transport
// error).
func TestFetchAndCompose_Cancellation(t *testing.T) {
	// 1 slow segment: respond after 200ms.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:6.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
		case strings.HasSuffix(r.URL.Path, "seg0.ts"):
			time.Sleep(200 * time.Millisecond)
			_, _ = w.Write(make([]byte, 188))
		}
	}))
	defer srv.Close()

	outDir := t.TempDir()
	f := NewHLSFetcher(HLSConfig{
		TotalTimeout:        5 * time.Second,
		SegmentFetchTimeout: 1 * time.Second,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after 50ms — before the 200ms segment response.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := f.FetchAndCompose(ctx, srv.URL+"/m.m3u8", outDir)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("FetchAndCompose: expected error, got nil")
	}
	// Should bail within 500ms (well below the 200ms segment delay
	// + 1s per-fetch timeout).
	if elapsed > 500*time.Millisecond {
		t.Errorf("FetchAndCompose did not honor ctx cancel quickly: elapsed=%v err=%v", elapsed, err)
	}
	if !isCtxErr(err) {
		t.Errorf("err is not a ctx error: %v", err)
	}
}

func isCtxErr(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "context canceled") ||
		strings.Contains(err.Error(), "context deadline exceeded"))
}

// TestFetchAndCompose_EmptyPlaylistFailsClosed pins the
// godlike/07 fail-closed contract: a playlist that parses cleanly
// but contains no segments is malformed input and MUST error
// (never silently produce an empty .ts).
func TestFetchAndCompose_EmptyPlaylistFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-ENDLIST\n")
		}
	}))
	defer srv.Close()

	outDir := t.TempDir()
	f := NewHLSFetcher(HLSConfig{TotalTimeout: 2 * time.Second}, nil)
	res, err := f.FetchAndCompose(context.Background(), srv.URL+"/empty.m3u8", outDir)
	if err == nil {
		t.Fatalf("FetchAndCompose: expected error on empty playlist, got result=%+v", res)
	}
	if !strings.Contains(err.Error(), "no segments") {
		t.Errorf("err does not mention 'no segments': %v", err)
	}
	// godlike/07 fail-closed: the temp file must NOT linger in
	// outDir after a failed compose.
	entries, _ := os.ReadDir(outDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file after failure: %s", e.Name())
		}
	}
	_ = filepath.Base // silence unused
}

// TestFetchAndCompose_TooManySegmentsFailsClosed pins the
// MaxSegments defense-in-depth cap.
func TestFetchAndCompose_TooManySegmentsFailsClosed(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n")
	for i := 0; i < 10; i++ {
		sb.WriteString("#EXTINF:6.0,\nseg")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(".ts\n")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			_, _ = io.WriteString(w, sb.String())
		}
	}))
	defer srv.Close()

	outDir := t.TempDir()
	f := NewHLSFetcher(HLSConfig{
		TotalTimeout: 2 * time.Second,
		MaxSegments:  3,
	}, nil)
	_, err := f.FetchAndCompose(context.Background(), srv.URL+"/big.m3u8", outDir)
	if err == nil {
		t.Fatalf("FetchAndCompose: expected error on over-cap playlist")
	}
	if !strings.Contains(err.Error(), "max=3") {
		t.Errorf("err does not mention 'max=3': %v", err)
	}
}

// TestFetchWithRetry_RetriesTransientThenSucceeds pins the
// canonical pkg/retry typed-path behavior: the first call returns
// 503 (transient), the second returns 200 (success). The fetcher
// MUST transparently retry and the body MUST match the second
// call's response. Without this test, the retry.Do migration
// would be unverified and a future regression to the hand-rolled
// loop could ship silently.
func TestFetchWithRetry_RetriesTransientThenSucceeds(t *testing.T) {
	var calls int32
	body := []byte("hello-payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	f := NewHLSFetcher(HLSConfig{
		MaxAttempts: 3,
	}, nil)
	got, err := f.fetchWithRetry(context.Background(), srv.URL+"/p.bin", 5*time.Second, HLSSegmentByteRange{})
	if err != nil {
		t.Fatalf("fetchWithRetry: %v", err)
	}
	if !bytesEqual(got, body) {
		t.Errorf("body mismatch: got %q want %q", got, body)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("expected 2 calls (1 fail + 1 success), got %d", n)
	}
}

// TestFetchWithRetry_Terminal4xxStopsImmediately pins the
// "4xx non-429 is terminal" branch: a 404 response is NOT
// retried (the canonical retry.IsTransient predicate reads the
// 4xx shape from the registered classifiers and returns false).
func TestFetchWithRetry_Terminal4xxStopsImmediately(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewHLSFetcher(HLSConfig{
		MaxAttempts: 5,
	}, nil)
	_, err := f.fetchWithRetry(context.Background(), srv.URL+"/missing.bin", 5*time.Second, HLSSegmentByteRange{})
	if err == nil {
		t.Fatalf("fetchWithRetry: expected error on 404, got nil")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("expected exactly 1 call (4xx is terminal), got %d", n)
	}
}

// strconv.Itoa is in strconv pkg; we add a tiny alias here so
// the test file does not need an extra import (the package is
// already pulled by the production file).
