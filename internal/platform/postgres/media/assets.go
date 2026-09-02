// Package media — assets.go: compile-time interface completeness checks for
// the canonical PostgreSQL writer family. Every port assertion lives here so
// a drifted method signature fails the build in exactly one place.
package media

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

// Canonical writer family assertions — PostgresMediaCommitter is the single
// production writer (CanonicalAssetWriter = AssetCommitter + AssetMutator +
// dispatcher-facing tx-bound mutations); PostgresAssetCommitter is the
// raw-path sibling owning the rendition and index-event seams.
var (
	_ persistence.CanonicalAssetWriter     = (*PostgresMediaCommitter)(nil)
	_ persistence.AssetMutator             = (*PostgresMediaCommitter)(nil)
	_ persistence.AssetRenditionCommitter  = (*PostgresAssetCommitter)(nil)
	_ persistence.AssetIndexEventCommitter = (*PostgresAssetCommitter)(nil)
)
