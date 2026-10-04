// internal/platform/downloader/ytdlp_mirror.go —
//
// Local per-video-ID mirror (F1, TODO-pipeline-100x-velocita): the same
// YouTube source is never re-downloaded. A full-source download lands in a
// content-addressed cache entry (key = video ID + format signature) and
// every later request for the same source hard-links the cached artifact
// into the caller's staging path instead of paying the network again.
// Measured basis: stock.youtube_download averaged ~10-35s per call and
// consumed 5.5h/week on the timing snapshot, largely repeated downloads of
// sources already fetched by an earlier job; clip.extract re-fetches the
// same sources the same way.
//
// Correctness contract:
//   - YouTube-only. Artlist and generic URLs keep the exact download path
//     (cookies, impersonation, and per-source licensing differ).
//   - The cache key includes the format signature, so a request with a
//     different format/merge policy can never be served another format's
//     bytes.
//   - Only FULL-source downloads are mirrored. Sectioned downloads
//     (DownloadSections / DownloadRange) keep their yt-dlp/ffmpeg path.
//   - A cache hit is still verified with the same VerifyFile gate as a
//     fresh download; a corrupted entry is evicted and the download reruns.
//   - Same-key in-flight downloads are single-flighted: concurrent jobs
//     asking for one video wait for one download instead of racing YouTube.
package downloader

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// EnvYTDLPMirrorRoot is the operator knob for the mirror root directory
// (PipelineGen-owned PIPELINEGEN_* env namespace).
//
// Mirroring is OPT-IN: unset or empty disables it entirely (every download
// reruns against the network), because a shared artifact cache changes the
// observable behavior of every Download call — including hermetic tests —
// and a silent default would leak cross-run state into any caller that did
// not ask for it. Production enables it by setting the env to a persistent
// cache path in the service environment (the same deployment-owned knob
// pattern as the other PIPELINEGEN_* quick wins); the literal value "off"
// also disables it where the variable is exported.
const EnvYTDLPMirrorRoot = "PIPELINEGEN_YTDLP_MIRROR_ROOT"

// mirrorEntryMeta is the sidecar metadata of one cache entry.
type mirrorEntryMeta struct {
	VideoID string `json:"video_id"`
	Key     string `json:"key"`
	Ext     string `json:"ext"`
	Size    int64  `json:"size"`
}

// youtubeVideoID extracts the canonical video ID from a YouTube URL, "" when
// the URL is not YouTube (mirroring is intentionally YouTube-only).
func youtubeVideoID(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	var id string
	switch {
	case strings.Contains(trimmed, "youtu.be/"):
		id = afterSubstring(trimmed, "youtu.be/")
	case strings.Contains(trimmed, "v="):
		id = afterSubstring(trimmed, "v=")
	case strings.Contains(trimmed, "shorts/"):
		id = afterSubstring(trimmed, "shorts/")
	default:
		return ""
	}
	id = strings.TrimRight(id, "/")
	if idx := strings.IndexAny(id, "&?"); idx >= 0 {
		id = id[:idx]
	}
	if id == "" || len(id) > 32 || strings.ContainsAny(id, "/\\ \t") {
		return ""
	}
	return id
}

func afterSubstring(s, sep string) string {
	idx := strings.Index(s, sep)
	if idx < 0 {
		return ""
	}
	return s[idx+len(sep):]
}

// mirrorRoot resolves the mirror root directory; ok=false when mirroring is
// disabled (unset/empty, or "off") or the root cannot be created.
func (d *YTDLPDownloader) mirrorRoot() (string, bool) {
	root := strings.TrimSpace(os.Getenv(EnvYTDLPMirrorRoot))
	if root == "" || strings.EqualFold(root, "off") {
		return "", false
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", false
	}
	return root, true
}

// mirrorKey hashes the source identity: video ID + format signature. The
// format signature covers the canonical format argument, the caller's format
// override, and the merge policy, so two requests that would produce
// different bytes never share an entry.
func mirrorKey(videoID, formatSignature string) string {
	return digest.SHA256String(videoID + "\x00" + formatSignature)[:32]
}

// mirrorInFlight serializes same-key downloads across concurrent jobs.
var mirrorInFlight sync.Map // key -> *sync.Mutex

func lockMirrorKey(key string) *sync.Mutex {
	locked, _ := mirrorInFlight.LoadOrStore(key, &sync.Mutex{})
	return locked.(*sync.Mutex)
}

// mirrorLookup serves a cached full-source download. ok=false means "no
// usable entry; run the network download".
func (d *YTDLPDownloader) mirrorLookup(root, key, outputTemplate string) (string, bool) {
	metaPath := filepath.Join(root, key+".json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return "", false
	}
	var meta mirrorEntryMeta
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Ext == "" || meta.Size <= 0 {
		_ = os.Remove(metaPath)
		return "", false
	}
	entry := filepath.Join(root, key+"."+meta.Ext)
	info, err := os.Stat(entry)
	if err != nil || info.Size() != meta.Size {
		// Corrupted/incomplete entry: evict and rerun the download.
		_ = os.Remove(entry)
		_ = os.Remove(metaPath)
		return "", false
	}
	dest := mirrorDestination(outputTemplate, meta.Ext)
	if dest == "" {
		return "", false
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", false
	}
	if err := linkOrCopy(entry, dest); err != nil {
		return "", false
	}
	if err := d.verifier.VerifyFile(dest); err != nil {
		// A bad cache artifact must never look like a successful download:
		// evict and let the caller fall through to the network path.
		_ = os.Remove(dest)
		_ = os.Remove(entry)
		_ = os.Remove(metaPath)
		return "", false
	}
	return dest, true
}

// mirrorStore records a completed download into the cache. Failures are
// non-fatal: mirroring is an optimization, never a delivery dependency.
func mirrorStore(root, key, videoID, resolvedPath string) {
	ext := strings.TrimPrefix(filepath.Ext(resolvedPath), ".")
	if ext == "" {
		return
	}
	info, err := os.Stat(resolvedPath)
	if err != nil || info.Size() <= 0 {
		return
	}
	entry := filepath.Join(root, key+"."+ext)
	_ = os.Remove(entry)
	if err := linkOrCopy(resolvedPath, entry); err != nil {
		return
	}
	meta, err := json.Marshal(mirrorEntryMeta{VideoID: videoID, Key: key, Ext: ext, Size: info.Size()})
	if err != nil {
		return
	}
	metaTmp := filepath.Join(root, key+".json.tmp")
	if err := os.WriteFile(metaTmp, meta, 0o644); err != nil {
		return
	}
	_ = os.Rename(metaTmp, filepath.Join(root, key+".json"))
}

// mirrorDestination expands the caller's output template with the cached
// entry's extension; "" when the template is not expandable.
func mirrorDestination(outputTemplate, ext string) string {
	if outputTemplate == "" || ext == "" {
		return ""
	}
	if strings.Contains(outputTemplate, "%(ext)s") {
		return strings.Replace(outputTemplate, "%(ext)s", ext, 1)
	}
	return outputTemplate + "." + ext
}

// linkOrCopy hard-links src into dst when both live on one filesystem, and
// falls back to a byte copy otherwise. A hard link keeps the cache entry
// alive even when the caller's staging copy is later cleaned.
func linkOrCopy(src, dst string) error {
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("mirror open source: %w", err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("mirror create dest: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("mirror copy: %w", err)
	}
	return out.Close()
}

// mirrorFormatSignature builds the cache-key format signature of one
// full-source request: canonical format argument + caller overrides + merge
// policy. Any change that would change the produced bytes changes the key.
func (d *YTDLPDownloader) mirrorFormatSignature(req *DownloadRequest) string {
	parts := append([]string{}, d.cmdBuilder.FormatArg(true)...)
	parts = append(parts, req.Format, req.MergeFormat)
	return strings.Join(parts, "\x1f")
}
