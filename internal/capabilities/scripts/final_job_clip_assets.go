package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

func finalJobRenderLanguage(req GenerateRequest, result *GenerateResult) (string, error) {
	if result == nil || len(result.LocalizedRenders) == 0 {
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
// is complete (drive file id + 64-char sha256 + positive duration): the runtime
// copies those bytes by identity and never re-renders them here.
func certifiedLocalizedRendersForScene(result *GenerateResult, sceneID, language string) []LocalizedRenderResult {
	if result == nil {
		return nil
	}
	var out []LocalizedRenderResult
	for _, rendered := range result.LocalizedRenders {
		if !strings.EqualFold(strings.TrimSpace(rendered.SceneID), sceneID) {
			continue
		}
		if language != "" && !strings.EqualFold(strings.TrimSpace(string(rendered.Language)), language) {
			continue
		}
		if !localizedRenderIsCertifiedClip(rendered) {
			continue
		}
		out = append(out, rendered)
	}
	return out
}

// certifiedLocalizedRenderForClip is the fixed-media projection: one certified
// render for the exact (scene, clip, language) unit, or none.
func certifiedLocalizedRenderForClip(result *GenerateResult, sceneID, clipID, language string) (LocalizedRenderResult, bool) {
	if result == nil || strings.TrimSpace(clipID) == "" {
		return LocalizedRenderResult{}, false
	}
	for _, rendered := range certifiedLocalizedRendersForScene(result, sceneID, language) {
		if strings.EqualFold(strings.TrimSpace(rendered.ClipID), strings.TrimSpace(clipID)) {
			return rendered, true
		}
	}
	return LocalizedRenderResult{}, false
}

func localizedRenderIsCertifiedClip(rendered LocalizedRenderResult) bool {
	locator := strings.TrimSpace(rendered.DriveFileID) != ""
	if finalJobObjectStoreConfigured() {
		locator = strings.TrimSpace(rendered.LocalPath) != ""
	}
	return locator && len(strings.TrimSpace(rendered.SHA256)) == 64 && rendered.DurationMS > 0
}

// clipSceneRuntimeAsset builds the runtime asset reference of a clip-only scene
// from its certified localized render. A scene without a certified render — or
// a scene that produced more than one — fails closed: the clip-only contract is
// "send the clip this pipeline produced", so there is no fallback to the
// unmodified source clip. The payload builder may repeat this certified visual
// to fill the longer canonical narration duration.
func clipSceneRuntimeAsset(ctx context.Context, resolver FinalJobAssetResolver, result *GenerateResult, renderLanguage string, renderLanguageErr error, sceneID string) (map[string]any, int64, error) {
	if renderLanguageErr != nil {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q: %w", sceneID, renderLanguageErr)
	}
	rendered := certifiedLocalizedRendersForScene(result, sceneID, renderLanguage)
	if len(rendered) == 0 {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q has no certified rendered clip for language %q: refusing to hand the unmodified source clip to the runtime", sceneID, renderLanguage)
	}
	if len(rendered) > 1 {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q has %d certified rendered clips for language %q: a clip-only final job sends exactly one rendered clip per scene", sceneID, len(rendered), renderLanguage)
	}
	return renderedClipAssetRef(ctx, resolver, rendered[0], sceneID)
}

// resolveFinalJobFixedMediaAsset prefers the certified render of one fixed
// (intro/outro) clip over the unmodified library clip, and keeps the library
// asset as the fallback: a fixed section may legitimately be declared without a
// render lane, which is the pre-existing behaviour for protected intros.
func resolveFinalJobFixedMediaAsset(ctx context.Context, resolver FinalJobAssetResolver, result *GenerateResult, sceneID, clipID, renderLanguage string) (map[string]any, error) {
	if renderLanguage != "" {
		if rendered, ok := certifiedLocalizedRenderForClip(result, sceneID, clipID, renderLanguage); ok {
			asset, _, err := renderedClipAssetRef(ctx, resolver, rendered, sceneID)
			return asset, err
		}
	}
	return resolveFinalJobAsset(ctx, resolver, clipID)
}

// renderedClipAssetRef projects a certified localized render into the runtime
// asset reference the remote scene copies. The key set matches the reference
// ResolveFinalJobAsset produces, so the remote sees one asset shape regardless
// of whether the bytes came from the media library or from this render lane.
func renderedClipAssetRef(ctx context.Context, resolver FinalJobAssetResolver, rendered LocalizedRenderResult, sceneID string) (map[string]any, int64, error) {
	if !finalJobObjectStoreConfigured() {
		driveID := strings.TrimSpace(rendered.DriveFileID)
		if driveID == "" || len(strings.TrimSpace(rendered.SHA256)) != 64 || rendered.DurationMS <= 0 {
			return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q is not certified", sceneID, rendered.AssetID)
		}
		size, err := resolver.FinalJobPublishedFileSize(ctx, driveID)
		if err != nil {
			return nil, 0, err
		}
		assetID := firstFinalJobValue(rendered.AssetID, driveID)
		return map[string]any{"asset_id": assetID, "drive_file_id": driveID, "url": driveFileWebLink(driveID),
			"sha256": strings.TrimSpace(rendered.SHA256), "size_bytes": size, "duration_ms": rendered.DurationMS}, rendered.DurationMS, nil
	}
	driveID := strings.TrimSpace(rendered.DriveFileID)
	sha := strings.TrimSpace(rendered.SHA256)
	if (!finalJobObjectStoreConfigured() && driveID == "") || len(sha) != 64 || rendered.DurationMS <= 0 {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q is not a certified MP4 (64-char sha256 and positive duration required)", sceneID, rendered.AssetID)
	}
	if finalJobObjectStoreConfigured() {
		info, err := os.Stat(strings.TrimSpace(rendered.LocalPath))
		if err != nil {
			return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q has no Creator-local source for Objectstore publication: %w", sceneID, rendered.AssetID, err)
		}
		if err := publishFinalJobLocalObject(ctx, rendered.LocalPath, sha, info.Size()); err != nil {
			return nil, 0, fmt.Errorf("publish final_job scene %q rendered clip to Objectstore: %w", sceneID, err)
		}
		asset, err := finalJobObjectStoreRef(ctx, "", sha, sha, info.Size())
		if err != nil {
			return nil, 0, err
		}
		asset["duration_ms"] = rendered.DurationMS
		return asset, rendered.DurationMS, nil
	}
	size, err := resolver.FinalJobPublishedFileSize(ctx, driveID)
	if err != nil {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %s: %w", sceneID, driveID, err)
	}
	if size <= 0 {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %s has no published byte size", sceneID, driveID)
	}
	assetID := strings.TrimSpace(rendered.AssetID)
	if assetID == "" {
		assetID = driveID
	}
	return map[string]any{
		"asset_id": assetID, "drive_file_id": driveID, "url": driveFileWebLink(driveID),
		"sha256": sha, "size_bytes": size, "duration_ms": rendered.DurationMS,
	}, rendered.DurationMS, nil
}
