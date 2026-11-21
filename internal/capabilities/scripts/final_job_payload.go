package scriptgeneration

import segmentcontract "github.com/Marcuss-ops/PipelineGen/internal/kernel/media/finaljob"

import (
	"context"
	"fmt"

	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Public aliases preserve the scripts API while the versioned wire contract
// and its validation live in the finaljob boundary package.
type FinalJobEnvelopeV1 = finaljob.EnvelopeV1
type FinalJobCompiledPlanV2 = finaljob.CompiledPlanV2
type FinalJobPrepareV1 = finaljob.PrepareV1
type FinalJobFinalizeV1 = finaljob.FinalizeV1
type FinalJobRuntimeAssetV1 = finaljob.RuntimeAssetV1
type FinalJobRuntimePayloadV1 = finaljob.RuntimePayloadV1
type FinalJobRuntimeAudioV1 = finaljob.RuntimeAudioV1

// The segment DTOs and the port are owned by the kernel: the platform adapter
// implements the port without importing this business package (RA-15).
type FinalJobVideoSegmentRequest = segmentcontract.VideoSegmentRequest
type FinalJobVideoSegmentPreparer = segmentcontract.VideoSegmentPreparer

const FinalJobEnvelopeSchemaV1 = finaljob.EnvelopeSchemaV1

// BuildFinalJobEnvelopeV1 builds and validates the typed handoff before the
// wiring adapter serializes it for the deployed Master contract.
func BuildFinalJobEnvelopeV1(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult, resolver FinalJobAssetResolver, preparers ...FinalJobVideoSegmentPreparer) (FinalJobEnvelopeV1, error) {
	prepareMap, finalizeMap, err := buildFinalJobPayloads(ctx, runID, req, result, resolver, preparers...)
	if err != nil {
		return FinalJobEnvelopeV1{}, err
	}
	return finaljob.BuildEnvelope(runID, prepareMap, finalizeMap)
}

type FinalJobStockFile struct{ ID, Name string }

type FinalJobAssetResolver interface {
	ResolveFinalJobAsset(context.Context, string) (FinalJobRuntimeAssetV1, error)
	// FinalJobPublishedFileSize reports the byte size of a published Drive
	// file. A clip-only scene is sent as the CERTIFIED localized render this
	// pipeline produced (background, watermark and burnt subtitles included),
	// whose bytes already live in Drive: the run result carries its identity
	// and duration, but not its size, so the reference is completed here from
	// the same Drive metadata every other runtime asset uses.
	FinalJobPublishedFileSize(context.Context, string) (int64, error)
}

// finalJobStockAssetResolver is the sole Drive-backed source path in the final
// job. Other scene and generated assets use ResolveFinalJobAsset's HTTPS flow.
type finalJobStockAssetResolver interface {
	ResolveFinalJobStockAsset(context.Context, string) (FinalJobRuntimeAssetV1, error)
}

func resolveFinalJobStockAsset(ctx context.Context, resolver FinalJobAssetResolver, id string) (map[string]any, error) {
	if stockResolver, ok := resolver.(finalJobStockAssetResolver); ok {
		asset, err := stockResolver.ResolveFinalJobStockAsset(ctx, id)
		if err != nil {
			return nil, err
		}
		return asset.Map()
	}
	return resolveFinalJobAsset(ctx, resolver, id)
}

// finalJobStockBindingAsset resolves the stock reference. The binding must
// name the exact clip or Drive file selected for the scene; a folder alone is
// not a concrete visual and cannot be handed to the render worker.
func finalJobStockBindingAsset(ctx context.Context, resolver FinalJobAssetResolver, binding *scriptpkg.StockBinding) (map[string]any, int64, error) {
	assetID := strings.TrimSpace(binding.AssetID)
	driveLink := strings.TrimSpace(binding.DriveLink)
	if assetID == "" && driveLink == "" {
		if folderID := strings.TrimSpace(binding.FolderID); folderID != "" {
			return nil, 0, fmt.Errorf("folder-only stock binding %q has no selected clip file; provide asset_id or drive_link", folderID)
		}
		return nil, 0, fmt.Errorf("stock binding has neither a Drive file nor a folder")
	}
	driveID := assetID
	if driveLink != "" {
		if id, err := urlutil.FileIDFromDriveLink(driveLink); err == nil && strings.TrimSpace(id) != "" {
			driveID = id
		}
	}
	windowMS := binding.DurationMs
	if windowMS <= 0 && binding.EndMs > binding.StartMs {
		windowMS = binding.EndMs - binding.StartMs
	}
	if driveID == "" {
		return nil, 0, fmt.Errorf("stock binding has no usable Drive file identity")
	}
	asset, err := resolveFinalJobStockAsset(ctx, resolver, driveID)
	if err != nil {
		return nil, 0, err
	}
	if asset == nil {
		return nil, 0, fmt.Errorf("stock binding %q resolved no metadata", driveID)
	}
	if windowMS <= 0 {
		windowMS = finalJobInt64(asset["duration_ms"])
	}
	if windowMS <= 0 {
		return nil, 0, fmt.Errorf("stock binding %q has no positive window duration", driveID)
	}
	asset["start_ms"] = binding.StartMs
	return asset, windowMS, nil
}

func hasFinalJobConcreteStockAsset(binding *scriptpkg.StockBinding) bool {
	return binding != nil && (strings.TrimSpace(binding.AssetID) != "" || strings.TrimSpace(binding.DriveLink) != "")
}

func hasFinalJobStockBinding(binding *scriptpkg.StockBinding) bool {
	return binding != nil && (strings.TrimSpace(binding.FolderID) != "" || hasFinalJobConcreteStockAsset(binding))
}

// finalJobStockPoolFolder selects a shared Drive folder for stock-only requests
// or for explicitly marked stock-pool slots in mixed-media timelines.
func finalJobStockPoolFolder(req GenerateRequest) (string, error) {
	folderID := ""
	for _, binding := range req.StockBindings {
		if req.MediaMode != scriptpkg.MediaModeStockOnly && !binding.PoolSlot {
			continue
		}
		candidate := strings.TrimSpace(binding.FolderID)
		if candidate == "" && strings.TrimSpace(binding.FolderLink) != "" {
			candidate = urlutil.FolderIDFromDriveLink(binding.FolderLink)
		}
		if candidate == "" {
			continue
		}
		if folderID != "" && folderID != candidate {
			return "", fmt.Errorf("final_job stock pool request references multiple stock folders (%q and %q)", folderID, candidate)
		}
		folderID = candidate
	}
	return folderID, nil
}

func BuildFinalJobPayloads(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult, resolver FinalJobAssetResolver, preparers ...FinalJobVideoSegmentPreparer) (map[string]any, map[string]any, error) {
	return buildFinalJobPayloads(ctx, runID, req, result, resolver, preparers...)
}

// NormalizeFinalJobHTTPSRuntimeAssets validates the remote source locator for
// each asset and keeps the worker-facing URL in its internal-reference form.
// Stock retains its Drive identity; other assets use their content-addressed
// Objectstore identity.
func NormalizeFinalJobHTTPSRuntimeAssets(assets []any) error {
	for i, raw := range assets {
		asset, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("final_job runtime asset %d has an invalid shape", i)
		}
		source := strings.TrimSpace(finalJobStringValue(asset["source_uri"]))
		if source == "" {
			source = strings.TrimSpace(finalJobStringValue(asset["url"]))
		}
		if !strings.HasPrefix(strings.ToLower(source), "https://") {
			return fmt.Errorf("final_job runtime asset %q has no HTTPS source_uri", finalJobStringValue(asset["asset_id"]))
		}
		assetID := strings.TrimSpace(finalJobStringValue(asset["asset_id"]))
		if assetID == "" {
			return fmt.Errorf("final_job runtime asset %d has no immutable asset_id", i)
		}
		asset["source_uri"] = source
		if strings.EqualFold(finalJobStringValue(asset["role"]), "stock") {
			driveID := strings.TrimSpace(finalJobStringValue(asset["drive_file_id"]))
			if driveID == "" {
				return fmt.Errorf("final_job stock runtime asset %q has no Drive identity", assetID)
			}
			asset["url"] = "velox-drive://" + driveID
		} else {
			delete(asset, "drive_file_id")
			asset["url"] = "velox-asset://" + assetID
		}
	}
	return nil
}

// exposeFinalJobSourceURLs keeps the canonical asset identity in asset_id and
// sha256 while also placing the already-authorized HTTPS locator in url. Older
// Master 51 deployments resolve worker sources from url and otherwise fall
// back to their local asset bridge; the newer source_uri field remains present
// as the canonical transport field.
func exposeFinalJobSourceURLs(assets []any) {
	for _, raw := range assets {
		asset, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		source := strings.TrimSpace(finalJobStringValue(asset["source_uri"]))
		if strings.HasPrefix(strings.ToLower(source), "https://") {
			asset["url"] = source
		}
	}
}

func buildFinalJobPayloads(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult, resolver FinalJobAssetResolver, preparers ...FinalJobVideoSegmentPreparer) (map[string]any, map[string]any, error) {
	stockPoolFolderID, hasFinalAudio, imageRequested, err := finalJobPreflight(ctx, runID, req, result)
	if err != nil {
		return nil, nil, err
	}
	builder := newFinalJobSceneBuilder(ctx, req, result, resolver, stockPoolFolderID)
	if err := builder.appendSegments(); err != nil {
		return nil, nil, err
	}
	return finalJobFinalizePayload(ctx, runID, req, result, builder, preparers, hasFinalAudio, imageRequested)
}

// Package-local aliases over the one coercion family. They keep the existing
// final-job call sites readable without maintaining a second implementation.
var (
	finalJobInt64       = finaljob.Int64
	finalJobStringValue = finaljob.String
	boolValue           = finaljob.Bool
)

func finalJobInt(value any) int {
	return int(finaljob.Int64(value))
}
