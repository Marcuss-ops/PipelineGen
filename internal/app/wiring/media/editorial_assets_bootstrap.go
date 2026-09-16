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
// plate row.
//
// # THE BYTES ARE THE AUTHORITY, NOT THE CATALOG FIELD
//
// Registering `content_sha256 = plate.SHA256` without ever reading the bytes
// reproduces exactly the failure this bootstrap exists to remove: the render
// resolver compares the registered hash against the file it hashes and fails
// closed, so a copied-but-unverified hash turns into a clip.render error at
// the worst possible moment. Every plate is therefore MATERIALIZED first —
// through the canonical content-addressed materializer, which hashes a
// registered local copy, a cached copy, or a freshly downloaded Drive file —
// and the verified digest is what gets committed. A mismatch is a hard error,
// never a warning, so a plate row can only ever be created from bytes that
// were actually read.
//
// # WRITE PATH
//
// The row is written exclusively through persistence.AssetCommitter
// (CommitAndIndex), the single media SSOT writer. There is no INSERT/UPDATE
// against media_assets in this file, and there must never be one.
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
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
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
	// SHA256 is the digest of the bytes that were READ and verified, which
	// equals the certified registry digest — or the bootstrap failed.
	SHA256 string
	// LocalPath is the verified local copy the digest was computed from.
	LocalPath string
	// SizeBytes is the verified byte count.
	SizeBytes int64
}

// EditorialAssetBytesSource materializes an asset's bytes and returns the
// digest of what it actually read. The production implementation is
// *drive.CanonicalAssetMaterializer; anything else is a test double.
type EditorialAssetBytesSource interface {
	Materialize(ctx context.Context, req drive.MaterializeRequest) (*drive.MaterializeResult, error)
}

// EditorialBackgroundsDirEnv names the environment variable that points at the
// directory holding the certified plate fixtures. It is an optimization, not
// the authority: when a fixture is absent the plate is fetched from Drive and
// verified against the same certified digest.
const EditorialBackgroundsDirEnv = "VELOX_EDITORIAL_BACKGROUNDS_DIR"

// EditorialAssetsOptions parameterizes one bootstrap run.
type EditorialAssetsOptions struct {
	// PlateIDs restricts the run to a subset of the canonical catalog and is
	// the canonical alias list, not a free-form id (an unknown alias is an
	// error). Empty means every plate in the catalog.
	PlateIDs []string
	// PlatesDir is the directory holding the certified plate fixtures. It is
	// passed to the materializer as a REGISTERED path: the bytes are hashed and
	// must match the certified digest, exactly like a downloaded Drive file.
	PlatesDir string
}

// editorialAssetCommitRequest builds the canonical commit request for one
// plate from its VERIFIED bytes. It is pure so the mapping is unit-testable
// without a database, and it is the single place that decides which value
// feeds which column:
//
//	id                 ← plate.ID            (the alias IS the asset id)
//	content_sha256     ← verifiedSHA256      (digest of the bytes just read)
//	drive_file_id      ← plate.DriveFileID
//	local_path         ← verifiedPath
//
// EmitIndexEvent stays false: a plate is a rendering input reached by alias,
// not a searchable corpus asset, so it must not enqueue vector indexing work.
func editorialAssetCommitRequest(plate mediaregistry.EditorialBackgroundAsset, verifiedPath, verifiedSHA256 string) persistence.CommitRequest {
	// The Drive location MUST carry the canonical `drive://<fileID>` URI, not
	// only the external id. Live observation (2026-09-16): a plate row existed
	// with uri = '' and is_primary = true — a primary locator every URI-based
	// reader is handed as an empty string, so the asset resolved to nothing
	// far away from the cause. URI and ExternalID are the two representations
	// of ONE locator; emitting one without the other is a half-written row.
	//
	// A plate with no Drive identity contributes NO location at all (the same
	// rule the YouTube clip writer applies in clipLocations): the absence
	// of a row is the honest representation of "no Drive identity", and no
	// reader has to distinguish "no row" from "row with nothing in it".
	locations := make([]persistence.LocationCommit, 0, 1)
	if driveFileID := strings.TrimSpace(plate.DriveFileID); driveFileID != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind:       "drive",
			Provider:   "drive",
			URI:        "drive://" + driveFileID,
			ExternalID: driveFileID,
			IsPrimary:  true,
		})
	}

	return persistence.CommitRequest{
		AssetID:        plate.ID,
		Source:         EditorialAssetSource,
		Name:           plate.ID,
		Filename:       plate.Filename,
		MediaType:      "video",
		Category:       "editorial-background",
		DurationMs:     int64(mediaregistry.EditorialBackgroundDurationSecs) * 1000,
		ContentHash:    verifiedSHA256,
		Description:    fmt.Sprintf("Curated editorial video background plate %s (%s)", plate.ID, mediaregistry.EditorialBackgroundContract),
		LifecycleState: "ACTIVE",
		LocalPath:      verifiedPath,
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
		Locations:      locations,
		EmitIndexEvent: false,
	}
}

// verifyPlateBytes is the gate that makes a plate row trustworthy: the
// materialized digest must be a canonical SHA-256 and must equal the digest
// the catalog certifies for this alias. It also re-runs the SAME check the
// clip.render resolver applies at read time
// (mediaregistry.ValidateEditorialBackgroundIdentity), so the bootstrap cannot
// register bytes the resolver would reject.
func verifyPlateBytes(plate mediaregistry.EditorialBackgroundAsset, materialized *drive.MaterializeResult) error {
	if materialized == nil {
		return fmt.Errorf("editorial assets bootstrap: %s produced no materialized bytes", plate.ID)
	}
	if strings.TrimSpace(materialized.LocalPath) == "" {
		return fmt.Errorf("editorial assets bootstrap: %s materialized no local path", plate.ID)
	}
	if materialized.SizeBytes <= 0 {
		return fmt.Errorf("editorial assets bootstrap: %s materialized a %d-byte file at %s", plate.ID, materialized.SizeBytes, materialized.LocalPath)
	}
	got := strings.ToLower(strings.TrimSpace(materialized.SHA256))
	if !digest.IsSHA256(got) {
		return fmt.Errorf("editorial assets bootstrap: %s materialized digest %q is not a canonical SHA-256", plate.ID, materialized.SHA256)
	}
	if got != plate.SHA256 {
		return fmt.Errorf("editorial assets bootstrap: %s bytes at %s have content hash %s, want the certified normalized plate hash %s",
			plate.ID, materialized.LocalPath, got, plate.SHA256)
	}
	if err := mediaregistry.ValidateEditorialBackgroundIdentity(plate.ID, got); err != nil {
		return fmt.Errorf("editorial assets bootstrap: %w", err)
	}
	return nil
}

// resolveEditorialPlates maps the requested aliases onto the canonical catalog
// in declaration order. An empty request means the whole catalog; an alias the
// catalog does not own is an error, never a silently skipped plate.
func resolveEditorialPlates(plateIDs []string) ([]mediaregistry.EditorialBackgroundAsset, error) {
	catalog := mediaregistry.EditorialBackgroundAssets()
	if len(plateIDs) == 0 {
		return catalog, nil
	}
	out := make([]mediaregistry.EditorialBackgroundAsset, 0, len(plateIDs))
	for _, requested := range plateIDs {
		plate, ok := mediaregistry.LookupEditorialBackground(requested)
		if !ok {
			return nil, fmt.Errorf("editorial assets bootstrap: %q is not a canonical editorial background", requested)
		}
		out = append(out, plate)
	}
	return out, nil
}

// EnsureEditorialAssets materializes, verifies and registers every requested
// plate in the PostgreSQL media SSOT.
//
// It is idempotent: the canonical commit upserts on media_assets.id, so a
// re-run refreshes the certified hash / Drive identity with bytes that were
// read and verified again, and converges instead of duplicating. Plates whose
// bytes cannot be materialized fail the whole run: registering a plate whose
// digest was never confirmed is the bug this function exists to prevent.
func EnsureEditorialAssets(
	ctx context.Context,
	db *sql.DB,
	bytes EditorialAssetBytesSource,
	options EditorialAssetsOptions,
	log *zap.Logger,
) ([]EditorialAssetRegistration, error) {
	if db == nil {
		return nil, fmt.Errorf("editorial assets bootstrap: db is required")
	}
	if bytes == nil {
		return nil, fmt.Errorf("editorial assets bootstrap: a byte source is required (the canonical Drive materializer)")
	}
	if err := mediaregistry.ValidateEditorialBackgroundCatalog(); err != nil {
		return nil, fmt.Errorf("editorial assets bootstrap: %w", err)
	}
	committer, err := NewPostgresMediaCommitterFromDB(db, log)
	if err != nil {
		return nil, err
	}
	plates, err := resolveEditorialPlates(options.PlateIDs)
	if err != nil {
		return nil, err
	}

	out := make([]EditorialAssetRegistration, 0, len(plates))
	for _, plate := range plates {
		// The fixtures directory is a CANDIDATE path, never a trusted one: the
		// materializer hashes it and must agree with the certified digest, and
		// falls through to the content-addressed cache or Drive when it is
		// absent.
		candidate := resolvePlateFixture(options.PlatesDir, plate.Filename)
		materialized, err := bytes.Materialize(ctx, drive.MaterializeRequest{
			AssetID:        plate.ID,
			DriveFileID:    plate.DriveFileID,
			ExpectedSHA256: plate.SHA256,
			Extension:      filepath.Ext(plate.Filename),
			RegisteredPath: candidate,
		})
		if err != nil {
			return nil, fmt.Errorf("editorial assets bootstrap: materialize %s: %w", plate.ID, err)
		}
		if err := verifyPlateBytes(plate, materialized); err != nil {
			return nil, err
		}

		request := editorialAssetCommitRequest(plate, materialized.LocalPath, materialized.SHA256)
		if _, err := committer.CommitAndIndex(ctx, request); err != nil {
			return nil, fmt.Errorf("editorial assets bootstrap: register %s: %w", plate.ID, err)
		}
		out = append(out, EditorialAssetRegistration{
			AssetID:     plate.ID,
			DriveFileID: plate.DriveFileID,
			SHA256:      materialized.SHA256,
			LocalPath:   materialized.LocalPath,
			SizeBytes:   materialized.SizeBytes,
		})
		if log != nil {
			log.Info("editorial asset registered",
				zap.String("asset_id", plate.ID),
				zap.String("drive_file_id", plate.DriveFileID),
				zap.String("sha256", materialized.SHA256),
				zap.String("local_path", materialized.LocalPath),
				zap.String("origin", materialized.OriginTag()),
				zap.Int64("size_bytes", materialized.SizeBytes))
		}
	}
	return out, nil
}

// resolvePlateFixture returns the absolute path of a plate fixture when it
// exists on disk, and "" otherwise. The result is only ever a CANDIDATE path
// handed to the materializer, which hashes whatever it finds there.
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
