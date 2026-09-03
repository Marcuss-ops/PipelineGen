// Package app — canonical_media_committer.go is the composition-root
// factory for every production asset writer.
//
// DEMOLITION COMPLETE (media cutover, September 2026): the SQLite media
// engine (SQLiteMediaCommitter + the SQLite media_assets write path) has
// been removed. The ONLY canonical media writer is PostgresMediaCommitter
// over the PostgreSQL + pgvector media SSOT.
//
// Degradation contract (graceful-degrade mode): when the media PostgreSQL
// handle is unavailable the factory returns (nil, nil) instead of a writer.
// Every caller MUST handle the nil writer by skipping the media-dependent
// wiring it feeds — media features simply do not register (godlike/07:
// degraded is honest; a fake writer would not be). A non-nil writer is
// ALWAYS a working PostgresMediaCommitter.
package wiring

import (
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

// newCanonicalAssetCommitter resolves the canonical media writer from an
// open media PostgreSQL handle. The database handle MUST come from
// RequireMediaPostgres / root.MediaPostgres — this constructor never opens
// connections itself and never consults SQLite.
//
// Returns (nil, nil) when db is nil: the composition-level signal that the
// media plane is degraded and media-consuming wiring must be skipped.
func newCanonicalAssetCommitter(db *sql.DB, log *zap.Logger) (persistence.CanonicalAssetWriter, error) {
	if db == nil {
		return nil, nil
	}
	return mediasub.NewPostgresMediaCommitterFromDB(db, log)
}

// canonicalCommitterForRoot resolves the canonical media writer from the
// composition root. root.MediaPostgres is opened FIRST in NewComposition
// (RequireMediaPostgres), so every wiring site sees a consistent engine.
//
// Returns (nil, nil) when the media plane is degraded — the caller skips
// its media wiring (modules that need media simply do not register).
func canonicalCommitterForRoot(root *ComposeRoot, log *zap.Logger) (persistence.CanonicalAssetWriter, error) {
	if root == nil || root.MediaPostgres == nil {
		return nil, nil
	}
	return mediasub.NewPostgresMediaCommitterFromDB(root.MediaPostgres, log)
}

// canonicalCommitterOrSkipped resolves the canonical writer for wiring
// sites that pass it into a bundle struct: the nil result is forwarded as
// a nil Committer and the consuming module decides whether that degrades
// the whole module or errors.
func canonicalCommitterOrSkipped(root *ComposeRoot, log *zap.Logger) persistence.CanonicalAssetWriter {
	w, err := canonicalCommitterForRoot(root, log)
	if err != nil {
		panic(fmt.Sprintf("canonical media committer: %v", err))
	}
	return w
}

// canonicalMediaWriterRequired is the fail-closed variant for wiring sites
// that CANNOT degrade (the writer is the site's core dependency). Use it
// only where a nil writer would produce a silently broken service.
func canonicalMediaWriterRequired(root *ComposeRoot, site string, log *zap.Logger) (persistence.CanonicalAssetWriter, error) {
	w, err := canonicalCommitterForRoot(root, log)
	if err != nil {
		return nil, fmt.Errorf("%s: canonical media writer: %w", site, err)
	}
	if w == nil {
		return nil, fmt.Errorf("%s: canonical media writer unavailable (media PostgreSQL degraded — composition started without a media SSOT handle)", site)
	}
	return w, nil
}
