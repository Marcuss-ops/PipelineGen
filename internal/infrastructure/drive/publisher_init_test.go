package drive

// init surface tests for the drive publisher.

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewPublisher_NilRegistry(t *testing.T) {
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(nil, folders, files, zap.NewNop())
	require.Error(t, err, "NewPublisher must fail-fast on nil registry")
	require.ErrorIs(t, err, ErrMissingDestinationRegistry,
		"nil-registry error must wrap ErrMissingDestinationRegistry verbatim (audit-trail grep)")
	require.Nil(t, pub, "publisher pointer must be nil on error return (composition-time safety)")
}

func TestNewPublisher_NilFolders(t *testing.T) {
	reg := testRegistry()
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, nil, files, zap.NewNop())
	require.Error(t, err, "NewPublisher must fail-fast on nil FolderManagerPort")
	require.ErrorIs(t, err, ErrMissingFolderManager,
		"nil-folders error must wrap ErrMissingFolderManager verbatim")
	require.Nil(t, pub)
}

func TestNewPublisher_NilFiles(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	pub, err := NewPublisher(reg, folders, nil, zap.NewNop())
	require.Error(t, err, "NewPublisher must fail-fast on nil FileUploaderPort")
	require.ErrorIs(t, err, ErrMissingFileUploader,
		"nil-files error must wrap ErrMissingFileUploader verbatim")
	require.Nil(t, pub)
}
