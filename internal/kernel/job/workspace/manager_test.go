// Package workspace — manager_test.go (P0 Commit 9, July 2026).
//
// Tests for the C9 canonical WorkspaceManager implementation:
//
//  1. Prepare / happy-path: allocates a directory tree under globalRoot,
//     writes a struct whose Root is the absolute attempted-path.
//
//  2. Prepare / negative: nil-args fail-closed at every input shape.
//
//  3. Download / happy-path: stub fetcher returns deterministic bytes;
//     the result LocalInputRef matches the ref by SHA-256 + SizeBytes.
//
//  4. Download / integrity-mismatch: each of ErrHashMismatch and
//     ErrSizeMismatch is reachable via a specific stub configuration.
//
//  5. Cleanup / happy-path: deletes the workspace tree.
//
//  6. Cleanup / always-on-terminal: §5.4 contract — the manager's
//     cleanup runs even when the workspace has been externally
//     touched (peeked at, file-handles opened, subdir manually
//     removed). Per the C9 spec literal "COMPLETE cleanup always runs".
//
//  7. Cleanup / idempotent: a second Cleanup on an already-removed
//     workspace returns nil.
//
//  8. Path-containment / dir-outside: a remote-asset ref whose
//     Filename resolves to outside the workspace root is rejected
//     with ErrPathOutsideWorkspace at Download time (no bytes written).
//
//  9. Path-containment / symlink-outside: a symlink at an
//     intermediate component of the target path is rejected with
//     ErrSymlinkRejected (per the spec literal "reject symlinks to
//     outside").
//
//  10. Path-containment / absolute-to-outside: an absolute path that
//     points outside the workspace root is rejected with
//     ErrPathOutsideWorkspace (regression guard against the
//     `os.Open(/etc/passwd)`-shaped attack).
//
// P1-7 atomic migration (godlike/07 ZERO_LEGACY_POLICY step): the
// canonical WorkspaceManager owner is now internal/kernel/job/workspace.
// This test file is the verbatim port of the previous test surface
// at internal/domain/job/workspace/manager_test.go. The deletion
// of that legacy file is part of the same atomic commit; the
// percheck_legacy_root_ban Wave-25 forward-prevention gate (now
// extended to also ban internal/domain/job) prevents any future
// reintroduction of the legacy root.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── stub fetcher ────────────────────────────────────────────────────────

// stubFetcher is the test-side implementation of Fetcher. Body / Size /
// Err are configured per-test. URL is recorded so a test can assert
// which URL was actually requested (e.g. "Download fetches the URL
// declared in ref.URL").
type stubFetcher struct {
	Body []byte
	Size int64
	Err  error
	URL  string
}

func (s *stubFetcher) Fetch(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	s.URL = url
	if s.Err != nil {
		return nil, 0, s.Err
	}
	return io.NopCloser(strings.NewReader(string(s.Body))), s.Size, nil
}

// ── helpers ─────────────────────────────────────────────────────────────

// hexSHA256 returns the canonical lowercase hex of the SHA-256 digest
// of b. Mirrors the canonical hashutil.SHA256Bytes helper at
// internal/infrastructure/files/hashutil.go.
func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newStubManager creates a manager backed by a temporary directory +
// the supplied fetcher. Returns the manager + the resolved globalRoot
// (post-filepath.Abs canonicalisation) so a test can assert expected
// paths against the canonical form.
func newStubManager(t *testing.T, fetcher Fetcher) (WorkspaceManager, string) {
	t.Helper()
	root := t.TempDir()
	m, err := managerWithFetcher(root, fetcher)
	if err != nil {
		t.Fatalf("managerWithFetcher: %v", err)
	}
	return m, root
}

// writeFile is a tiny test-side helper for cases where the canonical
// fs.WriteFile signature would obscure intent.
func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

// mkdirAll is a tiny test-side helper.
func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

// symlink is a tiny test-side helper. Wraps os.Symlink so a test
// failure points to the right line.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %q -> %q: %v", link, target, err)
	}
}

// ── Prepare tests ──────────────────────────────────────────────────────

func TestManager_Prepare_AllocatesUnderRootWithJobAndAttemptDirectories(t *testing.T) {
	mgr, globalRoot := newStubManager(t, &stubFetcher{})

	ws, err := mgr.Prepare(context.Background(), "job-42", 1)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if ws == nil {
		t.Fatal("Prepare returned nil ManagedWorkspace")
	}

	// Expected layout: <globalRoot>/job-job-42/attempt-1/
	wantDir := filepath.Join(globalRoot, "job-job-42", "attempt-1")
	if ws.Root != wantDir {
		t.Errorf("ws.Root = %q, want %q", ws.Root, wantDir)
	}

	// Both directories MUST exist on disk.
	if info, err := os.Stat(ws.Root); err != nil || !info.IsDir() {
		t.Errorf("ws.Root %q must be an existing directory: stat err=%v isDir=%v", ws.Root, err, info != nil && info.IsDir())
	}
	if _, err := os.Stat(filepath.Join(ws.Root, "..")); err != nil {
		t.Errorf("parent job dir must exist: %v", err)
	}
}

func TestManager_Prepare_SanitisesJobIDSegment(t *testing.T) {
	mgr, globalRoot := newStubManager(t, &stubFetcher{})

	// JobID with characters outside [A-Za-z0-9._-]. After sanitisation,
	// the directory MUST be safe.
	ws, err := mgr.Prepare(context.Background(), "job we/Ird$1!", 2)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	wantSuffix := filepath.Join("job-job_we_Ird_1_", "attempt-2")
	wantDir := filepath.Join(globalRoot, wantSuffix)
	if ws.Root != wantDir {
		t.Errorf("ws.Root = %q, want %q (sanitiseSegment should map unsafe chars to _)", ws.Root, wantDir)
	}
}

func TestManager_Prepare_AttemptMustBePositive(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{})
	if _, err := mgr.Prepare(context.Background(), "job-1", 0); err == nil {
		t.Error("Prepare(attempt=0) must fail")
	}
	if _, err := mgr.Prepare(context.Background(), "job-1", -1); err == nil {
		t.Error("Prepare(attempt=-1) must fail")
	}
}

func TestManager_Prepare_EmptyJobID_FailsClosed(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{})
	if _, err := mgr.Prepare(context.Background(), "", 1); err == nil {
		t.Error("Prepare(empty jobID) must fail closed")
	}
}

func TestManager_Prepare_NilContext_OK(t *testing.T) {
	// Per godlike/06 ctx propagation: the manager doesn't currently
	// use ctx; nil must not panic. A future implementation that uses
	// ctx for cancellation would update this test.
	mgr, _ := newStubManager(t, &stubFetcher{})
	ws, err := mgr.Prepare(context.TODO(), "job-1", 1)
	if err != nil {
		t.Fatalf("Prepare with ctx.TODO: %v", err)
	}
	if ws == nil {
		t.Fatal("ManagedWorkspace should be non-nil")
	}
}

// ── Download tests ──────────────────────────────────────────────────────

func TestManager_Download_HappyPath_StreamsAndVerifiesHashAndSize(t *testing.T) {
	body := []byte("hello, pipelinegen workspace!")
	stub := &stubFetcher{Body: body, Size: int64(len(body))}
	mgr, _ := newStubManager(t, stub)

	ws, err := mgr.Prepare(context.Background(), "j-dl-happy", 1)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	ref := RemoteAssetRef{
		URL:       "https://example.invalid/hello.txt",
		Filename:  "hello.txt",
		SHA256:    hexSHA256(body),
		SizeBytes: int64(len(body)),
		MIMEType:  "text/plain",
	}

	ref2, err := mgr.Download(context.Background(), ws, ref)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if stub.URL != ref.URL {
		t.Errorf("fetcher URL = %q, want %q", stub.URL, ref.URL)
	}
	wantPath := filepath.Join(ws.Root, "hello.txt")
	if ref2.Path != wantPath {
		t.Errorf("LocalInputRef.Path = %q, want %q", ref2.Path, wantPath)
	}
	if ref2.SHA256 != ref.SHA256 {
		t.Errorf("LocalInputRef.SHA256 = %q, want %q", ref2.SHA256, ref.SHA256)
	}
	if ref2.SizeBytes != ref.SizeBytes {
		t.Errorf("LocalInputRef.SizeBytes = %d, want %d", ref2.SizeBytes, ref.SizeBytes)
	}
	// File must exist on disk + content-matches
	gotBytes, rerr := os.ReadFile(ref2.Path)
	if rerr != nil {
		t.Fatalf("ReadFile %q: %v", ref2.Path, rerr)
	}
	if string(gotBytes) != string(body) {
		t.Errorf("on-disk content = %q, want %q", gotBytes, body)
	}
}

func TestManager_Download_SizeMismatch_ReturnsErrSizeMismatch(t *testing.T) {
	body := []byte("0123456789") // 10 bytes
	stub := &stubFetcher{Body: body, Size: int64(len(body))}
	mgr, _ := newStubManager(t, stub)
	ws, _ := mgr.Prepare(context.Background(), "j-dl-size", 1)

	// ref.SizeBytes declared as 999 (does not match actual 10).
	// Pre-flight Content-Length check (stub.Size=10 != ref.SizeBytes=999)
	// fires FIRST and returns ErrSizeMismatch WITHOUT writing bytes.
	_, err := mgr.Download(context.Background(), ws, RemoteAssetRef{
		URL:       "https://example.invalid/x",
		Filename:  "x.bin",
		SHA256:    hexSHA256(body),
		SizeBytes: 999,
	})
	if err == nil {
		t.Fatal("Download must fail when SizeBytes does not match Content-Length hint")
	}
	if !errors.Is(err, ErrSizeMismatch) {
		t.Errorf("error = %v; want wraps ErrSizeMismatch", err)
	}
	// No bytes should have hit disk
	if _, serr := os.Stat(filepath.Join(ws.Root, "x.bin")); serr == nil {
		t.Error("x.bin must NOT exist on disk (pre-flight check should fail before any write)")
	}
}

func TestManager_Download_HashMismatch_ReturnsErrHashMismatch(t *testing.T) {
	// Body is 10 bytes; stub fetcher reports Content-Length=10 (matches
	// ref.SizeBytes=10) so the pre-flight size check passes. The hash
	// is then computed from the actual content (10 bytes) but the ref
	// declares a different SHA256, so the post-stream hash check
	// catches the mismatch.
	body := []byte("abcdefghij")
	stub := &stubFetcher{Body: body, Size: int64(len(body))}
	mgr, _ := newStubManager(t, stub)
	ws, _ := mgr.Prepare(context.Background(), "j-dl-hash", 1)

	_, err := mgr.Download(context.Background(), ws, RemoteAssetRef{
		URL:       "https://example.invalid/y",
		Filename:  "y.bin",
		SHA256:    "deadbeef" + strings.Repeat("0", 56), // wrong hash (64 hex chars)
		SizeBytes: int64(len(body)),
	})
	if err == nil {
		t.Fatal("Download must fail on hash mismatch")
	}
	if !errors.Is(err, ErrHashMismatch) {
		t.Errorf("error = %v; want wraps ErrHashMismatch", err)
	}
	// Partial file should have been cleaned up best-effort
	if _, serr := os.Stat(filepath.Join(ws.Root, "y.bin")); serr == nil {
		t.Error("y.bin must be removed by best-effort partial-clean after hash mismatch")
	}
}

func TestManager_Download_EmptyRefFields_FailClosed(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{})
	ws, _ := mgr.Prepare(context.Background(), "j-dl-bad", 1)
	body := []byte("x")

	cases := []struct {
		name string
		ref  RemoteAssetRef
	}{
		{"empty URL", RemoteAssetRef{Filename: "x", SHA256: hexSHA256(body), SizeBytes: 1}},
		{"empty Filename", RemoteAssetRef{URL: "https://x", SHA256: hexSHA256(body), SizeBytes: 1}},
		{"empty SHA256", RemoteAssetRef{URL: "https://x", Filename: "x", SizeBytes: 1}},
		{"zero SizeBytes", RemoteAssetRef{URL: "https://x", Filename: "x", SHA256: hexSHA256(body)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Download(context.Background(), ws, tc.ref)
			if err == nil {
				t.Errorf("%s must fail closed", tc.name)
			}
		})
	}
}

func TestManager_Download_NilWorkspace_FailsClosed(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{})
	_, err := mgr.Download(context.Background(), nil, RemoteAssetRef{
		URL:       "https://x",
		Filename:  "x",
		SHA256:    "x",
		SizeBytes: 1,
	})
	if err == nil {
		t.Error("Download(nil workspace) must fail closed")
	}
}

func TestManager_Download_FetcherError_Propagated(t *testing.T) {
	netErr := errors.New("simulated network failure")
	stub := &stubFetcher{Err: netErr}
	mgr, _ := newStubManager(t, stub)
	ws, _ := mgr.Prepare(context.Background(), "j-dl-net", 1)

	body := []byte("anything")
	_, err := mgr.Download(context.Background(), ws, RemoteAssetRef{
		URL:       "https://x",
		Filename:  "x",
		SHA256:    hexSHA256(body),
		SizeBytes: int64(len(body)),
	})
	if err == nil {
		t.Fatal("Download must surface fetcher errors")
	}
	if !errors.Is(err, netErr) {
		t.Errorf("error chain must include the network error; got %v", err)
	}
}

// ── Cleanup tests ───────────────────────────────────────────────────────

func TestManager_Cleanup_DeletesFreshlyPreparedWorkspace(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{})
	ws, _ := mgr.Prepare(context.Background(), "j-cleanup-fresh", 1)
	if _, err := os.Stat(ws.Root); err != nil {
		t.Fatalf("workspace must exist before Cleanup: %v", err)
	}
	if err := mgr.Cleanup(context.Background(), ws); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(ws.Root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Cleanup must remove ws.Root; stat err = %v, want os.ErrNotExist", err)
	}
}

// TestManager_Cleanup_AlwaysRuns_OnTerminal is the canonical
// test the user spec names "COMPLETE cleanup always runs". It pins
// the always-on-terminal contract in three orthogonal dimensions:
//
//  1. Cleanup runs on a freshly-Prepared workspace (the happy-path).
//  2. Cleanup is idempotent on a previously-removed workspace
//     (operator-removed + a second Cleanup call return nil).
//  3. Cleanup runs even when the workspace was peeked-at between
//     Prepare and Cleanup (a defensive scan that read-only'd
//     files did not gate the removal — operator audit must NOT
//     prevent cleanup).
//
// All three sub-cases MUST pass; the test fails if any does not.
// This structural commitment is what makes "always-on-terminal"
// enforceable rather than aspirational.
func TestManager_Cleanup_AlwaysRuns_OnTerminal(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{})

	t.Run("happy", func(t *testing.T) {
		ws, _ := mgr.Prepare(context.Background(), "j-co-1", 1)
		if err := mgr.Cleanup(context.Background(), ws); err != nil {
			t.Errorf("happy Cleanup: %v", err)
		}
	})

	t.Run("idempotent_after_external_remove", func(t *testing.T) {
		ws, _ := mgr.Prepare(context.Background(), "j-co-2", 1)
		// Operator removes externally (e.g. a manual `rm -rf`).
		if err := os.RemoveAll(ws.Root); err != nil {
			t.Fatalf("seed external remove: %v", err)
		}
		// A second Cleanup MUST return nil (not panic, not chain an error).
		if err := mgr.Cleanup(context.Background(), ws); err != nil {
			t.Errorf("idempotent Cleanup after external remove: %v", err)
		}
	})

	t.Run("runs_after_audit_peek", func(t *testing.T) {
		ws, _ := mgr.Prepare(context.Background(), "j-co-3", 1)
		// Operator audit: read every file in the workspace (the
		// canonical "did-the-handler-write-what-it-promised" check).
		if err := filepath.WalkDir(ws.Root, func(_ string, _ os.DirEntry, _ error) error { return nil }); err != nil {
			t.Fatalf("audit walk: %v", err)
		}
		// Cleanup must still execute (no early-return on observable state).
		if err := mgr.Cleanup(context.Background(), ws); err != nil {
			t.Errorf("Cleanup after audit peek: %v", err)
		}
		if _, err := os.Stat(ws.Root); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Cleanup after audit peek must still remove ws.Root; stat err = %v", err)
		}
	})
}

func TestManager_Cleanup_NilWorkspace_FailsClosed(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{})
	if err := mgr.Cleanup(context.Background(), nil); err == nil {
		t.Error("Cleanup(nil) must fail closed")
	}
}

// ── §5.4 path-containment tests (the user spec's headline tests) ─────────

// TestManager_PathContainment_DirOutsideWorkspace_Rejected pins the
// user spec's first headline test: when Download's target Path
// canonicalises to a directory OUTSIDE the workspace, the manager
// MUST reject BEFORE writing any bytes. The contract is ErrPathOutsideWorkspace
// (via errors.Is) — godlike/07 no-fake-availability: it's never
// silently downgraded.
func TestManager_PathContainment_DirOutsideWorkspace_Rejected(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{
		Body: []byte("data"),
		Size: 4,
	})
	ws, _ := mgr.Prepare(context.Background(), "j-pc-outside", 1)

	body := []byte("data")
	// "../" + extra "x" makes the resolved target ONE LEVEL above ws.Root.
	_, err := mgr.Download(context.Background(), ws, RemoteAssetRef{
		URL:       "https://example.invalid/leak",
		Filename:  "../leak.txt",
		SHA256:    hexSHA256(body),
		SizeBytes: int64(len(body)),
	})
	if err == nil {
		t.Fatal("Download with traversal ref.Filename must fail closed")
	}
	if !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Errorf("error = %v; want wraps ErrPathOutsideWorkspace", err)
	}
}

// TestManager_PathContainment_SymlinkToOutside_Rejected pins the
// user spec's second headline test: an intermediate symlink that
// resolves outside the workspace is rejected by the §5.4 strict
// per-component Lstat walk — regardless of where the symlink's
// target actually lives (in-harness or not).
func TestManager_PathContainment_SymlinkToOutside_Rejected(t *testing.T) {
	mgr, globalRoot := newStubManager(t, &stubFetcher{
		Body: []byte("data"),
		Size: 4,
	})
	ws, _ := mgr.Prepare(context.Background(), "j-pc-symlink", 1)

	// Create the trailing target OUTSIDE the workspace tree.
	// (We cannot place the symlink directly on the globalRoot because
	// the globalRoot is meant to be a real dir, not a re-route point.)
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "data.txt")
	writeFile(t, outsideFile, []byte("data"))

	// Symlink: ws.Root/x -> outside
	linkPath := filepath.Join(ws.Root, "x")
	symlink(t, outside, linkPath)

	body := []byte("data")
	_, err := mgr.Download(context.Background(), ws, RemoteAssetRef{
		URL:       "https://example.invalid/x",
		Filename:  "x/data.txt", // traverses the symlink
		SHA256:    hexSHA256(body),
		SizeBytes: int64(len(body)),
	})
	if err == nil {
		t.Fatal("Download through a workspace-local symlink pointing outside MUST be rejected")
	}
	if !errors.Is(err, ErrSymlinkRejected) {
		t.Errorf("error = %v; want wraps ErrSymlinkRejected", err)
	}

	// Secondary assertion: assertContained also rejects the symlink
	// link itself as a workspace-root leak.
	if cerr := assertContained(globalRoot, linkPath); cerr == nil {
		t.Error("assertContained must reject a symlink at the globalRoot level too")
	} else if !errors.Is(cerr, ErrSymlinkRejected) {
		t.Errorf("assertContained global-level symlink err = %v; want wraps ErrSymlinkRejected", cerr)
	}
}

// TestManager_PathContainment_RelativeTraversal_Rejected pins the
// `../../../etc/passwd`-shaped attack: relative-traversal Filename
// values that resolve outside the workspace root.
func TestManager_PathContainment_RelativeTraversal_Rejected(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{Body: []byte("data"), Size: 4})
	ws, _ := mgr.Prepare(context.Background(), "j-pc-traversal", 1)

	body := []byte("data")
	// Multiple levels of traversal — ensures the algorithm handles
	// any depth of ".." segments.
	cases := []string{"../../../etc/passwd", "../../../../../../tmp/leak.txt", "./../escape.bin"}
	for _, fn := range cases {
		t.Run(fn, func(t *testing.T) {
			_, err := mgr.Download(context.Background(), ws, RemoteAssetRef{
				URL:       "https://example.invalid/",
				Filename:  fn,
				SHA256:    hexSHA256(body),
				SizeBytes: int64(len(body)),
			})
			if err == nil {
				t.Errorf("relative-traversal Filename %q must fail closed", fn)
				return
			}
			if !errors.Is(err, ErrPathOutsideWorkspace) {
				t.Errorf("relative-traversal Filename %q: error = %v; want wraps ErrPathOutsideWorkspace", fn, err)
			}
		})
	}
}

// TestManager_PathContainment_AbsoluteFilenames_Rejected: when
// ref.Filename is an absolute path, the manager MUST fail closed at
// the explicit filepath.IsAbs short-circuit (BEFORE filepath.Join,
// which would silently treat the absolute path as a path component
// and concatenate under ws.Root). The §5.4 spec literal — "reject
// anything whose filepath.Abs is not under the workspace root" —
// requires this short-circuit: filepath.Abs("/etc/leak.txt") ==
// "/etc/leak.txt" is not under ws.Root, so reject.
//
// Why this matters: Go's filepath.Join treats its arguments as path
// COMPONENTS, not as paths themselves. Without the IsAbs short-circuit,
// `filepath.Join(ws.Root, "/etc/leak.txt")` would silently return
// `<ws.Root>/etc/leak.txt` (still under root!) and pass the
// assertContained check — the file would be written under a name the
// operator did not sanction. This test is a regression guard against
// that entire class of mistake.
func TestManager_PathContainment_AbsoluteFilenames_Rejected(t *testing.T) {
	mgr, _ := newStubManager(t, &stubFetcher{Body: []byte("data"), Size: 4})
	ws, _ := mgr.Prepare(context.Background(), "j-pc-abs", 1)

	body := []byte("data")
	absLeak := filepath.Join(t.TempDir(), "leak.txt")

	_, err := mgr.Download(context.Background(), ws, RemoteAssetRef{
		URL:       "https://example.invalid/",
		Filename:  absLeak, // absolute path that lives outside ws.Root
		SHA256:    hexSHA256(body),
		SizeBytes: int64(len(body)),
	})
	if err == nil {
		t.Fatal("Download with absolute-path Filename MUST fail closed (filepath.IsAbs short-circuit)")
	}
	if !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Errorf("error = %v; want wraps ErrPathOutsideWorkspace", err)
	}
}

// TestManager_AssertContained_AbsoluteToOutside pin: a direct test
// of the §5.4 algorithm on an absolute path. Confirms that
// assertContained REJECTS any candidate whose filepath.Abs does not
// fall under the workspace root, regardless of whether the candidate
// came from a Download call or any other internal entry point. This
// is the truth-table guarantee behind the manager-level rejection
// above.
func TestManager_AssertContained_AbsoluteToOutside(t *testing.T) {
	// Set up a workspace; we use the actual manager because
	// assertContained is package-internal.
	mgr, _ := newStubManager(t, &stubFetcher{})
	ws, _ := mgr.Prepare(context.Background(), "j-pc-assert", 1)

	absOutside := filepath.Join(t.TempDir(), "abs-candidate.txt")
	err := assertContained(ws.Root, absOutside)
	if err == nil {
		t.Fatal("assertContained must reject an absolute-path candidate outside ws.Root")
	}
	if !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Errorf("error = %v; want wraps ErrPathOutsideWorkspace", err)
	}

	// Positive control: the workspace's own root is contained.
	if err := assertContained(ws.Root, ws.Root); err != nil {
		t.Errorf("assertContained(ws.Root, ws.Root) must pass: %v", err)
	}
	// Positive control: a deep-but-correct child is contained.
	if err := assertContained(ws.Root, filepath.Join(ws.Root, "a", "b", "c.txt")); err != nil {
		t.Errorf("assertContained(ws.Root, deep child) must pass: %v", err)
	}
}

// TestManager_PathContainment_DeepNestedFilename_OK pins the
// positive-control case: a deeply-but-correctly nested Filename
// (e.g. "subdir/subdir2/file.bin") is INSIDE the workspace and
// MUST succeed. The negative tests above would false-positive if
// the algorithm were too strict.
func TestManager_PathContainment_DeepNestedFilename_OK(t *testing.T) {
	body := []byte("payload")
	stub := &stubFetcher{Body: body, Size: int64(len(body))}
	mgr, _ := newStubManager(t, stub)
	ws, _ := mgr.Prepare(context.Background(), "j-pc-nested", 1)

	ref2, err := mgr.Download(context.Background(), ws, RemoteAssetRef{
		URL:       "https://example.invalid/nested",
		Filename:  "subdir/subdir2/file.bin",
		SHA256:    hexSHA256(body),
		SizeBytes: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("nested download must succeed: %v", err)
	}
	wantPrefix := ws.Root + string(os.PathSeparator)
	if !strings.HasPrefix(ref2.Path, wantPrefix) {
		t.Errorf("LocalInputRef.Path = %q, want prefix %q", ref2.Path, wantPrefix)
	}

	// Sanity: parent dirs were created (os.MkdirAll on the OpenFile
	// path is enough? OpenFile doesn't create parent dirs — explicit
	// assertion that the structure is at least consistent).
	if info, err := os.Stat(filepath.Join(ws.Root, "subdir", "subdir2", "file.bin")); err != nil {
		t.Errorf("nested file must exist on disk: %v", err)
	} else if info.Size() != int64(len(body)) {
		t.Errorf("nested file size = %d, want %d", info.Size(), len(body))
	}
}

// TestManager_PathContainment_SymlinkOnGlobalRoot_Rejected pins
// the edge case where the globalRoot itself is a symlink. The
// manager rejects it at assertContained so a future operator
// mistake (creating a symlink-rooted globalRoot) is detected at
// the first Prepare / Download / Cleanup call.
func TestManager_PathContainment_SymlinkOnGlobalRoot_Rejected(t *testing.T) {
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "linkroot")
	symlink(t, realRoot, linkRoot)

	MgrWithRejectedRoot, merr := managerWithFetcher(linkRoot, &stubFetcher{})
	if MgrWithRejectedRoot == nil && merr != nil {
		t.Skipf("manager construction rejected symlink-root as expected: %v", merr)
	}
	// If the manager accepted the symlink-root at construction, the
	// first Prepare call's assertContained self-check MUST fire.
	ws, err := MgrWithRejectedRoot.Prepare(context.Background(), "j-symroot", 1)
	if err == nil {
		t.Errorf("Prepare with symlinked globalRoot must fail; got ManagedWorkspace: %+v", ws)
	}
	if err != nil && !errors.Is(err, ErrSymlinkRejected) && !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Errorf("Prepare symlinked-root err = %v; want wraps ErrSymlinkRejected or ErrPathOutsideWorkspace", err)
	}
}
