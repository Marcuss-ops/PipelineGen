// Package adapters — youtube_sourcing_pg_adapter.go
//
// PostgreSQL-backed implementation of sourcing.ClipStorePort. The YouTube
// sourcing registrar reads existing media to dedupe registrations; after the
// September 2026 media cutover those rows live in the PostgreSQL media SSOT,
// so reading them through SQLite produced false "new clip" answers (duplicate
// processing) or not-found hydration for PG-committed assets.
//
// The legacy SQLite SourcingClipStoreAdapter remains only for the documented
// graceful-degrade path (media PostgreSQL disabled).
package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// PGMediaSourcingLookup is the narrow PostgreSQL surface the sourcing port
// consumes. *pgmedia.MediaSearcher (the canonical media read authority)
// implements it.
type PGMediaSourcingLookup interface {
	FindClipIDByName(ctx context.Context, name string) (string, error)
	FindClipIDByYouTubeVideoID(ctx context.Context, videoID string, hasSegment bool, startSec, endSec float64) (string, error)
	FindClipIDBySourceURL(ctx context.Context, url string) (string, error)
	GetAsset(ctx context.Context, assetID string) (*pgmedia.MediaAssetRecord, error)
}

// SourcingClipStorePGAdapter implements sourcing.ClipStorePort against the
// PostgreSQL media SSOT.
type SourcingClipStorePGAdapter struct {
	media PGMediaSourcingLookup
}

var _ sourcing.ClipStorePort = (*SourcingClipStorePGAdapter)(nil)

// NewSourcingClipStorePGAdapter wires the adapter. A nil lookup returns nil
// (fail-closed: the caller keeps the legacy adapter only when PG is disabled).
func NewSourcingClipStorePGAdapter(media PGMediaSourcingLookup) *SourcingClipStorePGAdapter {
	if media == nil {
		return nil
	}
	return &SourcingClipStorePGAdapter{media: media}
}

func (a *SourcingClipStorePGAdapter) FindByName(ctx context.Context, name string) (string, error) {
	if a == nil || a.media == nil {
		return "", nil
	}
	return a.media.FindClipIDByName(ctx, name)
}

func (a *SourcingClipStorePGAdapter) FindExisting(ctx context.Context, videoID, url string, startSec, endSec float64) (string, error) {
	if a == nil || a.media == nil {
		return "", nil
	}
	hasSegment := endSec > startSec
	if videoID != "" {
		if id, err := a.media.FindClipIDByYouTubeVideoID(ctx, videoID, hasSegment, startSec, endSec); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	if url != "" && !hasSegment {
		if id, err := a.media.FindClipIDBySourceURL(ctx, url); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", nil
}

func (a *SourcingClipStorePGAdapter) GetClip(ctx context.Context, id string) (*sourcing.ExistingClip, error) {
	if a == nil || a.media == nil {
		return nil, nil
	}
	rec, err := a.media.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, pgmedia.ErrMediaAssetNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return existingClipFromRecord(rec), nil
}

// existingClipFromRecord maps a PostgreSQL media record onto the sourcing DTO.
// It is the single translation site so GetClip cannot drift from the legacy
// adapter's toExistingClip field coverage.
func existingClipFromRecord(rec *pgmedia.MediaAssetRecord) *sourcing.ExistingClip {
	if rec == nil {
		return nil
	}
	// Segment bounds: prefer the canonical start_ms/end_ms columns; fall back
	// to the metadata start_sec/end_sec mirrors for rows written before the
	// column projection existed.
	startSec := rec.MetadataFloat("start_sec")
	if rec.StartMS != 0 {
		startSec = float64(rec.StartMS) / 1000
	}
	endSec := rec.MetadataFloat("end_sec")
	if rec.EndMS != 0 {
		endSec = float64(rec.EndMS) / 1000
	}
	sourceURL := firstNonEmptyString(rec.SourceURL, rec.MetadataString("source_url"), rec.MetadataString("youtube_url"))
	sourceVideoID := firstNonEmptyString(rec.SourceVideoID, rec.YouTubeVideoID, rec.MetadataString("source_video_id"))
	provider := firstNonEmptyString(rec.SourceProvider, rec.MetadataString("source_provider"), rec.Source)
	return &sourcing.ExistingClip{
		ID:              rec.ID,
		Name:            rec.Name,
		Filename:        rec.Filename,
		Duration:        time.Duration(rec.DurationMS) * time.Millisecond,
		Source:          rec.Source,
		SourceURL:       sourceURL,
		SourceProvider:  provider,
		SourceVideoID:   sourceVideoID,
		StartSec:        startSec,
		EndSec:          endSec,
		Category:        rec.Category,
		Tags:            append([]string(nil), rec.Tags...),
		LocalPath:       rec.LocalPath,
		DriveLink:       rec.DriveLink,
		DriveFileID:     rec.DriveFileID,
		LegacyFileMD5:   rec.SHA256,
		Summary:         rec.MetadataString("clip_summary"),
		Topics:          rec.MetadataStringSlice("topics"),
		Speakers:        rec.MetadataStringSlice("speakers"),
		MentionedPeople: rec.MetadataStringSlice("mentioned_people"),
		Hook:            rec.MetadataString("hook"),
	}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
