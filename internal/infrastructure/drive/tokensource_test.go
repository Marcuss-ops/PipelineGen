package drive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseTokenFile(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name       string
		payload    string
		write      bool
		wantToken  string
		wantErrSub string
	}{
		{name: "happy_path_full_token", payload: `{"access_token":"ya29.fake","refresh_token":"1//","token_type":"Bearer","expiry":"2026-12-31T00:00:00Z"}`, write: true, wantToken: "ya29.fake"},
		{name: "empty_path_is_error", payload: "", write: false, wantErrSub: "path is empty"},
		{name: "missing_access_token_field", payload: `{"refresh_token":"x"}`, write: true, wantErrSub: "missing access_token"},
		{name: "malformed_json", payload: `{"access_token": "missing-close-brace`, write: true, wantErrSub: "decode token JSON"},
		{name: "empty_file", payload: "", write: true, wantErrSub: "decode token JSON"},
		{name: "whitespace_only", payload: "   \n", write: true, wantErrSub: "decode token JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			if tc.write {
				f := filepath.Join(tmp, strings.ReplaceAll(tc.name, " ", "_")+".json")
				require.NoError(t, os.WriteFile(f, []byte(tc.payload), 0o600))
				path = f
			}
			tok, err := ParseTokenFile(path)
			if tc.wantErrSub != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErrSub)
				require.Empty(t, tok)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantToken, tok)
		})
	}
}

func TestParseTokenFile_PathNotFound(t *testing.T) {
	tok, err := ParseTokenFile("/nonexistent/should/not/exist.json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read")
	require.Empty(t, tok)
}
