package vidrush

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

// fakeContentStore is a CAS read side keyed by content address. It returns an
// error for addresses it does not hold, exactly like a store miss.
type fakeContentStore map[string]string

func (s fakeContentStore) LocalPath(contentSHA256 string) (string, error) {
	if path, ok := s[contentSHA256]; ok {
		return path, nil
	}
	return "", os.ErrNotExist
}

func writeCASFixture(t *testing.T) (path, contentSHA256 string) {
	t.Helper()
	bytes := []byte("vidrush recycled portrait bytes")
	sum := sha256.Sum256(bytes)
	path = filepath.Join(t.TempDir(), "portrait.jpg")
	require.NoError(t, os.WriteFile(path, bytes, 0o600))
	return path, hex.EncodeToString(sum[:])
}

// TestResolveMaterializedImageLocalPath pins the derivation rule: the path is
// computed from the content address, and it is only returned when the object is
// actually on this host.
func TestResolveMaterializedImageLocalPath(t *testing.T) {
	path, sha := writeCASFixture(t)

	require.Equal(t, path, resolveMaterializedImageLocalPath(fakeContentStore{sha: path}, sha))

	// A path the store names but the disk does not hold is absence, not a hit:
	// injecting it would make a media row claim a local copy that no longer
	// exists.
	require.Empty(t, resolveMaterializedImageLocalPath(fakeContentStore{sha: filepath.Join(t.TempDir(), "gone.jpg")}, sha))

	// A directory is not an image.
	require.Empty(t, resolveMaterializedImageLocalPath(fakeContentStore{sha: t.TempDir()}, sha))

	// Without a store, or without a real 64-hex address, there is nothing to
	// derive.
	require.Empty(t, resolveMaterializedImageLocalPath(nil, sha))
	require.Empty(t, resolveMaterializedImageLocalPath(fakeContentStore{sha: path}, "deadbeef"))
}

// TestEntityImageCatalogCandidatesResolveLocalPathFromCAS is the reuse
// contract: a materialized catalog row whose bytes are already in the CAS comes
// back with the canonical local path, so the scene binds local bytes instead of
// re-downloading the same Drive object.
func TestEntityImageCatalogCandidatesResolveLocalPathFromCAS(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Corrado Augias", "https://images.example/corrado-augias/portrait.jpg")
	identity, err := entitycatalog.CanonicalizePersonName("Corrado Augias")
	require.NoError(t, err)
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	casPath, sha := writeCASFixture(t)
	require.NoError(t, repo.UpsertMaterialization(context.Background(), entitycatalog.Materialization{
		CandidateID:   rows[0].ID,
		AssetID:       "asset-augias",
		ContentSHA256: sha,
		DriveFileID:   "drive-file",
		DriveLink:     "https://drive.google.com/file/d/drive-file/view",
		Status:        entitycatalog.MaterializationStatusMaterialized,
	}))

	pool, err := entityImageCatalogCandidates(context.Background(), repo, identity, 5, fakeContentStore{sha: casPath})
	require.NoError(t, err)
	require.NotEmpty(t, pool.Candidates)
	require.Equal(t, casPath, pool.Candidates[0].LocalPath)
	require.Equal(t, sha, pool.Candidates[0].ContentSHA256)
	require.Equal(t, "https://drive.google.com/file/d/drive-file/view", pool.Candidates[0].DriveLink)

	// Same row, no CAS on this host: the durable Drive artifact is still reused,
	// it simply carries no local path.
	pool, err = entityImageCatalogCandidates(context.Background(), repo, identity, 5, nil)
	require.NoError(t, err)
	require.NotEmpty(t, pool.Candidates)
	require.Empty(t, pool.Candidates[0].LocalPath)
	require.Equal(t, sha, pool.Candidates[0].ContentSHA256)
}
