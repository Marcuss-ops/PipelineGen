package atomicwrite

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestWriteFileCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content=%q, want %q", got, "hello")
	}
	assertNoTempFiles(t, dir)
}

func TestWriteFileOverwritesReplacingContentEntirely(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := WriteFile(path, []byte("a much longer original body"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteFile(path, []byte("short"), 0o644); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "short" {
		t.Fatalf("content=%q, want %q (a truncating write must not leave the tail)", got, "short")
	}
	assertNoTempFiles(t, dir)
}

func TestWriteFilePreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte("old"), 0o700); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode=%#o, want 0700 (existing mode must be preserved)", info.Mode().Perm())
	}
}

func TestWriteFileUsesPermForNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	if err := WriteFile(path, []byte("x"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%#o, want 0640", info.Mode().Perm())
	}
}

func TestWriteFailureLeavesDestinationUntouchedAndRemovesStaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sentinel := errors.New("boom")
	err := Write(path, 0o644, func(w io.Writer) error {
		_, _ = w.Write([]byte("partial garbage that must never land"))
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v, want %v", err, sentinel)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "keep me" {
		t.Fatalf("content=%q, want %q (a failed write must not touch the target)", got, "keep me")
	}
	assertNoTempFiles(t, dir)
}

func TestWriteFailureWithNoExistingDestinationCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "never.txt")

	err := Write(path, 0o644, func(io.Writer) error { return errors.New("nope") })
	if err == nil {
		t.Fatal("want error")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat err=%v, want not-exist", statErr)
	}
	assertNoTempFiles(t, dir)
}

func TestStageCommitPublishes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.txt")

	tmp, err := Stage(path, 0o644, func(w io.Writer) error {
		_, werr := w.Write([]byte("staged"))
		return werr
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target must not exist before Commit, stat err=%v", statErr)
	}
	if err := Commit(tmp, path); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "staged" {
		t.Fatalf("content=%q, want %q", got, "staged")
	}
	assertNoTempFiles(t, dir)
}

func TestDiscardRemovesStagedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	tmp, err := Stage(path, 0o644, func(w io.Writer) error {
		_, werr := w.Write([]byte("y"))
		return werr
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := Discard(tmp); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, statErr := os.Stat(tmp); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("staged file still present: %v", statErr)
	}
	// Discard is idempotent and tolerates the empty path.
	if err := Discard(tmp); err != nil {
		t.Fatalf("second Discard: %v", err)
	}
	if err := Discard(""); err != nil {
		t.Fatalf("empty Discard: %v", err)
	}
}

func TestWriteFileMissingDirectoryFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "out.txt")
	if err := WriteFile(path, []byte("x"), 0o644); err == nil {
		t.Fatal("want error for a missing parent directory")
	}
}

func TestWriteFileEmptyData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("size=%d, want 0", info.Size())
	}
}

// TestConcurrentReaderNeverObservesPartialContent is the payload of the whole
// package: a reader racing a writer must never see a truncated file. The
// payloads use different sizes so a torn read would be visible as a
// half-written buffer rather than a valid prefix of the same length.
func TestConcurrentReaderNeverObservesPartialContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "raced.txt")

	payloadA := []byte(strings.Repeat("A", 1<<16))
	payloadB := []byte(strings.Repeat("B", 1<<16))
	if err := WriteFile(path, payloadA, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readErr error
	var readErrMu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			got, err := os.ReadFile(path)
			if err != nil {
				readErrMu.Lock()
				readErr = err
				readErrMu.Unlock()
				return
			}
			if !bytes.Equal(got, payloadA) && !bytes.Equal(got, payloadB) {
				readErrMu.Lock()
				readErr = errors.New("reader observed partial content: len=" + strconv.Itoa(len(got)))
				readErrMu.Unlock()
				return
			}
		}
	}()

	for i := 0; i < 200; i++ {
		payload := payloadA
		if i%2 == 1 {
			payload = payloadB
		}
		if err := WriteFile(path, payload, 0o644); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("write %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
	if readErr != nil {
		t.Fatal(readErr)
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("staging file %q left behind in %s", e.Name(), dir)
		}
	}
}
