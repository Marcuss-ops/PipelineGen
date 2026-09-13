package drive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// rangeServer serves a byte payload with HTTP Range support, which is what
// makes a disk-free resumable Drive upload possible.
func rangeServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}
		var start, end int
		if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if start < 0 || start >= len(payload) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if end >= len(payload) {
			end = len(payload) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
}

// TestHTTPObjectSource_RangeReadsAndSequentialRead pins the two access modes the
// Drive uploader needs: ReadAt (resumable) via Range requests and Read (simple)
// via one GET.
func TestHTTPObjectSource_RangeReadsAndSequentialRead(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	server := rangeServer(t, payload)
	defer server.Close()

	src := &httpObjectSource{ctx: context.Background(), url: server.URL, size: int64(len(payload))}
	defer src.Close()

	buf := make([]byte, 5)
	n, err := src.ReadAt(buf, 10)
	if err != nil || n != 5 {
		t.Fatalf("ReadAt(10) = %d, %v; want 5, nil", n, err)
	}
	if string(buf) != "abcde" {
		t.Fatalf("ReadAt(10) = %q, want %q", buf, "abcde")
	}
	if _, err := src.ReadAt(buf, int64(len(payload))); err != io.EOF {
		t.Fatalf("ReadAt(past EOF) error = %v, want io.EOF", err)
	}

	all, err := io.ReadAll(src)
	if err != nil {
		t.Fatalf("sequential Read: %v", err)
	}
	if string(all) != string(payload) {
		t.Fatalf("sequential Read = %q, want the full payload", all)
	}
}

// TestHTTPObjectSource_NonRangeServerFallback pins behavior when the remote
// server ignores Range headers and returns 200 OK from byte 0.
func TestHTTPObjectSource_NonRangeServerFallback(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	src := &httpObjectSource{ctx: context.Background(), url: server.URL, size: int64(len(payload))}
	defer src.Close()

	buf := make([]byte, 5)
	n, err := src.ReadAt(buf, 10)
	if err != nil || n != 5 {
		t.Fatalf("ReadAt(10) = %d, %v; want 5, nil", n, err)
	}
	if string(buf) != "abcde" {
		t.Fatalf("ReadAt(10) = %q, want %q", buf, "abcde")
	}
}

// TestOpenUploadSource_FailClosed pins the source resolution rules: a local
// path wins, a remote URL needs an expected size, and neither is a typed error.
func TestOpenUploadSource_FailClosed(t *testing.T) {
	u := &Uploader{}
	if _, _, err := u.openUploadSource(context.Background(), PutFileRequest{}); err == nil {
		t.Fatal("openUploadSource must fail closed with neither local path nor source url")
	}
	if _, _, err := u.openUploadSource(context.Background(), PutFileRequest{SourceURL: "http://store/object"}); err == nil {
		t.Fatal("openUploadSource must fail closed when a remote source has no expected size")
	}
	src, size, err := u.openUploadSource(context.Background(), PutFileRequest{
		SourceURL: "http://store/object", ExpectedSize: 4096,
	})
	if err != nil {
		t.Fatalf("openUploadSource(remote): %v", err)
	}
	defer src.Close()
	if size != 4096 {
		t.Fatalf("remote source size = %d, want 4096", size)
	}
	if _, ok := src.(*httpObjectSource); !ok {
		t.Fatalf("remote source = %T, want *httpObjectSource", src)
	}
}
