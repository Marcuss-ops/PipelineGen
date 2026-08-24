package reconciliation

// Small pure helpers shared by the report and assertion tests.

func contains(haystack []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ── Defensive: NewServiceFromDeps panic guards ──────────────────────
//
// NewServiceFromDeps panics on the three fail-loud paths (Schema.Version
// empty, Qdrant nil, SQLite nil). Production wire-up MUST NEVER trip
// these guards; they exist to surface misconfiguration at startup, not
// runtime. The test below locks the contract so a future refactor that
// accidentally makes one of these a silent fall-back (e.g. defaulting
// to an identity/sqlite stub) is caught by CI.
// ── LocatorKeys asymmetric-path coverage ─────────────────────────────
//
// Reviewer's only actionable PR2 follow-up: TestReconcile_ApplyDispatchesPerKind
// exercises only the both-keys case. A regression reverting applyRepair
// to blanket-bump per locator point would slip past CI silently. The
// 3-row table below pins drive_link-only / local_path-only / both so
// each causal path is independently verified.
