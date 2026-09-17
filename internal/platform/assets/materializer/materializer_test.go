package materializer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func addressOf(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func originServer(t *testing.T, body []byte) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestEnsure_UsesVerifiedLocalHintWithoutFetching pins the optimization path:
// bytes already produced by this process are installed without a download.
func TestEnsure_UsesVerifiedLocalHintWithoutFetching(t *testing.T) {
	payload := []byte("producer-local verified bytes")
	address := addressOf(payload)
	local := writeFile(t, t.TempDir(), "asset.bin", payload)

	srv, hits := originServer(t, []byte("SHOULD NOT BE FETCHED"))

	got, err := New(Options{}).Ensure(context.Background(),
		Ref{AssetID: "a1", SHA256: address}, Source{LocalPath: local, URL: srv.URL}, Dest(t.TempDir(), address))
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got.Origin != OriginLocal {
		t.Errorf("origin = %q, want %q", got.Origin, OriginLocal)
	}
	if *hits != 0 {
		t.Errorf("origin was fetched %d time(s); a verified local hint must not download", *hits)
	}
	if !New(Options{}).Matches(got.Path, address) {
		t.Error("materialized file does not hash to the requested address")
	}
}

// TestEnsure_RejectsMismatchingLocalHintAndFallsBackToOrigin is the regression
// test for the reported failure: a stale local path must never be installed
// under someone else's address, and must not fail a recoverable job either.
func TestEnsure_RejectsMismatchingLocalHintAndFallsBackToOrigin(t *testing.T) {
	payload := []byte("the bytes the address actually names")
	address := addressOf(payload)
	stale := writeFile(t, t.TempDir(), "stale.bin", []byte("a different file entirely"))

	srv, hits := originServer(t, payload)

	got, err := New(Options{}).Ensure(context.Background(),
		Ref{AssetID: "a1", SHA256: address}, Source{LocalPath: stale, URL: srv.URL}, Dest(t.TempDir(), address))
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got.Origin != OriginURL {
		t.Errorf("origin = %q, want %q (a rejected hint must fall through to the origin)", got.Origin, OriginURL)
	}
	if *hits != 1 {
		t.Errorf("origin fetch count = %d, want 1", *hits)
	}
	if !New(Options{}).Matches(got.Path, address) {
		t.Error("materialized file does not hash to the requested address")
	}
}

// TestEnsure_ReVerifiesCacheHits is the invariant both legacy warming paths got
// wrong: trusting a file because it exists turns a disk fault into a silently
// wrong render. A cache entry that no longer matches is never returned.
func TestEnsure_ReVerifiesCacheHits(t *testing.T) {
	payload := []byte("canonical bytes")
	address := addressOf(payload)
	root := t.TempDir()

	corrupt := Dest(root, address)
	if err := os.MkdirAll(filepath.Dir(corrupt), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(corrupt, []byte("truncated or replaced"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	srv, _ := originServer(t, payload)

	got, err := New(Options{}).Ensure(context.Background(),
		Ref{AssetID: "a1", SHA256: address}, Source{URL: srv.URL}, Dest(root, address))
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got.Origin != OriginURL {
		t.Errorf("origin = %q, want %q (a corrupt cache entry must be repaired, not reused)", got.Origin, OriginURL)
	}
	if !New(Options{}).Matches(got.Path, address) {
		t.Error("repaired file does not hash to the requested address")
	}
}

// TestEnsure_CorruptCacheWithoutSourceFailsClosed: when the bytes are wrong and
// nothing can repair them the caller must hear about it, with the corruption
// named rather than a generic miss.
func TestEnsure_CorruptCacheWithoutSourceFailsClosed(t *testing.T) {
	payload := []byte("canonical bytes")
	address := addressOf(payload)
	root := t.TempDir()

	corrupt := Dest(root, address)
	if err := os.MkdirAll(filepath.Dir(corrupt), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(corrupt, []byte("wrong"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := New(Options{}).Ensure(context.Background(),
		Ref{AssetID: "a1", SHA256: address}, Source{}, Dest(root, address))
	if err == nil {
		t.Fatal("expected a fail-closed error")
	}
	if !errors.Is(err, ErrCorruptCache) && !errors.Is(err, ErrSourceUnavailable) {
		t.Errorf("error = %v, want ErrCorruptCache or ErrSourceUnavailable", err)
	}
}

// TestEnsure_NoSourceIsTypedAndAttributable: a refused local hint with no
// fetchable origin must not degrade into a silent skip.
func TestEnsure_NoSourceIsTypedAndAttributable(t *testing.T) {
	address := addressOf([]byte("expected"))
	stale := writeFile(t, t.TempDir(), "stale.bin", []byte("different"))

	_, err := New(Options{}).Ensure(context.Background(),
		Ref{AssetID: "a1", SHA256: address}, Source{LocalPath: stale, URL: "assets/logical-path.bin"}, Dest(t.TempDir(), address))
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("error = %v, want ErrSourceUnavailable", err)
	}
}

// TestEnsure_MismatchingOriginLeavesNothingBehind: a wrong origin cannot poison
// the destination address.
func TestEnsure_MismatchingOriginLeavesNothingBehind(t *testing.T) {
	address := addressOf([]byte("expected bytes"))
	root := t.TempDir()
	srv, _ := originServer(t, []byte("served the wrong bytes"))

	_, err := New(Options{}).Ensure(context.Background(),
		Ref{AssetID: "a1", SHA256: address}, Source{URL: srv.URL}, Dest(root, address))
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("error = %v, want ErrHashMismatch", err)
	}

	dest := Dest(root, address)
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("a rejected install must leave nothing at the destination")
	}
	entries, readErr := os.ReadDir(filepath.Dir(dest))
	if readErr == nil {
		for _, entry := range entries {
			t.Errorf("temporary file left behind: %s", entry.Name())
		}
	}
}

// TestEnsure_InvalidRefFailsBeforeAnyIO keeps caller errors distinguishable
// from storage and network failures.
func TestEnsure_InvalidRefFailsBeforeAnyIO(t *testing.T) {
	cases := []struct {
		name string
		ref  Ref
	}{
		{"empty address", Ref{AssetID: "a1"}},
		{"single character", Ref{AssetID: "a1", SHA256: "a"}},
		{"not hex", Ref{AssetID: "a1", SHA256: "zzzz"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Options{}).Ensure(context.Background(), tc.ref, Source{URL: "http://127.0.0.1:1/x"}, Dest(t.TempDir(), "aabb"))
			if !errors.Is(err, ErrInvalidRef) {
				t.Fatalf("error = %v, want ErrInvalidRef", err)
			}
		})
	}
}

// TestMatches is the truth table a caller relies on to decide "can I trust this
// file?". Only a verified digest answers true.
func TestMatches(t *testing.T) {
	payload := []byte("verified bytes")
	address := addressOf(payload)
	dir := t.TempDir()
	good := writeFile(t, dir, "good.bin", payload)
	bad := writeFile(t, dir, "bad.bin", []byte("nope"))

	m := New(Options{})
	cases := []struct {
		name    string
		path    string
		address string
		want    bool
	}{
		{"matching bytes", good, address, true},
		{"mismatching bytes", bad, address, false},
		{"missing file", filepath.Join(dir, "absent.bin"), address, false},
		{"empty path", "", address, false},
		{"empty address", good, "", false},
		{"directory", dir, address, false},
		{"upper-case address", good, fmt.Sprintf("%X", sha256.Sum256(payload)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.Matches(tc.path, tc.address); got != tc.want {
				t.Errorf("Matches(%q, %q) = %v, want %v", tc.path, tc.address, got, tc.want)
			}
		})
	}
}

// TestDigest_IsTheCanonicalAddress keeps the materializer and the rest of the
// pipeline agreeing about a file's identity.
func TestDigest_IsTheCanonicalAddress(t *testing.T) {
	payload := []byte("digest me")
	path := writeFile(t, t.TempDir(), "asset.bin", payload)

	sum, size, err := New(Options{}).Digest(path)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if sum != addressOf(payload) {
		t.Errorf("digest = %s, want %s", sum, addressOf(payload))
	}
	if size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", size, len(payload))
	}
}
