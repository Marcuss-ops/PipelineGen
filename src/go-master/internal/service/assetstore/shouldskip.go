package assetstore

import (
	"context"
	"strings"

	"velox/go-master/internal/upload/drive"
)

func ShouldSkipExisting(ctx context.Context, asset ExistingAsset, policy ExistencePolicy, checker ChecksumChecker) (bool, string, error) {
	switch policy {
	case ExistencePolicyReplace:
		return false, "", nil
	case ExistencePolicySkip:
		if strings.TrimSpace(asset.DriveLink) != "" {
			return true, "existing drive link (skip strategy)", nil
		}
		if strings.TrimSpace(asset.LocalPath) != "" {
			return true, "existing local file (skip strategy)", nil
		}
		return false, "", nil
	case ExistencePolicyVerify:
		if strings.TrimSpace(asset.DriveLink) == "" && strings.TrimSpace(asset.LocalPath) == "" {
			return false, "", nil
		}
		if strings.TrimSpace(asset.FileHash) == "" {
			return true, "missing file hash for verification", nil
		}
		if md5 := drive.MD5FromMetadata(asset.Metadata); md5 != "" {
			if strings.EqualFold(md5, strings.TrimSpace(asset.FileHash)) {
				return true, "metadata md5 matches file hash", nil
			}
		}
		if checker != nil {
			remoteChecksum, err := checker.GetMD5Checksum(ctx, asset.DriveLink)
			if err != nil {
				return true, "failed to verify remote checksum", err
			}
			if strings.EqualFold(remoteChecksum, strings.TrimSpace(asset.FileHash)) {
				return true, "remote checksum matches file hash", nil
			}
		}
		return false, "", nil
	default:
		if strings.TrimSpace(asset.DriveLink) != "" {
			return true, "existing drive link (default strategy)", nil
		}
		return false, "", nil
	}
}
