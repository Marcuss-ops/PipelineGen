package audio

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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// CacheHitPlan is the metrics outcome of a master reused for an
// identical plan. It is a value, not a log line, so the receipt can prove it.
const CacheHitPlan = "HIT_PLAN"

// MaxBytesEnv caps the on-disk size of the plan-keyed master cache. A value
// <= 0 disables the bound (the historical behaviour); the default is
// DefaultMaxBytes.
const MaxBytesEnv = "PIPELINEGEN_FINAL_AUDIO_CACHE_MAX_BYTES"

// DefaultMaxBytes is the default cap: 4 GiB. A certified AAC master is a few
// megabytes, so this holds well over a thousand plans while guaranteeing that
// an eval loop cannot fill the data disk with revisions nobody will reuse.
const DefaultMaxBytes int64 = 4 << 30

// FinalAudioCache is a directory of certified masters addressed by plan hash.
// A nil cache (or an empty directory) disables reuse, which is how an A/B run
// measures the cache itself.
type FinalAudioCache struct {
	dir      string
	maxBytes int64
}

// CachedFinalAudioReference is the immutable metadata needed to materialize a
// certified cached audio master. Workflow-specific publication fields remain
// owned by the scripts capability.
type CachedFinalAudioReference struct {
	AssetID              string
	Path                 string
	Container            string
	AudioContractVersion string
	AudioPlanVersion     string
	PlanSHA256           string
	FinalAudioSHA256     string
	Codec                string
	Profile              string
	SampleRate           int
	Channels             int
	ChannelLayout        string
	Bitrate              int64
	DurationUS           int64
	DurationMS           int64
	StartPTS             int64
	SizeBytes            int64
	FinalMix             bool
	CopyEligible         bool
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

func NewFinalAudioCache(dir string) *FinalAudioCache {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "", "off", "disabled", "none":
		// The kill switch is honoured at the constructor too, so no caller can
		// end up with a cache it asked to disable.
		return nil
	}
	maxBytes := DefaultMaxBytes
	if raw := strings.TrimSpace(os.Getenv(MaxBytesEnv)); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			maxBytes = parsed
		}
	}
	return &FinalAudioCache{dir: strings.TrimSpace(dir), maxBytes: maxBytes}
}

func (c *FinalAudioCache) Enabled() bool {
	return c != nil && c.dir != ""
}

func (c *FinalAudioCache) entryPath(planSHA string) string {
	return filepath.Join(c.dir, planSHA[:2], planSHA)
}

func FinalAudioContainerExt(container string) string {
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
func (c *FinalAudioCache) Take(plan capabilityaudio.CompiledAudioPlan, destDir string) (CachedFinalAudioReference, bool) {
	if !c.Enabled() || len(plan.PlanSHA256) != 64 || strings.TrimSpace(destDir) == "" {
		return CachedFinalAudioReference{}, false
	}
	base := c.entryPath(plan.PlanSHA256)
	payload, err := os.ReadFile(base + ".json")
	if err != nil {
		return CachedFinalAudioReference{}, false
	}
	var entry finalAudioCacheEntry
	if err := json.Unmarshal(payload, &entry); err != nil {
		return CachedFinalAudioReference{}, false
	}
	if !strings.EqualFold(entry.PlanSHA256, plan.PlanSHA256) ||
		entry.AudioContractVersion != capabilityaudio.AudioContractVersion ||
		entry.AudioPlanVersion != plan.Version ||
		len(entry.FinalAudioSHA256) != 64 || entry.SizeBytes <= 0 {
		return CachedFinalAudioReference{}, false
	}
	cached := base + FinalAudioContainerExt(entry.Container)
	info, err := os.Stat(cached)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.SizeBytes {
		return CachedFinalAudioReference{}, false
	}
	staged, sha, size, err := StageCachedFinalAudio(cached, destDir, plan.PlanSHA256, entry.Container)
	if err != nil || size != entry.SizeBytes || !strings.EqualFold(sha, entry.FinalAudioSHA256) {
		if staged != "" {
			_ = os.Remove(staged)
		}
		return CachedFinalAudioReference{}, false
	}
	return CachedFinalAudioReference{
		// The staged path is the run-owned identity: the renderer names its
		// artifact by its own local path, and a cache hit must not make two
		// runs share one asset id.
		AssetID:              staged,
		Path:                 staged,
		Container:            entry.Container,
		AudioContractVersion: capabilityaudio.AudioContractVersion,
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
func (c *FinalAudioCache) Put(plan capabilityaudio.CompiledAudioPlan, ref CachedFinalAudioReference) {
	// Only store what the canonical gate would accept: an entry that cannot
	// pass ValidateFinalAudioReference would be a guaranteed miss, and a cache
	// full of them is indistinguishable from a broken one.
	if !c.Enabled() || len(plan.PlanSHA256) != 64 || strings.TrimSpace(ref.Path) == "" ||
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
	final := base + FinalAudioContainerExt(ref.Container)
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return
	}
	payload, err := json.Marshal(finalAudioCacheEntry{
		PlanSHA256:           plan.PlanSHA256,
		AudioContractVersion: capabilityaudio.AudioContractVersion,
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
	c.enforceByteBudget(base)
}

// enforceByteBudget keeps the cache within maxBytes, evicting the least
// recently used entries (oldest modification time) first. base is the entry
// just written and is never evicted. Errors are ignored on purpose: the cache
// is an optimization, so a failed eviction leaves the previous bound in place
// rather than failing a render.
func (c *FinalAudioCache) enforceByteBudget(base string) {
	if c == nil || c.maxBytes <= 0 {
		return
	}
	type cachedEntry struct {
		master  string
		sidecar string
		size    int64
		mod     time.Time
	}
	var entries []cachedEntry
	var total int64
	walkErr := filepath.WalkDir(c.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var meta finalAudioCacheEntry
		if json.Unmarshal(payload, &meta) != nil {
			return nil
		}
		master := strings.TrimSuffix(path, ".json") + FinalAudioContainerExt(meta.Container)
		info, statErr := os.Stat(master)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		entries = append(entries, cachedEntry{master: master, sidecar: path, size: info.Size(), mod: info.ModTime()})
		total += info.Size()
		return nil
	})
	if walkErr != nil || total <= c.maxBytes {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.Before(entries[j].mod) })
	for _, e := range entries {
		if total <= c.maxBytes {
			break
		}
		if strings.TrimSuffix(e.sidecar, ".json") == base {
			continue
		}
		if os.Remove(e.master) == nil {
			_ = os.Remove(e.sidecar)
			total -= e.size
		}
	}
}

// stageCachedFinalAudio materializes independent run-owned bytes under a unique
// name. A hard link would let a caller mutate the cached master or another run,
// so it prefers a copy-on-write reflink (independent inode, no byte copy) and
// falls back to a streaming copy that hashes the bytes IN THE SAME PASS. The
// previous shape copied the whole master and then re-read it just to learn the
// digest the sidecar already named.
func StageCachedFinalAudio(cached, destDir, planSHA, container string) (path, sha string, size int64, err error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", "", 0, err
	}
	placeholder, err := os.CreateTemp(destDir, "pipelinegen-final-audio-"+planSHA[:16]+"-*"+FinalAudioContainerExt(container))
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
	if cloneErr := CloneFile(staged, cached); cloneErr == nil {
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
