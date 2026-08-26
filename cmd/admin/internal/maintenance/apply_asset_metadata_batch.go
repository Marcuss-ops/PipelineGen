package maintenance

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

func RunApplyAssetMetadataBatch(args []string) error {
	path := ""
	if len(args) == 2 && args[0] == "--manifest" {
		path = args[1]
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("usage: apply-asset-metadata-batch --manifest file.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read metadata manifest: %w", err)
	}
	var manifests []assetMetadataManifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifests); err != nil {
		return fmt.Errorf("decode metadata manifest array: %w", err)
	}
	if len(manifests) == 0 {
		return errors.New("metadata manifest array is empty")
	}
	for i := range manifests {
		if strings.TrimSpace(manifests[i].ClipID) == "" || len(manifests[i].Metadata) == 0 {
			return fmt.Errorf("metadata manifest item %d requires clip_id and metadata", i)
		}
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
	if root == nil || root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil || root.Outbox.Dispatcher == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
		return errors.New("clips repository and transactional outbox are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()
	for _, manifest := range manifests {
		clip, err := root.Repos.ClipsRepo.GetClip(ctx, manifest.ClipID)
		if err != nil {
			return fmt.Errorf("load clip %s: %w", manifest.ClipID, err)
		}
		if clip == nil {
			return fmt.Errorf("clip %s is not indexed", manifest.ClipID)
		}
		if strings.TrimSpace(clip.LegacyFileMD5()) == "" {
			return fmt.Errorf("clip %s has no file hash", manifest.ClipID)
		}
		if manifest.Name != "" {
			clip.Name = manifest.Name
		}
		for key, value := range manifest.Metadata {
			if strings.TrimSpace(key) == "" {
				return errors.New("metadata keys cannot be empty")
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("encode metadata %q: %w", key, err)
			}
			if string(encoded) == "null" {
				return fmt.Errorf("metadata %q cannot be null", key)
			}
			if valueString, ok := value.(string); ok {
				encoded = []byte(valueString)
			}
			clip.SetMetadataString(key, string(encoded))
		}
		clip.UpdatedAt = time.Now().UTC()
		before, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
		if err != nil {
			return err
		}
		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.LegacyFileMD5()); err != nil {
			return fmt.Errorf("index asset %s: %w", manifest.ClipID, err)
		}
		if err := cli.WaitForAssetIndexOutbox(ctx, root, before); err != nil {
			return err
		}
	}
	fmt.Printf("Asset metadata batch applied: assets=%d manifest=%s\n", len(manifests), path)
	return nil
}
