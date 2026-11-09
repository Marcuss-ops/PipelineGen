package scriptgeneration

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// The final-job publisher runs once per clip and once per audio asset, so a
// fresh http.Client per call would discard the connection pool between two
// uploads to the same store. Two package-level clients keep keep-alive reuse
// while preserving the distinct safety bounds: availability probes must not
// hang a job for minutes, uploads may legitimately take that long.
var (
	finalJobObjectStoreHeadClient   = &http.Client{Timeout: 20 * time.Second, Transport: finalJobObjectStoreTransport()}
	finalJobObjectStoreUploadClient = &http.Client{Timeout: 10 * time.Minute, Transport: finalJobObjectStoreTransport()}
)

func finalJobObjectStoreTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 16
	return transport
}

// signStoredFinalJobObject is called only after this operation verified the
// object's availability and size. It avoids repeating the same HEAD on every
// publication while retaining the check at the external-reference boundary.
func signStoredFinalJobObject(_ string, storageKey, sha string, sizeBytes int64) (map[string]any, error) {
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
	expires := time.Now().Add(7 * 24 * time.Hour).Unix()
	objectPath := "/objects/" + sha
	message := fmt.Sprintf("GET\n%s\n%d", objectPath, expires)
	mac := hmac.New(digest.NewSHA256, []byte(secret))
	_, _ = mac.Write([]byte(message))
	query := url.Values{"expires": {fmt.Sprintf("%d", expires)}, "sig": {hex.EncodeToString(mac.Sum(nil))}}
	// The content address is the only canonical identity for an objectstore
	// asset. The Producer's preferred ID (for example a manual_* alias) is a
	// display hint, not a key the Master bridge can resolve; using it here
	// produced a 404 while the same bytes were available under the SHA.
	assetID := sha

	assetURL := base + objectPath + "?" + query.Encode()
	return map[string]any{
		"asset_id": assetID,
		"url":      "velox-asset://" + assetID, "source_uri": assetURL,
		"sha256": sha, "size_bytes": sizeBytes,
	}, nil
}

// signCertifiedFinalJobObject signs a reference to bytes that another
// certified boundary (RenderingGen) already uploaded and described by its
// immutable content address. Unlike finalJobObjectStoreRef it deliberately
// performs NO availability HEAD: re-checking an object the renderer just
// certified is the duplicate round-trip this path exists to remove. Callers
// must pass the exact certified identity; shape is validated here, existence
// is the certificate's claim.
func signCertifiedFinalJobObject(storageKey, sha string, sizeBytes int64) (map[string]any, error) {
	if sizeBytes <= 0 {
		return nil, fmt.Errorf("certified objectstore asset requires a positive size")
	}
	return signStoredFinalJobObject("", storageKey, sha, sizeBytes)
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
	resp, err := finalJobObjectStoreHeadClient.Do(req)
	if err != nil {
		return fmt.Errorf("check Creator-to-Objectstore path: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
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
	// The object is addressed by its SHA-256: if that address already holds
	// exactly these bytes, the remote will fetch THOSE bytes and re-reading the
	// local file answers nothing new. Checking the address first turns a
	// per-run re-hash of every source into a HEAD, and only a real upload still
	// verifies the file it is about to send.
	if err := ensureFinalJobObjectStored(ctx, strings.ToLower(strings.TrimSpace(sha)), sizeBytes); err == nil {
		return nil
	}
	actualSHA, actualSize, err := digest.SHA256File(path)
	if err != nil {
		return fmt.Errorf("hash Creator-local asset: %w", err)
	}
	if !strings.EqualFold(actualSHA, strings.TrimSpace(sha)) || actualSize != sizeBytes {
		return fmt.Errorf("Creator-local asset does not match certified hash/size")
	}
	// The availability probe above already missed (otherwise this function
	// returned), so the upload must not repeat the same HEAD before the PUT.
	return publishFinalJobVerifiedObjectAfterMiss(ctx, path, sha, sizeBytes)
}

// publishFinalJobVerifiedObject uploads bytes whose SHA-256 and size the caller
// computed from this exact file in this process, immediately before the call.
// Re-hashing here would read and digest the same fragment a second time for no
// new information: the fragment was just produced and nobody else can write it.
// Every path that does NOT already hold that identity goes through
// publishFinalJobLocalObject, which always re-hashes.
func publishFinalJobVerifiedObject(ctx context.Context, path, sha string, sizeBytes int64) error {
	if err := ensureFinalJobObjectStored(ctx, strings.ToLower(sha), sizeBytes); err == nil {
		return nil
	}
	return publishFinalJobVerifiedObjectAfterMiss(ctx, path, sha, sizeBytes)
}

// publishFinalJobVerifiedObjectAfterMiss uploads when the caller has already
// probed the address and did not find the object. The probe is advisory: a
// non-200 answer is treated as a miss on this path, exactly as before, so a
// transient HEAD failure degrades to an idempotent PUT rather than a failed
// publication.
func publishFinalJobVerifiedObjectAfterMiss(ctx context.Context, path, sha string, sizeBytes int64) error {
	storeURL := finalJobPrivateObjectStoreURL()
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Creator-local asset for Objectstore upload: %w", err)
	}
	defer func() { _ = file.Close() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, storeURL+"/objects/"+strings.ToLower(sha), file)
	if err != nil {
		return fmt.Errorf("build Objectstore upload: %w", err)
	}
	req.ContentLength = sizeBytes
	if token := strings.TrimSpace(os.Getenv("OBJECTSTORE_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := finalJobObjectStoreUploadClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload Creator-local asset to Objectstore: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
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
	return signStoredFinalJobObject(preferredID, sha, sha, sizeBytes)
}

// PublishFinalJobLocalObjectVerified publishes a file whose SHA-256 and size the
// caller just computed from that file itself (a freshly encoded fragment, or a
// cache hit re-hashed on the way in). It exists so one fragment is hashed once
// instead of twice; any caller that cannot point at the exact bytes it hashed
// must use PublishFinalJobLocalObject, which always verifies.
func PublishFinalJobLocalObjectVerified(ctx context.Context, preferredID, path, sha string, sizeBytes int64) (map[string]any, error) {
	if err := publishFinalJobVerifiedObject(ctx, path, sha, sizeBytes); err != nil {
		return nil, err
	}
	return signStoredFinalJobObject(preferredID, sha, sha, sizeBytes)
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
	var ref map[string]any
	var err error
	if finalJobObjectStoreConfigured() {
		// A queue artifact that names its remote locator is already certified
		// and uploaded by the render boundary: sign that identity without a
		// second HEAD. An artifact with no locator (a legacy or Drive-only
		// producer) has not proven the object exists, so it keeps the
		// fail-closed availability check before signing.
		storageKey := strings.ToLower(strings.TrimSpace(artifact.StorageKey))
		if storageKey == "" {
			storageKey = strings.ToLower(strings.TrimSpace(artifact.SHA256))
		}
		if strings.TrimSpace(artifact.StorageKey) == "" {
			if err := ensureFinalJobObjectStored(ctx, strings.ToLower(strings.TrimSpace(artifact.SHA256)), artifact.SizeBytes); err != nil {
				return nil, err
			}
		}
		ref, err = signCertifiedFinalJobObject(storageKey, artifact.SHA256, artifact.SizeBytes)
	} else {
		driveID := strings.TrimSpace(artifact.DriveFileID)
		if driveID == "" || len(strings.TrimSpace(artifact.SHA256)) != 64 || artifact.SizeBytes <= 0 {
			return nil, fmt.Errorf("render artifact is not certified for final-job delivery")
		}
		ref = map[string]any{"asset_id": firstFinalJobValue(artifact.ID, driveID), "drive_file_id": driveID,
			"url": driveFileWebLink(driveID), "sha256": strings.TrimSpace(artifact.SHA256), "size_bytes": artifact.SizeBytes}
	}
	if err != nil {
		return nil, err
	}
	for key, value := range map[string]any{
		"duration_us": artifact.DurationUS, "frame_count": artifact.FrameCount,
		"width": artifact.Width, "height": artifact.Height, "fps_num": artifact.FPSNum, "fps_den": artifact.FPSDen,
		"profile_id": artifact.ProfileID, "copy_eligible": artifact.CopyEligible,
		"first_frame_keyframe": artifact.FirstFrameKeyframe, "closed_gop": artifact.ClosedGOP,
	} {
		if value != 0 && value != "" && value != false {
			ref[key] = value
		}
	}
	return ref, nil
}
