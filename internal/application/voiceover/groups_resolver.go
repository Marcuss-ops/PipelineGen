// Package voiceover — GroupsResolver moved to destination package (PR 6, June 2026).
// Type aliases preserve backward compatibility for consumers that haven't migrated yet.
package voiceover

import "github.com/Marcuss-ops/PipelineGen/internal/application/assets/destination"

// GroupsResolver is a thin DB-backed voiceover category resolver.
// Deprecated: use destination.Resolver directly.
type GroupsResolver = destination.Resolver

// GroupEntry mirrors the fields the HTTP layer cares about.
// Deprecated: use destination.Entry directly.
type GroupEntry = destination.Entry

// ErrGroupNotFound is returned when a topic doesn't match any row in asset_tree_nodes.
// Deprecated: use destination.ErrNotFound directly.
var ErrGroupNotFound = destination.ErrNotFound

// NewGroupsResolver builds a resolver backed by the assettree.Service.
// Deprecated: use destination.NewResolver directly.
var NewGroupsResolver = destination.NewResolver
