// Package destination provides a DB-backed resolver that maps topic names
// to Drive folder IDs via the asset-tree service. Extracted from
// voiceover/groups_resolver.go (PR 6, June 2026).
package assets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
)

// Resolver is a thin DB-backed category resolver for mapping topic names
// (e.g. "boxe", "calcio") to Drive folder IDs under a given parent.
type Resolver struct {
	svc *assettree.Service
	log *zap.Logger
}

// Entry mirrors the fields the HTTP layer cares about.
type Entry struct {
	Name      string `json:"name"`
	FolderID  string `json:"folder_id"`
	DriveLink string `json:"drive_link,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
}

// ErrNotFound is returned when a topic doesn't match any row.
var ErrNotFound = errors.New("group not found in asset tree")

// NewResolver builds a resolver backed by the assettree.Service.
func NewResolver(svc *assettree.Service, log *zap.Logger) (*Resolver, error) {
	if svc == nil {
		return nil, fmt.Errorf("assettree.Service is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Resolver{svc: svc, log: log}, nil
}

// ListChildren returns the direct children folders of parentID.
// parentID == "" is rejected.
func (r *Resolver) ListChildren(ctx context.Context, parentID string) ([]Entry, error) {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return nil, fmt.Errorf("parentID is required")
	}

	nodes, err := r.svc.ListChildren(ctx, "drive", parentID)
	if err != nil {
		return nil, fmt.Errorf("list children of %q: %w", parentID, err)
	}
	// Existing Drive folder snapshots may have been imported by the
	// YouTube/catalog synchronizer and therefore carry source="youtube".
	// Keep drive as the canonical source, but reuse that populated SQLite
	// projection when no drive-sourced children exist.
	if len(nodes) == 0 {
		nodes, err = r.svc.ListChildren(ctx, "youtube", parentID)
		if err != nil {
			return nil, fmt.Errorf("list fallback children of %q: %w", parentID, err)
		}
	}

	out := make([]Entry, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || !n.IsFolder {
			continue
		}
		out = append(out, Entry{
			Name:      n.Name,
			FolderID:  n.DriveFileID,
			DriveLink: n.DriveLink,
			ParentID:  n.ParentID,
		})
	}
	return out, nil
}

// ListGroups is a backward-compatible alias for ListChildren.
func (r *Resolver) ListGroups(ctx context.Context, parentID string) ([]Entry, error) {
	return r.ListChildren(ctx, parentID)
}

// ResolveByName looks up a single category folder by its exact name under
// parentID. Case-insensitive comparison, returns ErrNotFound on no match.
func (r *Resolver) ResolveByName(ctx context.Context, parentID, name string) (Entry, error) {
	parentID = strings.TrimSpace(parentID)
	name = strings.TrimSpace(name)
	if parentID == "" || name == "" {
		return Entry{}, fmt.Errorf("parentID and name are required")
	}

	nodes, err := r.ListChildren(ctx, parentID)
	if err != nil {
		return Entry{}, fmt.Errorf("lookup %q under %q: %w", name, parentID, err)
	}

	target := strings.ToLower(name)
	for _, n := range nodes {
		if strings.ToLower(strings.TrimSpace(n.Name)) == target {
			return Entry{
				Name:      n.Name,
				FolderID:  n.FolderID,
				DriveLink: n.DriveLink,
				ParentID:  n.ParentID,
			}, nil
		}
	}
	return Entry{}, fmt.Errorf("%w: %q under %q", ErrNotFound, name, parentID)
}
