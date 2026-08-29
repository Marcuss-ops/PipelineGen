package cliprender

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// PreparedAssetResolver checks shared content-addressed bytes before invoking
// the existing AssetMaterializer. The fallback preserves current behavior on
// cold caches or when no verified hash is available.
type PreparedAssetResolver struct {
	Root     string
	Fallback AssetMaterializer
}

func NewPreparedAssetResolver(root string, fallback AssetMaterializer) (*PreparedAssetResolver, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("prepared asset resolver: cache root is required")
	}
	if fallback == nil {
		return nil, errors.New("prepared asset resolver: fallback materializer is required")
	}
	return &PreparedAssetResolver{Root: root, Fallback: fallback}, nil
}

func (r *PreparedAssetResolver) Materialize(ctx context.Context, ref AssetRef) (*MaterializedAsset, error) {
	if r == nil || r.Fallback == nil {
		return nil, errors.New("prepared asset resolver is not wired")
	}
	expected := normalizeSHA256(ref.LegacyFileMD5)
	if expected != "" {
		path := filepath.Join(r.Root, expected, "source"+assetExtension(ref.MediaType))
		if asset, ok := verifiedPreparedAsset(path, expected, ref); ok {
			return asset, nil
		}
	}
	return r.Fallback.Materialize(ctx, ref)
}

func verifiedPreparedAsset(path, expected string, ref AssetRef) (*MaterializedAsset, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() <= 0 {
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	computed, err := digest.SHA256Reader(file)
	_ = file.Close()
	if err != nil || computed != expected {
		return nil, false
	}
	return &MaterializedAsset{
		AssetID: ref.AssetID, LocalPath: path, SHA256: computed,
		SizeBytes: info.Size(), DurationMS: ref.DurationMS, FromCache: true,
	}, true
}

func normalizeSHA256(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "sha256:")))
	if len(value) != 64 {
		return ""
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return ""
		}
	}
	return value
}

func assetExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "audio", "sound_effect":
		return ".m4a"
	case "image":
		return ".jpg"
	case "watermark":
		return ".png"
	default:
		return ".mp4"
	}
}

var _ AssetMaterializer = (*PreparedAssetResolver)(nil)
