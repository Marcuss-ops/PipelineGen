package voiceover

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/media/assettree"
)

// GroupsResolver is a thin DB-backed voiceover category resolver.
//
// Topic → Drive folder mapping lives in `asset_tree_nodes` (parent_id =
// the voiceover root). This service exposes two read operations used by
// the HTTP handlers so that:
//
//  1. GET /api/media/voiceover/groups returns the canonical list
//     (name, folder_id, drive_link) without any Drive API round-trip.
//  2. voiceover_group="boxe" passed to /generate or /promo is resolved
//     by name → folder_id before falling back to the existing Drive
//     deep-search (which would create a new folder instead of routing).
//
// Owns no state of its own — wraps assettree.Service which is already
// wired via the source_handlers composition chain.
type GroupsResolver struct {
	svc *assettree.Service
	log *zap.Logger
}

// GroupEntry mirrors the fields the HTTP layer cares about.
type GroupEntry struct {
	Name      string `json:"name"`
	FolderID  string `json:"folder_id"`
	DriveLink string `json:"drive_link,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
}

// ErrGroupNotFound is returned when a topic doesn't match any row in
// asset_tree_nodes under the given parent. Callers should fall back to
// the Drive-side resolve path (creating a new folder) on this signal.
var ErrGroupNotFound = errors.New("voiceover group not found in DB")

// NewGroupsResolver builds a resolver backed by the assettree.Service.
// The service is required (returns error if nil) so handlers can rely
// on DB-backed routing always being available when the resolver exists.
func NewGroupsResolver(svc *assettree.Service, log *zap.Logger) (*GroupsResolver, error) {
	if svc == nil {
		return nil, fmt.Errorf("assettree.Service is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &GroupsResolver{svc: svc, log: log}, nil
}

// ListGroups returns the direct children folders of parentID (the
// voiceover root). Source is hardcoded to "drive" because that's what
// the seed migration uses.
//
// parentID == "" is rejected (forces callers to be explicit about which
// Drive root they're listing — voiceover_root vs script_root etc.).
// Returns an empty slice (not an error) when no folders match — the
// caller treats that as "no groups registered" rather than a failure.
func (g *GroupsResolver) ListGroups(ctx context.Context, parentID string) ([]GroupEntry, error) {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return nil, fmt.Errorf("parentID is required")
	}

	nodes, err := g.svc.ListChildren(ctx, "drive", parentID)
	if err != nil {
		return nil, fmt.Errorf("list children of %q: %w", parentID, err)
	}

	out := make([]GroupEntry, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || !n.IsFolder {
			continue
		}
		out = append(out, GroupEntry{
			Name:      n.Name,
			FolderID:  n.DriveFileID,
			DriveLink: n.DriveLink,
			ParentID:  n.ParentID,
		})
	}
	return out, nil
}

// ResolveByName looks up a single category folder by its exact name under
// parentID. Returns ErrGroupNotFound on no match (callers must fall back
// to Drive-side resolution).
//
// Name comparison is case-insensitive but preserves the DB row's actual
// casing in the returned entry — so "Boxe" (canonical) is what callers
// see even when looking up "boxe".
func (g *GroupsResolver) ResolveByName(ctx context.Context, parentID, name string) (GroupEntry, error) {
	parentID = strings.TrimSpace(parentID)
	name = strings.TrimSpace(name)
	if parentID == "" || name == "" {
		return GroupEntry{}, fmt.Errorf("parentID and name are required")
	}

	nodes, err := g.svc.ListChildren(ctx, "drive", parentID)
	if err != nil {
		return GroupEntry{}, fmt.Errorf("lookup %q under %q: %w", name, parentID, err)
	}

	target := strings.ToLower(name)
	for _, n := range nodes {
		if n == nil || !n.IsFolder {
			continue
		}
		if strings.ToLower(strings.TrimSpace(n.Name)) == target {
			return GroupEntry{
				Name:      n.Name,
				FolderID:  n.DriveFileID,
				DriveLink: n.DriveLink,
				ParentID:  n.ParentID,
			}, nil
		}
	}
	return GroupEntry{}, fmt.Errorf("%w: %q under %q", ErrGroupNotFound, name, parentID)
}
