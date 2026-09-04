package wiring

import (
	"path/filepath"
	"runtime"
)

// testLexiconRoot resolves the repository's canonical lexicon directory from
// the test source path. It mirrors the helper in the script leaf package so
// wiring-level tests can bootstrap the linguistics registry without depending
// on a leaf-package symbol.
func testLexiconRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../config/lexicons"))
}