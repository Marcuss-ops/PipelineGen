// Package fixture contains the forbidden patterns that each CI check
// (scripts/ci-architectural-checks.sh) is designed to catch. The file
// lives in tests/fixtures/zero_legacy/ so the standard CI mode
// (--glob '!tests/fixtures/zero_legacy/**') excludes it; the --self-check
// mode scans ONLY this directory and verifies the regex catches the
// pattern here.
//
// This file (check_08_setter.go) demonstrates the post-Setup
// SetOutboxHandler / SetMediasearchHandler setter call pattern.
package fixture

type serverShim struct{}

// Forbidden: production code calling .SetOutboxHandler or
// .SetMediasearchHandler after server construction. The canonical
// constructor NewServerWithHealth accepts these handlers as params.
func init() {
	var s serverShim
	_ = s.SetOutboxHandler(nil)
	_ = s.SetMediasearchHandler(nil)
}
