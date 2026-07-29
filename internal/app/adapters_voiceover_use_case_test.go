// Package app — adapters_voiceover_use_case_test.go (P0.2 destination-adapter fix, July 2026).
//
// Tests for useCaseDestResolverAdapter field-forwarding: the adapter
// must forward ALL DestinationRequest fields (Group, FolderID, FolderPath,
// SubfolderName, CreateSubfolder, StyleGroup) to asset.ResolveRequest,
// and mirror SubfolderName + StyleGroup on the returned ResolvedDestination.
// Pre-fix only Group + StyleGroup were forwarded; FolderID / FolderPath /
// SubfolderName / CreateSubfolder were silently dropped.
package app

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingResolver implements asset.Resolver by capturing the
// ResolveRequest it receives and returning a pre-configured result.
type recordingResolver struct {
	lastReq   *asset.ResolveRequest
	allReqs   []*asset.ResolveRequest
	cannedRes *asset.ResolveResult
	cannedErr error
}

func (r *recordingResolver) Resolve(ctx context.Context, req *asset.ResolveRequest) (*asset.ResolveResult, error) {
	r.lastReq = req
	r.allReqs = append(r.allReqs, req)
	if r.cannedErr != nil {
		return nil, r.cannedErr
	}
	if r.cannedRes != nil {
		return r.cannedRes, nil
	}
	return &asset.ResolveResult{
		FolderID:   "resolver-returned-folder-id",
		FolderPath: "/resolver/returned/path",
		DriveLink:  "https://drive.google.com/drive/folders/resolver-folder",
	}, nil
}

var _ asset.Resolver = (*recordingResolver)(nil)

// ─────────────────────────────────────────────────────────────────────
// TestExplicitFolderIDReachesPublisher
// ─────────────────────────────────────────────────────────────────────

// Pins the P0.2 destination-adapter fix: when a DestinationRequest
// carries an explicit FolderID, the adapter forwards it to the
// asset.ResolveRequest AND uses it as the canonical FolderID on the
// returned ResolvedDestination (explicit override over the resolver's
// result). Also verifies SubfolderName, FolderPath, CreateSubfolder,
// Group, and StyleGroup all reach the resolver.
func TestExplicitFolderIDReachesPublisher(t *testing.T) {
	rec := &recordingResolver{}
	adapter := newUseCaseDestResolverAdapter(rec)

	dest := &voiceover.DestinationRequest{
		Kind:            "explicit",
		Group:           "test-group",
		FolderID:        "explicit-folder-abc123",
		FolderPath:      "/explicit/path",
		SubfolderName:   "my-subfolder",
		CreateSubfolder: true,
		StyleGroup:      "style-cohort-1",
	}

	result, err := adapter.Resolve(context.Background(), dest)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, rec.lastReq, "resolver must have been called")

	// ── Forwarding assertions: every field must reach asset.ResolveRequest ──
	assert.Equal(t, "voiceover", rec.lastReq.Source, "Source must be 'voiceover'")
	assert.Equal(t, "test-group", rec.lastReq.Group, "Group must be forwarded")
	assert.Equal(t, "explicit-folder-abc123", rec.lastReq.FolderID,
		"P0.2 fix: FolderID must be forwarded to ResolveRequest (was silently dropped pre-fix)")
	assert.Equal(t, "/explicit/path", rec.lastReq.FolderPath,
		"P0.2 fix: FolderPath must be forwarded to ResolveRequest")
	assert.Equal(t, "my-subfolder", rec.lastReq.SubfolderName,
		"P0.2 fix: SubfolderName must be forwarded to ResolveRequest")
	assert.True(t, rec.lastReq.CreateSubfolder,
		"P0.2 fix: CreateSubfolder must be forwarded to ResolveRequest")
	assert.Equal(t, "style-cohort-1", rec.lastReq.StyleGroup,
		"StyleGroup must be forwarded to ResolveRequest")

	// ── Mirror assertions: explicit fields take precedence on ResolvedDestination ──
	// P0.2 fix: explicit FolderID overrides the resolver's returned FolderID.
	assert.Equal(t, "explicit-folder-abc123", result.FolderID,
		"P0.2 fix: explicit FolderID must take precedence over resolver's FolderID")
	assert.Equal(t, "/explicit/path", result.FolderPath,
		"P0.2 fix: explicit FolderPath must take precedence over resolver's FolderPath")
	assert.Equal(t, "my-subfolder", result.SubfolderName,
		"P0.2 fix: SubfolderName must be mirrored on ResolvedDestination")
	assert.Equal(t, voiceover.StyleGroup("style-cohort-1"), result.StyleGroup,
		"StyleGroup must be mirrored verbatim on ResolvedDestination")
	assert.Equal(t, "test-group", result.Group,
		"Group must be mirrored on ResolvedDestination")
}

// ─────────────────────────────────────────────────────────────────────
// TestDestinationResolverExplicitFolderIDOverridesResolver
// ─────────────────────────────────────────────────────────────────────

// Pins the explicit-override contract: when FolderID is set on the
// DestinationRequest AND the resolver returns a different FolderID,
// the explicit value wins. This is the \"I know exactly which folder
// I want — don't resolve through Group\" semantic.
func TestDestinationResolverExplicitFolderIDOverridesResolver(t *testing.T) {
	rec := &recordingResolver{
		cannedRes: &asset.ResolveResult{
			FolderID:   "resolver-returned-different-folder",
			FolderPath: "/resolver/path",
			DriveLink:  "https://drive.google.com/drive/folders/resolver-link",
		},
	}
	adapter := newUseCaseDestResolverAdapter(rec)

	dest := &voiceover.DestinationRequest{
		FolderID:   "my-explicit-xyz",
		FolderPath: "/my/explicit/path",
	}

	result, err := adapter.Resolve(context.Background(), dest)
	require.NoError(t, err)

	// FolderID: explicit wins over resolver.
	assert.Equal(t, "my-explicit-xyz", result.FolderID,
		"P0.2: explicit FolderID must override resolver's FolderID")
	assert.Equal(t, "/my/explicit/path", result.FolderPath,
		"P0.2: explicit FolderPath must override resolver's FolderPath")
	// DriveLink still comes from the resolver (no explicit override).
	assert.Equal(t, "https://drive.google.com/drive/folders/resolver-link", result.DriveLink,
		"DriveLink must come from resolver (no explicit override)")
}

// ─────────────────────────────────────────────────────────────────────
// TestDestinationResolverEmptyFolderIDFallsBackToResolver
// ─────────────────────────────────────────────────────────────────────

// Pins the fallback contract: when FolderID is empty, the resolver's
// returned FolderID is used. The explicit-override gate must NOT
// zero-out a valid resolver FolderID.
func TestDestinationResolverEmptyFolderIDFallsBackToResolver(t *testing.T) {
	rec := &recordingResolver{
		cannedRes: &asset.ResolveResult{
			FolderID:   "resolver-folder-456",
			FolderPath: "/resolver/group/path",
			DriveLink:  "https://drive.google.com/drive/folders/group-resolved",
		},
	}
	adapter := newUseCaseDestResolverAdapter(rec)

	dest := &voiceover.DestinationRequest{
		Group:         "boxe",
		SubfolderName: "per-script-sub",
		StyleGroup:    "promo",
	}

	result, err := adapter.Resolve(context.Background(), dest)
	require.NoError(t, err)

	// No explicit FolderID → resolver's value wins.
	assert.Equal(t, "resolver-folder-456", result.FolderID,
		"No explicit FolderID → resolver's FolderID must be used")
	assert.Equal(t, "/resolver/group/path", result.FolderPath,
		"No explicit FolderPath → resolver's FolderPath must be used")
	assert.Equal(t, "per-script-sub", result.SubfolderName,
		"SubfolderName must be mirrored verbatim")
	assert.Equal(t, voiceover.StyleGroup("promo"), result.StyleGroup,
		"StyleGroup must be mirrored verbatim")
}
