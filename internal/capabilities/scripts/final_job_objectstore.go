package scriptgeneration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// finalJobObjectStoreRef turns a RenderingGen artifact already present in the
// shared Objectstore into a short-lived HTTPS URL the remote worker can fetch
// directly. Creator and Objectstore share the signing secret; neither Velox
// nor the Master needs credentials or a proxy path for the object bytes.
func finalJobObjectStoreRef(ctx context.Context, preferredID, storageKey, sha string, sizeBytes int64) (map[string]any, error) {
	sha = strings.ToLower(strings.TrimSpace(sha))
	storageKey = strings.ToLower(strings.TrimSpace(storageKey))
	if len(sha) != 64 || storageKey != sha || sizeBytes <= 0 {
		return nil, fmt.Errorf("objectstore asset requires matching SHA-256 storage key and positive size")
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("OBJECTSTORE_PUBLIC_URL")), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed == nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" {
		return nil, fmt.Errorf("OBJECTSTORE_PUBLIC_URL must be a public HTTPS base URL")
	}
	secret := strings.TrimSpace(os.Getenv("OBJECTSTORE_SIGNING_SECRET"))
	if len(secret) < 32 {
		return nil, fmt.Errorf("OBJECTSTORE_SIGNING_SECRET must contain at least 32 characters")
	}
	if err := ensureFinalJobObjectStored(ctx, sha, sizeBytes); err != nil {
		return nil, err
	}
	expires := time.Now().Add(7 * 24 * time.Hour).Unix()
	objectPath := "/objects/" + sha
	message := fmt.Sprintf("GET\n%s\n%d", objectPath, expires)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	query := url.Values{"expires": {fmt.Sprintf("%d", expires)}, "sig": {hex.EncodeToString(mac.Sum(nil))}}
	assetID := strings.TrimSpace(preferredID)
	if assetID == "" || strings.ContainsAny(assetID, "\\/\x00\r\n") || assetID == "." || assetID == ".." {
		assetID = "sha256-" + sha
	}
	assetURL := base + objectPath + "?" + query.Encode()
	return map[string]any{
		"asset_id": assetID, "asset_key": assetID,
		"url": "velox-asset://" + assetID, "source_uri": assetURL,
		"sha256": sha, "size_bytes": sizeBytes,
	}, nil
}

func ensureFinalJobObjectStored(ctx context.Context, sha string, expectedSize int64) error {
	storeURL := finalJobPrivateObjectStoreURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, storeURL+"/objects/"+sha, nil)
	if err != nil {
		return fmt.Errorf("build Objectstore availability check: %w", err)
	}
	if token := strings.TrimSpace(os.Getenv("OBJECTSTORE_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("check Creator-to-Objectstore path: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Objectstore has no object %s (HEAD returned %d)", sha, resp.StatusCode)
	}
	if resp.ContentLength != expectedSize {
		return fmt.Errorf("Objectstore object %s has size %d, expected %d", sha, resp.ContentLength, expectedSize)
	}
	return nil
}

func publishFinalJobLocalObject(ctx context.Context, path, sha string, sizeBytes int64) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("Creator-local asset path is required for direct Objectstore publication")
	}
	actualSHA, actualSize, err := digest.SHA256File(path)
	if err != nil {
		return fmt.Errorf("hash Creator-local asset: %w", err)
	}
	if !strings.EqualFold(actualSHA, strings.TrimSpace(sha)) || actualSize != sizeBytes {
		return fmt.Errorf("Creator-local asset does not match certified hash/size")
	}
	if err := ensureFinalJobObjectStored(ctx, strings.ToLower(sha), sizeBytes); err == nil {
		return nil
	}
	storeURL := finalJobPrivateObjectStoreURL()
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Creator-local asset for Objectstore upload: %w", err)
	}
	defer file.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, storeURL+"/objects/"+strings.ToLower(sha), file)
	if err != nil {
		return fmt.Errorf("build Objectstore upload: %w", err)
	}
	req.ContentLength = sizeBytes
	if token := strings.TrimSpace(os.Getenv("OBJECTSTORE_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload Creator-local asset to Objectstore: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Objectstore rejected Creator-local asset upload with status %d", resp.StatusCode)
	}
	return ensureFinalJobObjectStored(ctx, strings.ToLower(sha), sizeBytes)
}

// PublishFinalJobLocalObject publishes a Creator-local media file once under
// its verified content address and returns the worker-fetchable reference.
// It is exported for the composition root's media resolver adapter.
func PublishFinalJobLocalObject(ctx context.Context, preferredID, path, sha string, sizeBytes int64) (map[string]any, error) {
	if err := publishFinalJobLocalObject(ctx, path, sha, sizeBytes); err != nil {
		return nil, err
	}
	return finalJobObjectStoreRef(ctx, preferredID, sha, sha, sizeBytes)
}

func finalJobPrivateObjectStoreURL() string {
	storeURL := strings.TrimRight(strings.TrimSpace(os.Getenv("RENDERINGGEN_STORE_URL")), "/")
	if storeURL == "" {
		return "http://127.0.0.1:9000"
	}
	return storeURL
}

func finalJobObjectStoreConfigured() bool {
	return strings.TrimSpace(os.Getenv("OBJECTSTORE_PUBLIC_URL")) != ""
}

type finalJobObjectStoreAssetResolver interface {
	ResolveFinalJobObjectStoreAsset(context.Context, string) (map[string]any, error)
}

func resolveFinalJobAsset(ctx context.Context, resolver FinalJobAssetResolver, id string) (map[string]any, error) {
	if finalJobObjectStoreConfigured() {
		publisher, ok := resolver.(finalJobObjectStoreAssetResolver)
		if !ok {
			return nil, fmt.Errorf("direct Objectstore mode requires a Creator-side asset publisher")
		}
		return publisher.ResolveFinalJobObjectStoreAsset(ctx, id)
	}
	return resolver.ResolveFinalJobAsset(ctx, id)
}

func finalJobRenderArtifactRef(ctx context.Context, artifact *RenderArtifact) (map[string]any, error) {
	if artifact == nil {
		return nil, fmt.Errorf("render artifact is missing")
	}
	if finalJobObjectStoreConfigured() {
		return finalJobObjectStoreRef(ctx, "", artifact.StorageKey, artifact.SHA256, artifact.SizeBytes)
	}
	driveID := strings.TrimSpace(artifact.DriveFileID)
	if driveID == "" || len(strings.TrimSpace(artifact.SHA256)) != 64 || artifact.SizeBytes <= 0 {
		return nil, fmt.Errorf("render artifact is not a certified published Drive artifact")
	}
	return map[string]any{
		"asset_id": firstFinalJobValue(artifact.ID, driveID), "drive_file_id": driveID,
		"url": driveFileWebLink(driveID), "sha256": strings.TrimSpace(artifact.SHA256), "size_bytes": artifact.SizeBytes,
	}, nil
}
