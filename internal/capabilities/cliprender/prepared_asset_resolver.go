package cliprender

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// PreparedAssetResolver checks shared content-addressed bytes before invoking
// the existing AssetMaterializer. The fallback preserves current behavior on
// cold caches or when no verified hash is available.
//
// Verification is memoized per process (ContentVerifier): the shared bytes are
// immutable content-addressed artifacts, so a batch of clip renders that reuse
// one source verifies it ONCE instead of paying a full-file SHA-256 pass per
// clip. Any change to the file (size/mtime) invalidates the memo.
type PreparedAssetResolver struct {
	Root     string
	Fallback AssetMaterializer
	verifier *ContentVerifier
}

func NewPreparedAssetResolver(root string, fallback AssetMaterializer) (*PreparedAssetResolver, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("prepared asset resolver: cache root is required")
	}
	if fallback == nil {
		return nil, errors.New("prepared asset resolver: fallback materializer is required")
	}
	return &PreparedAssetResolver{Root: root, Fallback: fallback, verifier: NewContentVerifier(nil)}, nil
}

func (r *PreparedAssetResolver) Materialize(ctx context.Context, ref AssetRef) (*MaterializedAsset, error) {
	if r == nil || r.Fallback == nil {
		return nil, errors.New("prepared asset resolver is not wired")
	}
	expected := normalizeSHA256(ref.LegacyFileMD5)
	if expected != "" {
		path := filepath.Join(r.Root, expected, "source"+assetExtension(ref.MediaType))
		if asset, ok := r.verifiedPreparedAsset(path, expected, ref); ok {
			return asset, nil
		}
	}
	return r.Fallback.Materialize(ctx, ref)
}

// verifiedPreparedAsset reports the shared content-addressed file at path when
// its bytes still match expected. The digest is memoized (size+modtime keyed),
// so repeated renders of the same source never re-read it; a drifted file
// fails the size/mtime validity check and is re-hashed from scratch.
func (r *PreparedAssetResolver) verifiedPreparedAsset(path, expected string, ref AssetRef) (*MaterializedAsset, bool) {
	verifier := r.verifier
	if verifier == nil {
		verifier = NewContentVerifier(nil)
	}
	computed, size, err := verifier.Verify(path)
	if err != nil || size <= 0 || computed != expected {
		return nil, false
	}
	return &MaterializedAsset{
		AssetID: ref.AssetID, LocalPath: path, SHA256: computed,
		SizeBytes: size, DurationMS: ref.DurationMS, FromCache: true,
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
