package scriptgeneration

// final_audio_cache.go keeps a CERTIFIED final-audio artifact keyed by the
// audio PLAN, not by the run that produced it.
//
// Why this exists: the durable checkpoint resolver keys its decision on
// exec.JobID, so it can only ever skip work inside one job (crash → restart).
// A revision, a retry after an unrelated failure, or a second run of the same
// payload re-mixes and re-encodes the whole master even when the compiled plan
// is byte-identical. Measured on the Garlasco payload: r57 and r64 share
// audio_plan_sha256 6a1eb1d8… and final_audio_sha256 c2718881…, yet r64 paid
// 2,3 s of mix and 9,5 s of AAC for bytes it had already produced.
//
// The cache is deliberately the same shape as the final-job fragment cache:
// a directory of bytes plus a sidecar that names them. It can never certify
// itself — every hit re-hashes the file and compares it with the sidecar —
// and any missing/mismatched fact is a miss, never a guess.

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// finalAudioCacheHitPlan is the metrics outcome of a master reused for an
// identical plan. It is a value, not a log line, so the receipt can prove it.
const finalAudioCacheHitPlan = "HIT_PLAN"

// finalAudioCache is a directory of certified masters addressed by plan hash.
// A nil cache (or an empty directory) disables reuse, which is how an A/B run
// measures the cache itself.
type finalAudioCache struct {
	dir string
}

// finalAudioCacheEntry is the sidecar of a cached master: the identity of the
// bytes and the contract they were certified against. The plan hash is the
// cache key, so a plan change can never answer with older audio.
type finalAudioCacheEntry struct {
	PlanSHA256           string `json:"plan_sha256"`
	AudioContractVersion string `json:"audio_contract_version"`
	AudioPlanVersion     string `json:"audio_plan_version"`
	FinalAudioSHA256     string `json:"final_audio_sha256"`
	SizeBytes            int64  `json:"size_bytes"`
	DurationUS           int64  `json:"duration_us"`
	DurationMS           int64  `json:"duration_ms"`
	Container            string `json:"container"`
	Codec                string `json:"codec"`
	Profile              string `json:"profile"`
	SampleRate           int    `json:"sample_rate"`
	Channels             int    `json:"channels"`
	ChannelLayout        string `json:"channel_layout"`
	Bitrate              int64  `json:"bitrate"`
	StartPTS             int64  `json:"start_pts"`
}

func newFinalAudioCache(dir string) *finalAudioCache {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "", "off", "disabled", "none":
		// The kill switch is honoured at the constructor too, so no caller can
		// end up with a cache it asked to disable.
		return nil
	}
	return &finalAudioCache{dir: strings.TrimSpace(dir)}
}

func (c *finalAudioCache) enabled() bool {
	return c != nil && c.dir != ""
}

func (c *finalAudioCache) entryPath(planSHA string) string {
	return filepath.Join(c.dir, planSHA[:2], planSHA)
}

func finalAudioContainerExt(container string) string {
	switch strings.ToLower(strings.TrimSpace(container)) {
	case "mp4", "m4a", "mov":
		return ".m4a"
	case "wav":
		return ".wav"
	default:
		return ".m4a"
	}
}

// take returns a certified master for this plan, materialized as a fresh file
// in destDir so each caller owns independent bytes. The staged file is hashed
// before the hit is accepted, even if the cache changes during the copy.
func (c *finalAudioCache) take(plan audio.CompiledAudioPlan, destDir string) (FinalAudioReference, bool) {
	if !c.enabled() || len(plan.PlanSHA256) != 64 || strings.TrimSpace(destDir) == "" {
		return FinalAudioReference{}, false
	}
	base := c.entryPath(plan.PlanSHA256)
	payload, err := os.ReadFile(base + ".json")
	if err != nil {
		return FinalAudioReference{}, false
	}
	var entry finalAudioCacheEntry
	if err := json.Unmarshal(payload, &entry); err != nil {
		return FinalAudioReference{}, false
	}
	if !strings.EqualFold(entry.PlanSHA256, plan.PlanSHA256) ||
		entry.AudioContractVersion != audio.AudioContractVersion ||
		entry.AudioPlanVersion != plan.Version ||
		len(entry.FinalAudioSHA256) != 64 || entry.SizeBytes <= 0 {
		return FinalAudioReference{}, false
	}
	cached := base + finalAudioContainerExt(entry.Container)
	info, err := os.Stat(cached)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.SizeBytes {
		return FinalAudioReference{}, false
	}
	staged, sha, size, err := stageCachedFinalAudio(cached, destDir, plan.PlanSHA256, entry.Container)
	if err != nil || size != entry.SizeBytes || !strings.EqualFold(sha, entry.FinalAudioSHA256) {
		if staged != "" {
			_ = os.Remove(staged)
		}
		return FinalAudioReference{}, false
	}
	return FinalAudioReference{
		// The staged path is the run-owned identity: the renderer names its
		// artifact by its own local path, and a cache hit must not make two
		// runs share one asset id.
		AssetID:              staged,
		Path:                 staged,
		Container:            entry.Container,
		AudioContractVersion: audio.AudioContractVersion,
		AudioPlanVersion:     plan.Version,
		PlanSHA256:           plan.PlanSHA256,
		FinalAudioSHA256:     strings.ToLower(entry.FinalAudioSHA256),
		Codec:                entry.Codec,
		Profile:              entry.Profile,
		SampleRate:           entry.SampleRate,
		Channels:             entry.Channels,
		ChannelLayout:        entry.ChannelLayout,
		Bitrate:              entry.Bitrate,
		DurationUS:           entry.DurationUS,
		DurationMS:           entry.DurationMS,
		StartPTS:             entry.StartPTS,
		SizeBytes:            entry.SizeBytes,
		FinalMix:             true,
		CopyEligible:         true,
	}, true
}

// put keeps a certified master for the next run of the same plan. Failures are
// silent by design: the cache is an optimization and must never fail a render.
func (c *finalAudioCache) put(plan audio.CompiledAudioPlan, ref FinalAudioReference) {
	// Only store what the canonical gate would accept: an entry that cannot
	// pass ValidateFinalAudioReference would be a guaranteed miss, and a cache
	// full of them is indistinguishable from a broken one.
	if !c.enabled() || len(plan.PlanSHA256) != 64 || strings.TrimSpace(ref.Path) == "" ||
		len(ref.FinalAudioSHA256) != 64 || ref.SizeBytes <= 0 || ref.DurationMS <= 0 || ref.Bitrate <= 0 {
		return
	}
	base := c.entryPath(plan.PlanSHA256)
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		return
	}
	source, err := os.Open(ref.Path)
	if err != nil {
		return
	}
	defer func() { _ = source.Close() }()
	dest, err := os.CreateTemp(filepath.Dir(base), filepath.Base(base)+"-*.tmp")
	if err != nil {
		return
	}
	tmp := dest.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := dest.Close(); err != nil {
		_ = os.Remove(tmp)
		return
	}
	final := base + finalAudioContainerExt(ref.Container)
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return
	}
	payload, err := json.Marshal(finalAudioCacheEntry{
		PlanSHA256:           plan.PlanSHA256,
		AudioContractVersion: audio.AudioContractVersion,
		AudioPlanVersion:     plan.Version,
		FinalAudioSHA256:     strings.ToLower(ref.FinalAudioSHA256),
		SizeBytes:            ref.SizeBytes,
		DurationUS:           ref.DurationUS,
		DurationMS:           ref.DurationMS,
		Container:            ref.Container,
		Codec:                ref.Codec,
		Profile:              ref.Profile,
		SampleRate:           ref.SampleRate,
		Channels:             ref.Channels,
		ChannelLayout:        ref.ChannelLayout,
		Bitrate:              ref.Bitrate,
		StartPTS:             ref.StartPTS,
	})
	if err != nil {
		return
	}
	sidecar, err := os.CreateTemp(filepath.Dir(base), filepath.Base(base)+"-*.json.tmp")
	if err != nil {
		return
	}
	sidecarPath := sidecar.Name()
	defer func() { _ = os.Remove(sidecarPath) }()
	if _, err := sidecar.Write(payload); err != nil {
		_ = sidecar.Close()
		return
	}
	if err := sidecar.Close(); err != nil {
		return
	}
	_ = os.Rename(sidecarPath, base+".json")
}

// stageCachedFinalAudio materializes independent run-owned bytes under a unique
// name. A hard link would let a caller mutate the cached master or another run,
// so it prefers a copy-on-write reflink (independent inode, no byte copy) and
// falls back to a streaming copy that hashes the bytes IN THE SAME PASS. The
// previous shape copied the whole master and then re-read it just to learn the
// digest the sidecar already named.
func stageCachedFinalAudio(cached, destDir, planSHA, container string) (path, sha string, size int64, err error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", "", 0, err
	}
	placeholder, err := os.CreateTemp(destDir, "pipelinegen-final-audio-"+planSHA[:16]+"-*"+finalAudioContainerExt(container))
	if err != nil {
		return "", "", 0, err
	}
	staged := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		_ = os.Remove(staged)
		return "", "", 0, err
	}
	// Reflink first: the filesystem proves the bytes are the cached file's
	// bytes without moving them, and the result is an independent file.
	if cloneErr := cloneFile(staged, cached); cloneErr == nil {
		sha, size, err = digest.SHA256File(staged)
		if err != nil {
			_ = os.Remove(staged)
			return "", "", 0, err
		}
		return staged, sha, size, nil
	}
	source, err := os.Open(cached)
	if err != nil {
		_ = os.Remove(staged)
		return "", "", 0, err
	}
	defer func() { _ = source.Close() }()
	dest, err := os.OpenFile(staged, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = os.Remove(staged)
		return "", "", 0, err
	}
	hasher := digest.NewSHA256()
	size, copyErr := io.Copy(io.MultiWriter(dest, hasher), source)
	closeErr := dest.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(staged)
		if copyErr != nil {
			return "", "", 0, copyErr
		}
		return "", "", 0, closeErr
	}
	return staged, hex.EncodeToString(hasher.Sum(nil)), size, nil
}
