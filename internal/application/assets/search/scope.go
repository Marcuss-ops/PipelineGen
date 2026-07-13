// Package search — scope.go holds the canonical scope + filter types
// the Qdrant adapters use to construct server-side filters.
//
// PR 5 (June 2026, fix/qdrant-tenant-scope): replaces the previous
// ad-hoc (WorkspaceID string + individual Source/Category/MediaType/
// Language strings) parameter list that each adapter rebuilt by hand
// (search_adapter.go::buildLifecycleAwareFilter, clip_search_adapter.go
// ::buildCurateClipFilter). Cross-tenant isolation was lost in
// clip_search_adapter because the curate filter deliberately omitted
// the workspace clause (see comment "delegate to qdrant.buildLifecycleAwareFilter
// is intentionally avoided here"). PR 5 collapses both code paths
// onto qdrant.CompileQdrantFilter so the canonical lifecycle +
// workspace clause is always on the wire.
//
// Layering note: this file lives in `application/` but is consumed
// by `infra/qdrant/`. The infra layer is allowed to import
// application-layer types (per AGENTS.md layer rules); the reverse
// is not.
package search

// SearchScope is the per-request authorisation envelope that
// CompileQdrantFilter applies as a server-side workspace must-clause.
//
// WorkspaceID is REQUIRED for user-facing traffic. The convention
// matches mediasearch.WorkspaceContext: an empty value is a
// programming error (the API middleware must have rejected it
// already) and a literal "default" value is treated as the
// pre-tenancy sentinel that also fails closed.
//
// IsSystem=true short-circuits the workspace filter so admin +
// background reconciler + DR drills can scan across all
// workspaces without having to enumerate them. The flag must be
// set EXPLICITLY by the calling handler — callers cannot infer it
// from the request body.
type SearchScope struct {
	WorkspaceID string
	PrincipalID string
	IsSystem    bool
}

// AssetFilter is the canonical call-side filter set. Empty fields
// drop out of the resulting Qdrant filter (no zero-value must-clauses
// that would force a full-collection scan).
//
// LifecycleState is an explicit allow-list. Empty means "default
// ACTIVE" — pre-PR5 the canonical ACTIVE-only filter was hardcoded
// in buildLifecycleAwareFilter and the curate path duplicated it;
// PR 5 moves the value-list to AssetFilter so a future admin
// "include-staging" diagnostic opt-in doesn't need to revisit
// qdrant/*.go.
//
// FolderNormalizedGroup (PR-FOLDER-FILTER, July 2026) is the
// canonical lowercase routing key for the `normalized_group`
// Qdrant must-clause. Source of truth = clipfolder.ClipFolderRef;
// producers MUST route via FolderAliasResolver.Resolve() before
// setting this field (godlike/07 NO-FAKE-AVAILABILITY: an
// unmapped input fails upstream at ErrUnknownFolderAlias, never
// silently backfills here).
//
// INVARIANT: the wire key emitted by CompileQdrantFilter is
// `normalized_group` — NEVER `folder`, `macro_topic`, or
// `blueprint`. The latter names are forbidden at the search-port
// surface (pipeline-plan directive, July 2026). Any future
// rename of the payload key requires a coordinated migration
// across the writer (clip_metadata_writer_payload.go) + readers
// (this filter) + the indexer's IndexDocument airlock.
type AssetFilter struct {
	Source                string
	Category              string
	MediaType             string
	Language              string
	LifecycleState        []string
	FolderNormalizedGroup string
}
