package assets

import (
	"path/filepath"
	"strconv"
	"strings"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/repository/assettree"
	"velox/go-master/pkg/models"
)

// clipToAssetNode converts a models.Clip to assettree.AssetNode for unified tree handling.
func clipToAssetNode(clip *models.Clip) *assettree.AssetNode {
	nodeType := "file"
	if clip.IsFolder {
		nodeType = "folder"
	} else if clip.MediaType != "" {
		nodeType = clip.MediaType
	}

	return &assettree.AssetNode{
		ID:          clip.ID,
		Source:      clip.Source,
		AssetID:     clip.ID,
		Name:        clip.Name,
		Type:        nodeType,
		ParentID:    clip.FolderID,
		Path:        clip.FolderPath,
		Depth:       clip.Depth,
		IsFolder:    clip.IsFolder,
		DriveFileID: clip.DriveFileID,
		DriveLink:   clip.DriveLink,
		Metadata:    clip.Metadata,
		CreatedAt:   clip.CreatedAt,
		UpdatedAt:   clip.UpdatedAt,
	}
}

// voiceoverRecordToClip converts a voiceover.Record to models.Clip for unified handling.
func voiceoverRecordToClip(rec *voiceovers.Record) *models.Clip {
	name := rec.Filename
	if name == "" {
		name = rec.TextPreview
		if len(name) > 50 {
			name = name[:50]
		}
	}
	return &models.Clip{
		ID:           rec.ID,
		Name:         name,
		Filename:     rec.Filename,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		LocalPath:    rec.LocalPath,
		Source:       "voiceover",
		Metadata:     rec.Metadata,
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
	}
}

// imageAssetToClip converts a models.ImageAsset to models.Clip for unified handling.
func imageAssetToClip(img *models.ImageAsset) *models.Clip {
	name := img.Description
	if name == "" {
		name = filepath.Base(img.PathRel)
	}
	return &models.Clip{
		ID:        strconv.FormatInt(img.ID, 10),
		Name:      name,
		Filename:  filepath.Base(img.PathRel),
		DriveLink: img.SourceURL,
		FileHash:  img.Hash,
		LocalPath: img.PathRel,
		Source:    "images",
		CreatedAt: img.CreatedAt,
	}
}

// resolveRepo returns the appropriate repository based on source.
func (h *Handler) resolveRepo(source string) *clips.Repository {
	switch strings.ToLower(source) {
	case "artlist":
		return h.artlistRepo
	case "youtube", "clips", "boxe", "wwe", "discovery", "music":
		return h.clipsRepo
	case "stock":
		return h.stockRepo
	default:
		return nil
	}
}

// resolveVoiceoverRepo returns the voiceover repository if source matches.
func (h *Handler) resolveVoiceoverRepo(source string) *voiceovers.Repository {
	if strings.ToLower(source) == "voiceover" {
		return h.voiceoverRepo
	}
	return nil
}
