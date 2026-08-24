package assembly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	contract "github.com/Marcuss-ops/PipelineGen/internal/kernel/assembly"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// FileCache is the real Velox-side content-addressed cache. Files are stored
// below root/<sha256>.media; a successful Prepare always verifies the digest.
type FileCache struct {
	Root    string
	Client  *http.Client
	FFprobe string
}

func NewFileCache(root string) (*FileCache, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("assembly cache root is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &FileCache{Root: root, Client: http.DefaultClient, FFprobe: "ffprobe"}, nil
}
func (c *FileCache) path(a contract.AssetRequirement) string {
	key := a.SHA256
	key = strings.TrimPrefix(key, "sha256:")
	if key == "" {
		key = a.AssetID
	}
	return filepath.Join(c.Root, key+".media")
}
func (c *FileCache) Prepare(ctx context.Context, a contract.AssetRequirement) (bool, error) {
	dst := c.path(a)
	if ok, err := verifiedFile(dst, a.SHA256); err != nil {
		return false, err
	} else if ok {
		alias := filepath.Join(c.Root, a.AssetID+".media")
		if alias != dst {
			_ = os.Link(dst, alias)
		}
		if a.Kind == "source_clip" {
			if err := c.Probe(ctx, dst); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	if a.Location == "" {
		return false, fmt.Errorf("asset %q has no download location", a.AssetID)
	}
	tmp, err := os.CreateTemp(c.Root, ".download-*")
	if err != nil {
		return false, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if strings.HasPrefix(a.Location, "file://") || (filepath.IsAbs(a.Location) && !strings.HasPrefix(a.Location, "http")) {
		src := strings.TrimPrefix(a.Location, "file://")
		in, err := os.Open(src)
		if err != nil {
			return false, err
		}
		_, copyErr := io.Copy(tmp, in)
		_ = in.Close()
		if copyErr != nil {
			_ = tmp.Close()
			return false, copyErr
		}
		if err := tmp.Close(); err != nil {
			return false, err
		}
	} else {
		var err error
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Location, nil)
		if err != nil {
			return false, err
		}
		client := c.Client
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return false, fmt.Errorf("download %s: http %s", a.AssetID, resp.Status)
		}
		if _, err = io.Copy(tmp, resp.Body); err != nil {
			return false, err
		}
		if err = tmp.Close(); err != nil {
			return false, err
		}
	}
	if ok, err := verifiedFile(name, a.SHA256); err != nil || !ok {
		if err != nil {
			return false, err
		}
		return false, fmt.Errorf("asset %q SHA256 mismatch", a.AssetID)
	}
	if err := os.Rename(name, dst); err != nil {
		return false, err
	}
	// Timeline references use the logical asset_id while the canonical cache
	// key is SHA256. Keep a non-authoritative alias for deterministic lookup.
	alias := filepath.Join(c.Root, a.AssetID+".media")
	if alias != dst {
		_ = os.Link(dst, alias)
	}
	if a.Kind == "source_clip" {
		if err := c.Probe(ctx, dst); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (c *FileCache) Probe(ctx context.Context, path string) error {
	probe := c.FFprobe
	if probe == "" {
		probe = "ffprobe"
	}
	cmd := exec.CommandContext(ctx, probe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffprobe %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
func verifiedFile(path, expected string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if expected == "" {
		return true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	got, err := digest.SHA256Reader(f)
	if err != nil {
		return false, err
	}
	return strings.TrimPrefix(expected, "sha256:") == got, nil
}

type FileFinalizer struct {
	Cache      *FileCache
	OutputRoot string
	FFmpeg     string
}

func (f *FileFinalizer) Finalize(ctx context.Context, p contract.FinalizeV1) (contract.FinalizeResultV1, error) {
	if f == nil || f.Cache == nil {
		return contract.FinalizeResultV1{}, fmt.Errorf("assembly finalizer cache is nil")
	}
	if err := os.MkdirAll(f.OutputRoot, 0o755); err != nil {
		return contract.FinalizeResultV1{}, err
	}
	for _, a := range p.RuntimeAssets {
		if _, err := f.Cache.Prepare(ctx, a); err != nil {
			return contract.FinalizeResultV1{}, fmt.Errorf("finalize runtime asset %q: %w", a.AssetID, err)
		}
	}
	list, err := os.CreateTemp(f.OutputRoot, "concat-*.txt")
	if err != nil {
		return contract.FinalizeResultV1{}, err
	}
	listName := list.Name()
	defer os.Remove(listName)
	for _, e := range p.Timeline {
		a := contract.AssetRequirement{AssetID: e.AssetID, Kind: "timeline", SHA256: e.AssetID, Availability: contract.AvailabilityKnown, Required: true}
		path := f.Cache.path(a)
		if _, err := os.Stat(path); err != nil {
			return contract.FinalizeResultV1{}, fmt.Errorf("timeline asset %q not cached: %w", e.AssetID, err)
		}
		line := "file '" + strings.ReplaceAll(path, "'", "'\\''") + "'\n"
		if _, err := io.WriteString(list, line); err != nil {
			return contract.FinalizeResultV1{}, err
		}
	}
	if err := list.Close(); err != nil {
		return contract.FinalizeResultV1{}, err
	}
	out := filepath.Join(f.OutputRoot, p.AssemblyID+".mp4")
	ffmpeg := f.FFmpeg
	if ffmpeg == "" {
		return contract.FinalizeResultV1{}, fmt.Errorf("assembly finalizer ffmpeg executable is not configured")
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-y", "-f", "concat", "-safe", "0", "-i", listName, "-c", "copy", out)
	if data, err := cmd.CombinedOutput(); err != nil {
		return contract.FinalizeResultV1{}, fmt.Errorf("ffmpeg concat: %w: %s", err, strings.TrimSpace(string(data)))
	}
	sha, err := fileSHA256(out)
	if err != nil {
		return contract.FinalizeResultV1{}, err
	}
	return contract.FinalizeResultV1{ContractVersion: contract.ContractVersion, AssemblyID: p.AssemblyID, ArtifactID: sha, ArtifactPath: out, State: "completed"}, nil
}
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return digest.SHA256Reader(f)
}

// ArtifactManifestResult turns a final local file into the canonical worker
// sidecar consumed by CompleteWithArtifacts.
func ArtifactManifestResult(jid, path string) (map[string]any, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	sha, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	m := job.ArtifactManifest{SchemaVersion: job.SchemaVersionArtifactManifestV1, JobID: jid, Artifacts: []job.Artifact{{ID: jid + ":final", Kind: job.ArtifactKindFinalVideo, Path: path, Filename: filepath.Base(path), MIMEType: "video/mp4", SizeBytes: st.Size(), SHA256: sha, Required: true}}}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return map[string]any{job.ManifestKey: string(b), "sha256": sha, "size_bytes": strconv.FormatInt(st.Size(), 10)}, nil
}
