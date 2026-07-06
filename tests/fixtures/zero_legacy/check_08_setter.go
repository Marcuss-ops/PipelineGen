//go:build ignore

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

// SetOutboxHandler / SetMediasearchHandler are defined on serverShim
// so the init() block below can call them — without these methods the
// fixture has no surface for the lint to match against, breaking the
// compile.
//
// Return type is `error` (idiomatic for setters that may signal
// rejection in production) because the original fixture assigns the
// result to `_ =` — that assignment only compiles when the method
// returns at least one value. The blank-discard pattern keeps the
// demo call readable while the methods themselves stay inert.
func (s *serverShim) SetOutboxHandler(_ any) error      { return nil }
func (s *serverShim) SetMediasearchHandler(_ any) error { return nil }

// Forbidden: production code calling .SetOutboxHandler or
// .SetMediasearchHandler after server construction. The canonical
// constructor NewServerWithHealth accepts these handlers as params.
func init() {
	var s serverShim
	_ = s.SetOutboxHandler(nil)
	_ = s.SetMediasearchHandler(nil)
}
