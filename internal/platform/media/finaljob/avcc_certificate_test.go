package finaljob

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	segment "github.com/Marcuss-ops/PipelineGen/internal/kernel/media/finaljob"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// The copy-only mux pins the FIRST copied source's SPS/PPS as the output
// decoder configuration and rejects every later source that differs byte for
// byte. A fragment certificate that only checks profile/level/geometry lets a
// foreign avcC through, and the rejection then lands on whichever innocent
// fragment came next. These tests pin the certificate to the bytes.
func TestFinalJobCertificateRequiresTheCanonicalDecoderConfiguration(t *testing.T) {
	probe := finalJobFragmentProbe{
		streamOK:        true,
		extradataSHA256: kernelmedia.CanonicalVideoExtradataSHA256,
		startAtZero:     true,
		durationUS:      kernelmedia.FrameDurationUS(96),
		keyframeAt:      map[int64]bool{0: true, 48: true},
		packets:         96,
	}
	if err := probe.certify(96); err != nil {
		t.Fatalf("canonical avcC must certify: %v", err)
	}

	// Every declared stream fact still matches; only the decoder
	// configuration differs. This is exactly the shape the fragment cache
	// held for 54 of the 109 fragments prepared on 2026-11-16.
	probe.extradataSHA256 = strings.Repeat("a", 64)
	err := probe.certify(96)
	if err == nil {
		t.Fatal("a foreign decoder configuration must not certify")
	}
	if !strings.Contains(err.Error(), "decoder configuration") ||
		!strings.Contains(err.Error(), kernelmedia.CanonicalVideoExtradataSHA256) {
		t.Fatalf("certificate reason = %q, want the canonical avcC named", err)
	}

	probe.extradataSHA256 = ""
	if err := probe.certify(96); err == nil {
		t.Fatal("a fragment with no decoder configuration must not certify")
	}
}

// The published reference must carry the same decoder configuration it
// certifies, and reuse must refuse a reference that does not declare it: a
// ref from an older run cannot vouch for bytes it never identified.
func TestFinalJobPreparedFragmentRefCarriesTheCanonicalDecoderConfiguration(t *testing.T) {
	request := segment.VideoSegmentRequest{
		SegmentID: "segment-0000", FrameCount: 24, TimelineRevision: 3,
		TimelineSHA256: strings.Repeat("b", 64), TimelineStartFrame: 0,
	}
	durationUS := kernelmedia.FrameDurationUS(request.FrameCount)
	ref := finalJobPreparedRef(map[string]any{"role": "clip"}, strings.Repeat("c", 64), 4096, durationUS, request)
	if ref["extradata_sha256"] != kernelmedia.CanonicalVideoExtradataSHA256 {
		t.Fatalf("prepared ref avcC = %v, want %s", ref["extradata_sha256"], kernelmedia.CanonicalVideoExtradataSHA256)
	}
	if !finalJobSourceIsExactPreparedFragment(ref, request, durationUS) {
		t.Fatal("a freshly prepared ref must be reusable as an exact fragment")
	}
	delete(ref, "extradata_sha256")
	if finalJobSourceIsExactPreparedFragment(ref, request, durationUS) {
		t.Fatal("a ref with no decoder configuration identity must not be reused")
	}
}

// The cache contract: an entry only answers for fragments that carry the
// canonical decoder configuration, so entries written before that identity
// existed (or by a build that packet-copied a foreign SPS/PPS) are misses and
// get regenerated instead of poisoning the next mux.
func TestFinalJobFragmentCacheMissesNonCanonicalEntries(t *testing.T) {
	dir := t.TempDir()
	preparer := &SegmentPreparer{cacheDir: dir}
	key := strings.Repeat("d", 64)
	payload := filepath.Join(dir, key[:2], key+".mp4")
	if err := os.MkdirAll(filepath.Dir(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("fragment"), 0o600); err != nil {
		t.Fatal(err)
	}
	frames, durationUS := int64(24), kernelmedia.FrameDurationUS(24)
	bytesSHA, _, err := digest.SHA256File(payload)
	if err != nil {
		t.Fatal(err)
	}
	write := func(entry finalJobFragmentCacheEntry) {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, key[:2], key+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := finalJobFragmentCacheEntry{
		SHA256: bytesSHA, SizeBytes: int64(len("fragment")), FrameCount: frames, DurationUS: durationUS,
		ProfileID: finalJobSegmentProfile,
	}

	// The legacy entry shape (no avcC recorded) must not be trusted even
	// though its bytes hash to the recorded identity and it names the
	// certified profile.
	write(base)
	if _, _, _, ok := preparer.cachedFragment(key, frames, durationUS); ok {
		t.Fatal("a cache entry without a decoder configuration identity must be a miss")
	}

	foreign := base
	foreign.ExtradataSHA256 = strings.Repeat("f", 64)
	write(foreign)
	if _, _, _, ok := preparer.cachedFragment(key, frames, durationUS); ok {
		t.Fatal("a cache entry with a foreign decoder configuration must be a miss")
	}
}

// Regeneration proof: the staged source that produced a foreign fragment is
// handed to the SAME encoder command production uses, and must come out
// carrying the canonical decoder configuration. That is what makes the
// invalidated cache entries regenerate into muxable bytes instead of the same
// poison the remote mux rejects.
func TestFinalJobForeignSourceRegeneratesToTheCanonicalConfiguration(t *testing.T) {
	if testing.Short() {
		t.Skip("regeneration encodes from the staged source corpus")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := FragmentCacheDir(filepath.Join("..", "..", "..", "data"))
	if dir == "" {
		t.Skip("fragment cache is disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sourcePath, sourceSHA := foreignStagedSource(t, ctx, dir)
	// The production policy, not a convenient one: the canonical avcC is the
	// output of THIS lane (libx264/veryfast, ref=1), and a different preset
	// emits different SPS/PPS for the same pictures.
	preparer := &SegmentPreparer{
		policy: kernelmedia.EncoderPolicy{
			Codec:  "libx264",
			Preset: config.CanonicalStockEncoderPreset,
			CRF:    23,
		},
		lanes: 1,
	}

	// 1. The census refuses to call these bytes copyable, which is what sends
	// the window to the encoder instead of to the silent packet copy.
	census, ok := preparer.sourceCensus(ctx, sourceSHA, sourcePath)
	if !ok {
		t.Fatalf("census of %s failed", sourcePath)
	}
	if census.certified {
		t.Fatalf("%s still certifies for packet copy although its avcC is not canonical", sourcePath)
	}

	// 2. The production encoder command turns it into a certifiable fragment.
	request := segment.VideoSegmentRequest{
		SegmentID: "regenerated-0000", FrameCount: 24, TimelineRevision: 1,
		TimelineSHA256: strings.Repeat("a", 64), TimelineStartFrame: 0,
	}
	output := filepath.Join(t.TempDir(), "regenerated.mp4")
	if err := preparer.encodeFragment(ctx, sourcePath, output, request, census.needsNormalization, 0); err != nil {
		t.Fatalf("encode %s: %v", sourcePath, err)
	}
	regenerated, err := probeFinalJobFragment(ctx, output)
	if err != nil {
		t.Fatalf("probe regenerated fragment: %v", err)
	}
	if !strings.EqualFold(regenerated.extradataSHA256, kernelmedia.CanonicalVideoExtradataSHA256) {
		t.Fatalf("regenerated avcC = %s, want the canonical %s",
			regenerated.extradataSHA256, kernelmedia.CanonicalVideoExtradataSHA256)
	}
	if err := regenerated.certify(request.FrameCount); err != nil {
		t.Fatalf("regenerated fragment does not certify: %v", err)
	}
}

// foreignStagedSource returns one staged source whose decoder configuration is
// not the canonical one, with its content hash as the census key.
func foreignStagedSource(t *testing.T, ctx context.Context, cacheDir string) (string, string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(cacheDir, "sources", "*", "*.mp4"))
	if err != nil || len(matches) == 0 {
		t.Skipf("no staged sources under %s", filepath.Join(cacheDir, "sources"))
	}
	if len(matches) > 40 {
		matches = matches[:40]
	}
	for _, path := range matches {
		probe, err := probeFinalJobFragment(ctx, path)
		if err != nil || probe.extradataSHA256 == "" {
			continue
		}
		if strings.EqualFold(probe.extradataSHA256, kernelmedia.CanonicalVideoExtradataSHA256) {
			continue
		}
		return path, strings.TrimSuffix(filepath.Base(path), ".mp4")
	}
	t.Skipf("no staged source under %s carries a foreign decoder configuration", cacheDir)
	return "", ""
}

// Corpus regression: every fragment the Producer has certified as
// VELOX_ASSEMBLY_READY_V1 must carry the one avcC the remote mux can copy. It
// runs on the real cache when it exists (the pipeline's own data directory)
// and skips otherwise, so CI without the corpus stays green without pretending
// the invariant was checked.
func TestFinalJobCertifiedFragmentsShareOneDecoderConfiguration(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus probe reads the fragment cache")
	}
	dir := FragmentCacheDir(filepath.Join("..", "..", "..", "data"))
	if dir == "" {
		t.Skip("fragment cache is disabled")
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*", "*.json"))
	if err != nil || len(entries) == 0 {
		t.Skipf("no fragment cache under %s", dir)
	}
	checked := 0
	for _, entryPath := range entries {
		raw, err := os.ReadFile(entryPath)
		if err != nil {
			continue
		}
		var entry finalJobFragmentCacheEntry
		if json.Unmarshal(raw, &entry) != nil || entry.ProfileID != finalJobSegmentProfile {
			continue
		}
		payload := strings.TrimSuffix(entryPath, ".json") + ".mp4"
		if _, err := os.Stat(payload); err != nil {
			continue
		}
		probe, err := probeFinalJobFragment(context.Background(), payload)
		if err != nil {
			t.Fatalf("%s: probe: %v", payload, err)
		}
		if !strings.EqualFold(probe.extradataSHA256, kernelmedia.CanonicalVideoExtradataSHA256) {
			t.Fatalf("%s claims %s but carries avcC %s", payload, finalJobSegmentProfile, probe.extradataSHA256)
		}
		checked++
	}
	if checked == 0 {
		t.Skip("fragment cache holds no certifiable entries")
	}
}
