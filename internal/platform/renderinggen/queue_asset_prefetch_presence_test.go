package renderinggen

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestObjectStoredCachesPositivePresence pins the reuse contract for the
// per-clip prefetch: the same background, source or font in a later clip must
// not pay another HEAD once the object store has confirmed it. Only positive
// answers are cached, so the second call here must not reach the server.
func TestObjectStoredCachesPositivePresence(t *testing.T) {
	resetObjectPresenceCacheForTest()
	t.Cleanup(resetObjectPresenceCacheForTest)

	hash := hashOf([]byte("shared clip background"))
	var heads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+hash {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		heads.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for i := 0; i < 2; i++ {
		present, err := objectStored(context.Background(), srv.URL, hash)
		if err != nil {
			t.Fatalf("objectStored call %d: %v", i+1, err)
		}
		if !present {
			t.Fatalf("objectStored call %d = false, want true", i+1)
		}
	}
	if got := heads.Load(); got != 1 {
		t.Fatalf("HEAD probes = %d, want 1 (the second check must come from the presence cache)", got)
	}

	// Case differences are the same content address.
	if !objectKnownPresent(srv.URL, strings.ToUpper(hash)) {
		t.Fatal("presence cache must canonicalize the content address")
	}
}

// TestObjectStoredDoesNotCacheAbsence keeps the cache safe: an object that is
// absent now can be uploaded by the next clip, so a miss must always be
// re-probed instead of being remembered as permanent.
func TestObjectStoredDoesNotCacheAbsence(t *testing.T) {
	resetObjectPresenceCacheForTest()
	t.Cleanup(resetObjectPresenceCacheForTest)

	hash := hashOf([]byte("not uploaded yet"))
	var heads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+hash {
			http.NotFound(w, r)
			return
		}
		heads.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	for i := 0; i < 2; i++ {
		present, err := objectStored(context.Background(), srv.URL, hash)
		if err != nil {
			t.Fatalf("objectStored call %d: %v", i+1, err)
		}
		if present {
			t.Fatalf("objectStored call %d = true, want false", i+1)
		}
	}
	if got := heads.Load(); got != 2 {
		t.Fatalf("HEAD probes = %d, want 2 (a miss must not be cached)", got)
	}
}

// TestStreamPutMarksObjectPresent proves the second half of the reuse path:
// the upload that fills the object store also teaches the cache, so the next
// clip does not probe the object it just uploaded.
func TestStreamPutMarksObjectPresent(t *testing.T) {
	resetObjectPresenceCacheForTest()
	t.Cleanup(resetObjectPresenceCacheForTest)

	payload := "clip source bytes"
	hash := hashOf([]byte(payload))
	var heads atomic.Int64
	var puts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+hash {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodHead:
			heads.Add(1)
			http.NotFound(w, r)
		case http.MethodPut:
			puts.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	if err := streamPutReader(context.Background(), srv.URL, hash, strings.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("streamPutReader: %v", err)
	}
	if !objectKnownPresent(srv.URL, hash) {
		t.Fatal("a successful upload must mark the content address present")
	}
	present, err := objectStored(context.Background(), srv.URL, hash)
	if err != nil {
		t.Fatalf("objectStored after upload: %v", err)
	}
	if !present {
		t.Fatal("objectStored after upload = false, want true")
	}
	if got := heads.Load(); got != 0 {
		t.Fatalf("HEAD probes after upload = %d, want 0 (the upload already proved presence)", got)
	}
	if got := puts.Load(); got != 1 {
		t.Fatalf("PUT calls = %d, want 1", got)
	}
}

// resetObjectPresenceCacheForTest clears the process-lifetime cache. Tests use
// unique content addresses, but an explicit reset keeps a cache assertion from
// depending on test ordering.
func resetObjectPresenceCacheForTest() {
	objectPresence.mu.Lock()
	objectPresence.entries = make(map[string]time.Time)
	objectPresence.mu.Unlock()
}
