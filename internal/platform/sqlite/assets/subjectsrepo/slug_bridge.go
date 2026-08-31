package subjectsrepo

// This file isolates the single line of canonical-slug idiom that
// reaches outside this package. It exists solely to keep
// `internal/platform/` free of `pkg/...` imports in the
// godlike/06 SSOT path.
//
// subjectsrepo lives in internal/infrastructure and may freely
// import `pkg/slug` (the leaf rule is unidirectional — leaf
// packages may NOT import internal/, but internal/ MAY import
// leaf). The bridge is kept as a separate file for narrative
// readability, not for any layering purpose.

import "github.com/Marcuss-ops/PipelineGen/pkg/slug"

// slugifyTitle is the subjectsrepo-local name for the canonical
// pkg/slug.SlugifyTitle. Identical byte-for-byte; the rename is
// a narrative cue ("this is the version the resolver uses") and
// isolates a possible future divergence (godlike/06 SSOT
// exemption text). See pkg/slug/slug.go docstring for the
// canonical 5-step pipeline.
func slugifyTitle(s string) string {
	return slug.SlugifyTitle(s)
}
