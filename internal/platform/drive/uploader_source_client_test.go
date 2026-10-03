package drive

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestObjectStoreHTTPClientIsTuned pins the transport contract: the object
// store client must NOT be the bare http.DefaultClient (idle pool 2, no dial
// tuning, no header deadline) — the disk-free resumable upload path streams
// multi-hundred-MB masters through it one 16MB chunk at a time.
func TestObjectStoreHTTPClientIsTuned(t *testing.T) {
	transport, ok := objectStoreHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", objectStoreHTTPClient.Transport)
	}
	if transport.MaxIdleConnsPerHost < 4 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want >= 4 (default 2 starves chunk reuse)", transport.MaxIdleConnsPerHost)
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Fatal("ResponseHeaderTimeout must be set so a hung store surfaces fast")
	}
	if objectStoreHTTPClient.Timeout != 0 {
		t.Fatalf("client Timeout = %v, want 0 (the body stream must never be end-to-end timed out)", objectStoreHTTPClient.Timeout)
	}
	if transport.DialContext == nil {
		t.Fatal("DialContext must be set (explicit keep-alive dialer)")
	}
}

func TestHTTPObjectSourceReadAtServesRanges(t *testing.T) {
	payload := strings.Repeat("abcdefghij", 1024) // 10 KiB
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "" {
			_, _ = fmt.Fprint(w, payload)
			return
		}
		// Minimal Range parser: "bytes=start-end".
		var start, end int
		if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		_, _ = fmt.Fprint(w, payload[start:end+1])
	}))
	defer server.Close()

	src := &httpObjectSource{ctx: context.Background(), url: server.URL, size: int64(len(payload))}

	buf := make([]byte, 7)
	if _, err := src.ReadAt(buf, 100); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(buf) != payload[100:107] {
		t.Fatalf("ReadAt content = %q, want %q", string(buf), payload[100:107])
	}

	// Sequential read path still works through the same tuned client.
	seq := make([]byte, 5)
	if _, err := src.Read(seq); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(seq) != payload[:5] {
		t.Fatalf("Read content = %q, want %q", string(seq), payload[:5])
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestObjectStoreTransportDialDeadlineExists documents the dialer budget: a
// dead object store must fail within seconds, not the OS-level ~2min SYN
// timeout, so the render job's error path stays responsive.
func TestObjectStoreTransportDialDeadlineExists(t *testing.T) {
	transport := newObjectStoreTransport()
	if transport.ResponseHeaderTimeout > time.Minute {
		t.Fatalf("ResponseHeaderTimeout = %v, want <= 1m", transport.ResponseHeaderTimeout)
	}
}
