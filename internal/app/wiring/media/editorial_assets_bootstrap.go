// Package media — editorial_assets_bootstrap.go owns the idempotent
// registration of the curated editorial assets (the video background plates)
// into the PostgreSQL media SSOT, under their canonical alias.
//
// # WHY THIS EXISTS
//
// clip.render resolves a plate by its alias as the registry asset id
// (`media_assets.id = 'drive-background-03'`, see
// docs/operations/editorial-assets.md). The registry
// (internal/capabilities/mediaregistry/editorial_backgrounds.go) owns WHICH
// plates exist, their Drive identity and their CERTIFIED normalized bytes;
// the media SSOT owns the row the resolver reads. Nothing used to bridge the
// two, so a clean database (a fresh E2E database, a new deployment) had no
// plate rows at all and the only way to make a render work was a hand-written
// INSERT — which also happily wrote an arbitrary content hash and silently
// bypassed mediaregistry.ValidateEditorialBackgroundIdentity.
//
// This bootstrap is that bridge, and it is deliberately the ONLY writer of a
// plate row: it derives every field from the registry, so the registered
// content hash IS the certified hash and the read-boundary fail-closed check
// can never disagree with the catalog.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// EditorialAssetSource is the provenance value written for a catalog-owned
// editorial asset. It is not a Drive source and carries no source URI, so the
// committer's RegisterSource step is guard-skipped (see
// PostgresMediaCommitter.commitTx).
const EditorialAssetSource = "editorial"

// EditorialAssetRegistration is one plate's registration outcome.
type EditorialAssetRegistration struct {
	AssetID     string
	DriveFileID string
	SHA256      string
	// LocalPath is the registered local copy, or "" when no fixture is
	// available locally and the plate must be fetched from Drive.
	LocalPath string
}

// EditorialBackgroundsDirEnv names the environment variable that points at the
// directory holding the certified plate fixtures. When unset (or when the file
// is absent) the plate is registered WITHOUT a local path: the canonical
// materializer then fetches it from Drive and verifies the certified hash.
const EditorialBackgroundsDirEnv = "VELOX_EDITORIAL_BACKGROUNDS_DIR"

// editorialAssetCommitRequest builds the canonical commit request for one
// plate. It is pure so the mapping is unit-testable without a database, and it
// is the single place that decides which registry field feeds which column:
//
//	id                 ← plate.ID              (the alias IS the asset id)
//	content_sha256     ← plate.SHA256          (certified normalized bytes)
//	drive_file_id      ← plate.DriveFileID
//	local_path         ← <fixtures>/<Filename> when present, else ""
//
// EmitIndexEvent stays false: a plate is a rendering input reached by alias,
// not a searchable corpus asset, so it must not enqueue vector indexing work.
func editorialAssetCommitRequest(plate mediaregistry.EditorialBackgroundAsset, localPath string) persistence.CommitRequest {
	return persistence.CommitRequest{
		AssetID:        plate.ID,
		Source:         EditorialAssetSource,
		Name:           plate.ID,
		Filename:       plate.Filename,
		MediaType:      "video",
		Category:       "editorial-background",
		DurationMs:     int64(mediaregistry.EditorialBackgroundDurationSecs) * 1000,
		ContentHash:    plate.SHA256,
		Description:    fmt.Sprintf("Curated editorial video background plate %s (%s)", plate.ID, mediaregistry.EditorialBackgroundContract),
		LifecycleState: "ACTIVE",
		LocalPath:      localPath,
		IndexState:     "DISCOVERED",
		Title:          plate.ID,
		Metadata: persistence.TypedMetadata{
			Title:       plate.ID,
			Description: fmt.Sprintf("Curated editorial video background plate (role=%s)", plate.Role),
			Extra: map[string]any{
				"editorial_role":     plate.Role,
				"editorial_contract": mediaregistry.EditorialBackgroundContract,
				"media_type":         plate.MediaType,
				"width":              mediaregistry.EditorialBackgroundWidth,
				"height":             mediaregistry.EditorialBackgroundHeight,
				"fps":                mediaregistry.EditorialBackgroundFPS,
				"audio_streams":      mediaregistry.EditorialBackgroundAudioStreams,
			},
		},
		Locations: []persistence.LocationCommit{{
			Kind:       "drive",
			Provider:   "drive",
			ExternalID: plate.DriveFileID,
			IsPrimary:  true,
		}},
		EmitIndexEvent: false,
	}
}

// EnsureEditorialAssets registers every curated background plate in the
// PostgreSQL media SSOT. It is idempotent: the canonical commit upserts on
// media_assets.id, so a re-run refreshes the certified hash / Drive identity
// and converges instead of duplicating.
//
// platesDir is the directory holding the certified plate fixtures. When empty,
// or when the fixture file is absent, the plate is registered without a local
// path and the canonical materializer will fetch it from Drive. A relative
// path is resolved against the process working directory by the caller's
// choice — this function never invents a repository location.
func EnsureEditorialAssets(ctx context.Context, db *sql.DB, platesDir string, log *zap.Logger) ([]EditorialAssetRegistration, error) {
	if db == nil {
		return nil, fmt.Errorf("editorial assets bootstrap: db is required")
	}
	if err := mediaregistry.ValidateEditorialBackgroundCatalog(); err != nil {
		return nil, fmt.Errorf("editorial assets bootstrap: %w", err)
	}
	committer, err := NewPostgresMediaCommitterFromDB(db, log)
	if err != nil {
		return nil, err
	}

	plates := mediaregistry.EditorialBackgroundAssets()
	out := make([]EditorialAssetRegistration, 0, len(plates))
	for _, plate := range plates {
		localPath := resolvePlateFixture(platesDir, plate.Filename)
		if _, err := committer.CommitAndIndex(ctx, editorialAssetCommitRequest(plate, localPath)); err != nil {
			return nil, fmt.Errorf("editorial assets bootstrap: register %s: %w", plate.ID, err)
		}
		out = append(out, EditorialAssetRegistration{
			AssetID:     plate.ID,
			DriveFileID: plate.DriveFileID,
			SHA256:      plate.SHA256,
			LocalPath:   localPath,
		})
		if log != nil {
			log.Info("editorial asset registered",
				zap.String("asset_id", plate.ID),
				zap.String("drive_file_id", plate.DriveFileID),
				zap.String("sha256", plate.SHA256),
				zap.String("local_path", localPath))
		}
	}
	return out, nil
}

// resolvePlateFixture returns the absolute path of a plate fixture when it
// exists on disk, and "" otherwise.
func resolvePlateFixture(dir, filename string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	candidate := filepath.Join(dir, filename)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return ""
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return candidate
	}
	return absolute
}
