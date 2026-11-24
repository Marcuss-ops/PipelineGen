package finaljob

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

func FinalJobRenderLanguage(req contract.GenerateRequest, result *contract.GenerateResult) (string, error) {
	if result == nil {
		return "", nil
	}
	// A final job with an explicit target language must use that language for
	// fixed-media subtitles and localized clip assets. The source render remains
	// available for source-only jobs, but must not override the requested target.
	for _, target := range req.Languages {
		preferred := strings.TrimSpace(string(target))
		if preferred == "" {
			continue
		}
		for _, rendered := range result.LocalizedRenders {
			if strings.EqualFold(strings.TrimSpace(string(rendered.Language)), preferred) {
				return strings.TrimSpace(string(rendered.Language)), nil
			}
		}
	}
	// Fixed-media subtitle renders are created after this choice. A singleton
	// target is therefore authoritative even when no localized clip render
	// exists yet; falling back to the source here burns the wrong language into
	// stock intros/outros.
	if len(req.Languages) == 1 {
		if target := strings.TrimSpace(string(req.Languages[0])); target != "" {
			return target, nil
		}
	}
	if len(result.LocalizedRenders) == 0 {
		return "", nil
	}
	preferred := strings.TrimSpace(string(req.SourceLanguage))
	if preferred != "" {
		for _, rendered := range result.LocalizedRenders {
			if strings.EqualFold(strings.TrimSpace(string(rendered.Language)), preferred) {
				return strings.TrimSpace(string(rendered.Language)), nil
			}
		}
	}
	languages := make(map[string]struct{}, len(result.LocalizedRenders))
	for _, rendered := range result.LocalizedRenders {
		if language := strings.TrimSpace(string(rendered.Language)); language != "" {
			languages[language] = struct{}{}
		}
	}
	if len(languages) == 1 {
		for language := range languages {
			return language, nil
		}
	}
	available := make([]string, 0, len(languages))
	for language := range languages {
		available = append(available, language)
	}
	sort.Strings(available)
	return "", fmt.Errorf("no certified render for source language %q and the run produced %d render languages (%s)", preferred, len(available), strings.Join(available, ", "))
}

// certifiedLocalizedRendersForScene returns the certified renders of one scene
// for one language. A render is certified only when its published MP4 identity
// is complete (drive file id + 64-char SHA256 + positive duration): the runtime
// copies those bytes by identity and never re-renders them here.
func certifiedLocalizedRendersForScene(result *contract.GenerateResult, SceneID, language string) []contract.LocalizedRenderResult {
	if result == nil {
		return nil
	}
	var out []contract.LocalizedRenderResult
	for _, rendered := range result.LocalizedRenders {
		if !strings.EqualFold(strings.TrimSpace(rendered.SceneID), SceneID) {
			continue
		}
		if language != "" && !strings.EqualFold(strings.TrimSpace(string(rendered.Language)), language) {
			continue
		}
		if !LocalizedRenderIsCertifiedClip(rendered) {
			continue
		}
		out = append(out, rendered)
	}
	return out
}

// CertifiedLocalizedRenderForClip is the fixed-media projection: one certified
// render for the exact (scene, clip, language) unit, or none.
func CertifiedLocalizedRenderForClip(result *contract.GenerateResult, SceneID, clipID, language string) (contract.LocalizedRenderResult, bool) {
	if result == nil || strings.TrimSpace(clipID) == "" {
		return contract.LocalizedRenderResult{}, false
	}
	for _, rendered := range certifiedLocalizedRendersForScene(result, SceneID, language) {
		if strings.EqualFold(strings.TrimSpace(rendered.ClipID), strings.TrimSpace(clipID)) {
			return rendered, true
		}
	}
	return contract.LocalizedRenderResult{}, false
}

func LocalizedRenderIsCertifiedClip(rendered contract.LocalizedRenderResult) bool {
	if len(strings.TrimSpace(rendered.SHA256)) != 64 || rendered.DurationMS <= 0 {
		return false
	}
	if !FinalJobObjectStoreConfigured() {
		return strings.TrimSpace(rendered.DriveFileID) != ""
	}
	// Objectstore mode: a local path is a convenience, not the certificate.
	// A render that stayed in the shared store is certified by its immutable
	// storage key/URL plus the byte size the render boundary measured.
	if strings.TrimSpace(rendered.LocalPath) != "" {
		return true
	}
	if rendered.SizeBytes <= 0 {
		return false
	}
	return strings.TrimSpace(rendered.StorageKey) != "" || strings.TrimSpace(rendered.ArtifactURL) != ""
}

// clipSceneRuntimeAsset builds the runtime asset reference of a clip-only scene
// from its certified localized render. A scene without a certified render — or
// a scene that produced more than one — fails closed: the clip-only contract is
// "send the clip this pipeline produced", so there is no fallback to the
// unmodified source clip. The payload builder may repeat this certified visual
// to fill the longer canonical narration duration.
func clipSceneRuntimeAsset(ctx context.Context, resolver FinalJobAssetResolver, result *contract.GenerateResult, renderLanguage string, renderLanguageErr error, SceneID string) (map[string]any, int64, error) {
	if renderLanguageErr != nil {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q: %w", SceneID, renderLanguageErr)
	}
	rendered := certifiedLocalizedRendersForScene(result, SceneID, renderLanguage)
	if len(rendered) == 0 {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q has no certified rendered clip for language %q: refusing to hand the unmodified source clip to the runtime", SceneID, renderLanguage)
	}
	if len(rendered) > 1 {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q has %d certified rendered clips for language %q: a clip-only final job sends exactly one rendered clip per scene", SceneID, len(rendered), renderLanguage)
	}
	return RenderedClipAssetRef(ctx, resolver, rendered[0], SceneID)
}

// resolveFinalJobFixedMediaAsset uses a localized subtitle render only for the
// exact fixed-media clip and language. The output is still one complete clip;
// source duration is retained in the final timeline and stock never reaches
// this path.
func resolveFinalJobFixedMediaAsset(ctx context.Context, resolver FinalJobAssetResolver, result *contract.GenerateResult, SceneID, clipID, renderLanguage string) (map[string]any, error) {
	source, err := resolveFinalJobAsset(ctx, resolver, clipID)
	if err != nil {
		return nil, err
	}
	sourceDurationMS := finalJobInt64(source["duration_ms"])
	if renderLanguage == "" {
		return source, nil
	}
	rendered, ok := CertifiedLocalizedRenderForClip(result, SceneID, clipID, renderLanguage)
	if !ok {
		return source, nil
	}
	if sourceDurationMS <= 0 {
		return nil, fmt.Errorf("final_job fixed clip %q has no certified source duration", clipID)
	}
	asset, renderedDurationMS, err := RenderedClipAssetRef(ctx, resolver, rendered, SceneID)
	if err != nil {
		return nil, err
	}
	// Container probes can round to a frame boundary. Reject a render that
	// actually shortened or extended the protected clip by more than 50 ms.
	delta := renderedDurationMS - sourceDurationMS
	if delta < -50 || delta > 50 {
		return nil, fmt.Errorf("final_job localized fixed clip %q changed duration from %dms to %dms; refusing a trimmed or extended subtitle render", clipID, sourceDurationMS, renderedDurationMS)
	}
	asset["duration_ms"] = sourceDurationMS
	return asset, nil
}

// RenderedClipAssetRef projects a certified localized render into the runtime
// asset reference the remote scene copies. The key set matches the reference
// ResolveFinalJobAsset produces, so the remote sees one asset shape regardless
// of whether the bytes came from the media library or from this render lane.
func RenderedClipAssetRef(ctx context.Context, resolver FinalJobAssetResolver, rendered contract.LocalizedRenderResult, SceneID string) (map[string]any, int64, error) {
	if !FinalJobObjectStoreConfigured() {
		driveID := strings.TrimSpace(rendered.DriveFileID)
		if driveID == "" || len(strings.TrimSpace(rendered.SHA256)) != 64 || rendered.DurationMS <= 0 {
			return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q is not certified", SceneID, rendered.AssetID)
		}
		size, err := resolver.FinalJobPublishedFileSize(ctx, driveID)
		if err != nil {
			return nil, 0, err
		}
		AssetID := firstFinalJobValue(rendered.AssetID, driveID)
		return map[string]any{"asset_id": AssetID, "drive_file_id": driveID, "url": driveFileWebLink(driveID),
			"sha256": strings.TrimSpace(rendered.SHA256), "size_bytes": size, "duration_ms": rendered.DurationMS}, rendered.DurationMS, nil
	}
	driveID := strings.TrimSpace(rendered.DriveFileID)
	sha := strings.TrimSpace(rendered.SHA256)
	if (!FinalJobObjectStoreConfigured() && driveID == "") || len(sha) != 64 || rendered.DurationMS <= 0 {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q is not a certified MP4 (64-char SHA256 and positive duration required)", SceneID, rendered.AssetID)
	}
	if FinalJobObjectStoreConfigured() {
		size := rendered.SizeBytes
		storageKey := strings.ToLower(strings.TrimSpace(rendered.StorageKey))
		if storageKey == "" {
			storageKey = sha
		}
		if storageKey != sha {
			return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q has storage key %q, want its SHA-256", SceneID, rendered.AssetID, storageKey)
		}
		if localPath := strings.TrimSpace(rendered.LocalPath); localPath != "" {
			// The Producer still owns the bytes: publish them once and use the
			// returned reference. Publish already verified the object it
			// uploaded, so asking for a second signature/HEAD here was a
			// duplicate round-trip on every clip.
			if size <= 0 {
				info, err := os.Stat(localPath)
				if err != nil {
					return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q has no Creator-local source for Objectstore publication: %w", SceneID, rendered.AssetID, err)
				}
				size = info.Size()
			}
			// The SHA-256 and size come from the render certificate for these
			// exact bytes (a fresh render or a cache hit that was re-hashed on
			// entry). Re-hashing the local file here would read the whole
			// fragment a second time; the verified publication keeps the
			// availability probe and lets the store reject a bad upload.
			asset, err := PublishFinalJobLocalObjectVerified(ctx, rendered.AssetID, localPath, sha, size)
			if err != nil {
				return nil, 0, fmt.Errorf("publish final_job scene %q rendered clip to Objectstore: %w", SceneID, err)
			}
			asset["duration_ms"] = rendered.DurationMS
			return asset, rendered.DurationMS, nil
		}
		// Remote-only: RenderingGen certified the upload (storage key,
		// SHA-256, size) and the bytes never came back to the Producer. Sign
		// that identity directly; there is no local file to verify and no
		// reason to download one.
		if size <= 0 {
			return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q is remote-only but carries no certified size", SceneID, rendered.AssetID)
		}
		if strings.TrimSpace(rendered.ArtifactURL) == "" && strings.TrimSpace(rendered.StorageKey) == "" {
			return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q has no certified locator", SceneID, rendered.AssetID)
		}
		asset, err := signCertifiedFinalJobObject(storageKey, sha, size)
		if err != nil {
			return nil, 0, err
		}
		asset["duration_ms"] = rendered.DurationMS
		return asset, rendered.DurationMS, nil
	}
	size, err := resolver.FinalJobPublishedFileSize(ctx, driveID)
	if err != nil {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %s: %w", SceneID, driveID, err)
	}
	if size <= 0 {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %s has no published byte size", SceneID, driveID)
	}
	AssetID := strings.TrimSpace(rendered.AssetID)
	if AssetID == "" {
		AssetID = driveID
	}
	return map[string]any{
		"asset_id": AssetID, "drive_file_id": driveID, "url": driveFileWebLink(driveID),
		"sha256": sha, "size_bytes": size, "duration_ms": rendered.DurationMS,
	}, rendered.DurationMS, nil
}
