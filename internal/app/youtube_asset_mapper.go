// Package app — asset mapping helpers extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

func fromExistingClip(c *sourcing.ExistingClip) *asset.Asset {
	if c == nil {
		return nil
	}
	out := &asset.Asset{
		ID:       c.ID,
		Name:     c.Name,
		Filename: c.Filename,
		Source:   asset.Source(c.Source),
		Category: c.Category,
		Tags:     append([]string(nil), c.Tags...),
		Duration: c.Duration,
	}
	out.SetLocalPath(c.LocalPath)
	out.SetDriveLink(c.DriveLink)
	out.SetDriveFileID(c.DriveFileID)
	out.SetFileHash(c.FileHash)
	return out
}

func toExistingClip(c *asset.Asset) *sourcing.ExistingClip {
	if c == nil {
		return nil
	}
	return &sourcing.ExistingClip{
		ID:          c.ID,
		Name:        c.Name,
		Filename:    c.Filename,
		Duration:    c.Duration,
		Source:      string(c.Source),
		Category:    c.Category,
		Tags:        append([]string(nil), c.Tags...),
		LocalPath:   c.LocalPath(),
		DriveLink:   c.DriveLink(),
		DriveFileID: c.DriveFileID(),
		FileHash:    c.FileHash(),
	}
}
