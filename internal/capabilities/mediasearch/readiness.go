package mediasearch

import (
	"context"
	"strings"
	"time"
)

// SemanticReadyChecker is the narrow port for semantic media-search readiness.
// Production wiring checks query embedding, the canonical search backend, and
// the PostgreSQL media SSOT. Qdrant and SQLite are not media dependencies.
type SemanticReadyChecker interface {
	Ready(ctx context.Context) error
}

// IndexVersionSource is the narrow port for the live search-side index version.
type IndexVersionSource interface {
	IndexVersion(ctx context.Context) string
}

type staticIndexVersion struct{ v string }

func (s staticIndexVersion) IndexVersion(_ context.Context) string { return s.v }
func StaticIndexVersion(v string) IndexVersionSource              { return staticIndexVersion{v: v} }

// buildReadinessReport maps the typed subsystem failures onto the public DTO.
// A non-typed error fails every sub-check closed rather than reporting a fake
// green state.
func buildReadinessReport(err error, indexVer string) ReadinessReport {
	if err == nil {
		return ReadinessReport{
			Ready:              true,
			Embedder:           true,
			SemanticBackend:    true,
			MediaPostgresReady: true,
			WorkspaceEnforced:  true,
			Timestamp:          nowRFC3339(),
			IndexVersion:       indexVer,
		}
	}

	subErrs := decomposeReadinessFailures(err)
	typedProbeAbsent := len(subErrs) == 0
	failuresField := joinFailures(subErrs)
	if typedProbeAbsent {
		failuresField = "typed readiness probe not wired (Subsystems() contract missing)"
	}
	return ReadinessReport{
		Ready:              false,
		Embedder:           !typedProbeAbsent && subErrs["embedder"] == "",
		SemanticBackend:    !typedProbeAbsent && subErrs["semantic_backend"] == "",
		MediaPostgresReady: !typedProbeAbsent && subErrs["media_postgres"] == "",
		WorkspaceEnforced:  !typedProbeAbsent && subErrs["workspace"] == "",
		Timestamp:          nowRFC3339(),
		IndexVersion:       indexVer,
		Failures:           failuresField,
	}
}

func decomposeReadinessFailures(err error) map[string]string {
	out := make(map[string]string, 4)
	if err == nil {
		return out
	}
	if rErr, ok := err.(interface{ Subsystems() map[string]string }); ok {
		return rErr.Subsystems()
	}
	return out
}

func joinFailures(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		if v != "" {
			parts = append(parts, k+"="+sanitizeMessage(v))
		}
	}
	return strings.Join(parts, " ")
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
