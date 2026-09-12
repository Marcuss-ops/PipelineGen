package cliprender

// bench_assets_test.go owns scenarios 4, 5 and 8 of the canonical clip.render
// benchmark — the per-clip WASTE checks, i.e. the work that must happen once
// per SOURCE, not once per clip:
//
//	4  SHA cache-hit     — N clips from one source must cost ONE full-file hash
//	8  Cold vs warm      — what a warm process saves over a cold one
//	5  Shared CAS        — the same bytes materialised once, reused everywhere
//
// These are measured on the real ContentVerifier / PreparedAssetResolver pair
// with a counting hasher and a counting downloader, so "no second read" is a
// counted fact rather than an inference.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// benchCASContent is the synthetic source payload for the asset scenarios.
var benchCASContent = []byte("benchmark source bytes — the payload that must be read exactly once per process")

// benchDownloader models the cold-cache download + materialisation into the
// shared content-addressed root. Every call is one full download and one full
// disk write; the file lands at <root>/<sha256>/source.mp4, which is the same
// address the PreparedAssetResolver checks first.
type benchDownloader struct {
	root       string
	content    []byte
	downloads  int
	bytes      int64
	diskWrites int64
}

func (d *benchDownloader) Materialize(_ context.Context, ref AssetRef) (*MaterializedAsset, error) {
	sha := digest.SHA256Bytes(d.content)
	path := filepath.Join(d.root, sha, "source.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, d.content, 0o644); err != nil {
		return nil, err
	}
	d.downloads++
	d.bytes += int64(len(d.content))
	d.diskWrites += int64(len(d.content))
	return &MaterializedAsset{
		AssetID: ref.AssetID, LocalPath: path, SHA256: sha,
		SizeBytes: int64(len(d.content)), FromCache: false,
	}, nil
}

// newBenchSourceResolver wires the real PreparedAssetResolver over a shared
// CAS root with a counting hasher and a counting downloader.
func newBenchSourceResolver(t *testing.T, root string, content []byte) (*PreparedAssetResolver, *countingHasher, *benchDownloader) {
	t.Helper()
	downloader := &benchDownloader{root: root, content: content}
	resolver, err := NewPreparedAssetResolver(root, downloader)
	if err != nil {
		t.Fatal(err)
	}
	counted := &countingHasher{}
	resolver.verifier = NewContentVerifier(counted.hash)
	return resolver, counted, downloader
}

// TestScenario4_SHACacheHit is the "source SHA-256 once" gate: 20 clips cut
// from the same source must produce exactly one full-file read. The naive
// baseline (one hash + one download per clip) is reported alongside so the
// saving is a number, not a claim.
func TestScenario4_SHACacheHit(t *testing.T) {
	const clips = 20
	root := t.TempDir()
	content := benchCASContent
	sha := digest.SHA256Bytes(content)

	resolver, counted, downloader := newBenchSourceResolver(t, root, content)

	startedAt := time.Now()
	for i := 0; i < clips; i++ {
		asset, err := resolver.Materialize(context.Background(), AssetRef{
			AssetID:       fmt.Sprintf("source-%d", i),
			MediaType:     "video",
			LegacyFileMD5: sha,
		})
		if err != nil {
			t.Fatalf("clip %d: materialize: %v", i, err)
		}
		if asset.SHA256 != sha || asset.SizeBytes != int64(len(content)) {
			t.Fatalf("clip %d: unexpected materialization %+v", i, asset)
		}
		// Clip 0 populates the cold CAS through the downloader; every later
		// clip must be served from the shared root.
		if i > 0 && !asset.FromCache {
			t.Fatalf("clip %d: expected a shared-CAS hit, got %+v", i, asset)
		}
	}
	elapsed := time.Since(startedAt)

	naiveBytes := int64(clips) * int64(len(content))
	rep := benchReport{
		Scenario:         "scenario-04-sha-cache-hit",
		Mode:             "assets",
		Clips:            clips,
		WallMS:           elapsed.Milliseconds(),
		ClipsPerMin:      benchClipsPerMin(clips, elapsed),
		SourceFullHashes: int64(counted.calls),
		SourceDownloads:  int64(downloader.downloads),
		DiskReadBytes:    int64(counted.calls) * int64(len(content)),
		DiskWriteBytes:   downloader.diskWrites,
		NetworkRXBytes:   downloader.bytes,
	}
	writeBenchReport(t, rep)

	if counted.calls != 1 {
		t.Errorf("full-file hashes across %d clips = %d, want 1", clips, counted.calls)
	}
	if downloader.downloads != 1 {
		t.Errorf("downloads across %d clips = %d, want 1", clips, downloader.downloads)
	}
	t.Logf("SHA cache-hit: %d clips → %d full read(s), %d download(s); naive would read %d bytes, measured %d bytes",
		clips, counted.calls, downloader.downloads, naiveBytes, rep.DiskReadBytes)
}

// TestScenario8_ColdVsWarmPipeline separates the two costs the spec calls out:
// a COLD process (empty memo, empty CAS) pays the download + the first hash; a
// WARM process over the same shared root pays neither. The delta is what a
// batch actually saves.
func TestScenario8_ColdVsWarmPipeline(t *testing.T) {
	const clips = 10
	root := t.TempDir()
	content := benchCASContent
	sha := digest.SHA256Bytes(content)

	// Cold process: empty verifier memo and an empty CAS root.
	coldResolver, coldHashes, coldDownloads := newBenchSourceResolver(t, root, content)
	for i := 0; i < clips; i++ {
		if _, err := coldResolver.Materialize(context.Background(), AssetRef{AssetID: "cold", MediaType: "video", LegacyFileMD5: sha}); err != nil {
			t.Fatalf("cold clip %d: %v", i, err)
		}
	}

	// Warm process: a NEW resolver over the SAME root (the process restarted,
	// the CAS did not).
	warmResolver, warmHashes, warmDownloads := newBenchSourceResolver(t, root, content)
	warmStart := time.Now()
	for i := 0; i < clips; i++ {
		if _, err := warmResolver.Materialize(context.Background(), AssetRef{AssetID: "warm", MediaType: "video", LegacyFileMD5: sha}); err != nil {
			t.Fatalf("warm clip %d: %v", i, err)
		}
	}
	warmElapsed := time.Since(warmStart)

	rep := benchReport{
		Scenario:         "scenario-08-cold-vs-warm",
		Mode:             "assets",
		Clips:            clips,
		WallMS:           warmElapsed.Milliseconds(),
		SourceFullHashes: int64(warmHashes.calls),
		SourceDownloads:  int64(warmDownloads.downloads),
		DiskReadBytes:    int64(warmHashes.calls) * int64(len(content)),
	}
	writeBenchReport(t, rep)

	// Cold: the first clip downloads + writes, every clip hits the memo, so
	// exactly one hash and one download.
	if coldDownloads.downloads != 1 {
		t.Errorf("cold downloads = %d, want 1 (the source is fetched once)", coldDownloads.downloads)
	}
	if coldHashes.calls != 1 {
		t.Errorf("cold hashes = %d, want 1", coldHashes.calls)
	}
	// Warm: the CAS root already has the bytes, so the downloader is never
	// invoked; the new process still verifies once (an honest once-per-process
	// hash rather than trusting a path with no in-process evidence).
	if warmDownloads.downloads != 0 {
		t.Errorf("warm downloads = %d, want 0 (the shared CAS already holds the bytes)", warmDownloads.downloads)
	}
	if warmHashes.calls != 1 {
		t.Errorf("warm hashes = %d, want exactly 1 (one verification per process)", warmHashes.calls)
	}
	t.Logf("cold vs warm (%d clips): cold downloads=%d hashes=%d | warm downloads=%d hashes=%d",
		clips, coldDownloads.downloads, coldHashes.calls, warmDownloads.downloads, warmHashes.calls)
}

// TestScenario5_SharedCAS proves the materialisation root is shared: two
// independent resolver instances (two "processes") over one root materialise
// the source bytes ONCE in total. Batch two is a pure cache hit — zero
// downloads, zero disk writes, zero network bytes.
func TestScenario5_SharedCAS(t *testing.T) {
	const clips = 10
	root := t.TempDir()
	content := benchCASContent
	sha := digest.SHA256Bytes(content)

	// Batch 1: cold CAS.
	batch1, hashes1, downloads1 := newBenchSourceResolver(t, root, content)
	for i := 0; i < clips; i++ {
		if _, err := batch1.Materialize(context.Background(), AssetRef{AssetID: fmt.Sprintf("b1-%d", i), MediaType: "video", LegacyFileMD5: sha}); err != nil {
			t.Fatalf("batch1 clip %d: %v", i, err)
		}
	}

	// Batch 2: same root, fresh process.
	batch2, hashes2, downloads2 := newBenchSourceResolver(t, root, content)
	for i := 0; i < clips; i++ {
		if _, err := batch2.Materialize(context.Background(), AssetRef{AssetID: fmt.Sprintf("b2-%d", i), MediaType: "video", LegacyFileMD5: sha}); err != nil {
			t.Fatalf("batch2 clip %d: %v", i, err)
		}
	}

	totalDownloads := downloads1.downloads + downloads2.downloads
	totalWrites := downloads1.diskWrites + downloads2.diskWrites
	rep := benchReport{
		Scenario:         "scenario-05-shared-cas",
		Mode:             "assets",
		Clips:            2 * clips,
		SourceFullHashes: int64(hashes1.calls + hashes2.calls),
		SourceDownloads:  int64(totalDownloads),
		DiskReadBytes:    int64(hashes1.calls+hashes2.calls) * int64(len(content)),
		DiskWriteBytes:   totalWrites,
		NetworkRXBytes:   downloads1.bytes + downloads2.bytes,
	}
	writeBenchReport(t, rep)

	if totalDownloads != 1 {
		t.Errorf("shared CAS: %d downloads across 2 batches of %d clips, want 1", totalDownloads, clips)
	}
	if totalWrites != int64(len(content)) {
		t.Errorf("shared CAS: %d bytes written for the same source, want %d (materialised once)", totalWrites, len(content))
	}
	if downloads2.downloads != 0 {
		t.Errorf("shared CAS: the second batch re-downloaded the source (%d downloads)", downloads2.downloads)
	}
	t.Logf("shared CAS: 2 batches × %d clips → %d download(s), %d bytes written, %d verification(s)",
		clips, totalDownloads, totalWrites, hashes1.calls+hashes2.calls)
}
