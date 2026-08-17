package script

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// The three contract cases that pin the script docs folder precedence:
//
//	ENV default = A, payload folder = B    → RESULT = B
//	ENV default = A, payload folder = ""   → RESULT = A
//	ENV default = "", payload folder = ""  → RESULT = ERROR (when enabled)
func TestResolveScriptDocsFolderID_Contract(t *testing.T) {
	t.Run("caller override wins over configured default", func(t *testing.T) {
		folderID, err := ResolveScriptDocsFolderID(true, "B", "A")
		require.NoError(t, err)
		require.Equal(t, "B", folderID)
	})

	t.Run("configured default used when payload folder empty", func(t *testing.T) {
		folderID, err := ResolveScriptDocsFolderID(true, "", "A")
		require.NoError(t, err)
		require.Equal(t, "A", folderID)
	})

	t.Run("fail closed when enabled and neither configured", func(t *testing.T) {
		folderID, err := ResolveScriptDocsFolderID(true, "", "")
		require.ErrorIs(t, err, ErrScriptDocsFolderRequired)
		require.Empty(t, folderID)
	})
}

func TestResolveScriptDocsFolderID_WhitespaceIsEmpty(t *testing.T) {
	t.Run("caller whitespace falls back to configured default", func(t *testing.T) {
		folderID, err := ResolveScriptDocsFolderID(true, "   ", "A")
		require.NoError(t, err)
		require.Equal(t, "A", folderID)
	})

	t.Run("configured default whitespace counts as unset", func(t *testing.T) {
		folderID, err := ResolveScriptDocsFolderID(true, "B", "   ")
		require.NoError(t, err)
		require.Equal(t, "B", folderID)
	})

	t.Run("both whitespace and enabled fails closed", func(t *testing.T) {
		_, err := ResolveScriptDocsFolderID(true, "  ", " \t ")
		require.ErrorIs(t, err, ErrScriptDocsFolderRequired)
	})
}

func TestResolveScriptDocsFolderID_DisabledNeverFails(t *testing.T) {
	folderID, err := ResolveScriptDocsFolderID(false, "", "")
	require.NoError(t, err)
	require.Empty(t, folderID)
	require.False(t, errors.Is(err, ErrScriptDocsFolderRequired))
}
