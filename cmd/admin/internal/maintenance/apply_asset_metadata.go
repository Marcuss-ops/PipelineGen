package maintenance

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

// assetMetadataManifest is the reusable, data-only input for
// apply-asset-metadata. It replaces one command per animal/video.
type assetMetadataManifest struct {
	ClipID   string         `json:"clip_id"`
	Name     string         `json:"name,omitempty"`
	Metadata map[string]any `json:"metadata"`
}

func loadAssetMetadataManifest(path string) (*assetMetadataManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read metadata manifest: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var manifest assetMetadataManifest
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode metadata manifest: %w", err)
	}
	if strings.TrimSpace(manifest.ClipID) == "" {
		return nil, errors.New("metadata manifest: clip_id is required")
	}
	if len(manifest.Metadata) == 0 && strings.TrimSpace(manifest.Name) == "" {
		return nil, errors.New("metadata manifest: name or metadata is required")
	}
	return &manifest, nil
}

func RunApplyAssetMetadata(args []string) error {
	fs := flag.NewFlagSet("apply-asset-metadata", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	manifestPath := fs.String("manifest", "", "JSON metadata manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return errors.New("--manifest is required")
	}
	manifest, err := loadAssetMetadataManifest(*manifestPath)
	if err != nil {
		return err
	}
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil ||
		root.Outbox.Dispatcher == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
		return errors.New("clips repository and transactional outbox are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	clip, err := root.Repos.ClipsRepo.GetClip(ctx, manifest.ClipID)
	if err != nil {
		return fmt.Errorf("load clip: %w", err)
	}
	if clip == nil {
		return fmt.Errorf("clip %s is not indexed", manifest.ClipID)
	}
	if strings.TrimSpace(clip.LegacyFileMD5()) == "" {
		return errors.New("clip has no file hash")
	}
	if manifest.Name != "" {
		clip.Name = manifest.Name
	}
	for key, value := range manifest.Metadata {
		if strings.TrimSpace(key) == "" {
			return errors.New("metadata manifest: metadata keys cannot be empty")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode metadata %q: %w", key, err)
		}
		if string(encoded) == "null" {
			return fmt.Errorf("metadata manifest: %q cannot be null", key)
		}
		if s, ok := value.(string); ok {
			encoded = []byte(s)
		}
		clip.SetMetadataString(key, string(encoded))
	}
	clip.UpdatedAt = time.Now().UTC()
	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.LegacyFileMD5()); err != nil {
		return fmt.Errorf("apply asset metadata: %w", err)
	}
	if err := cli.WaitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Printf("Asset metadata applied: asset=%s keys=%d manifest=%s\n", clip.ID, len(manifest.Metadata), *manifestPath)
	return nil
}
