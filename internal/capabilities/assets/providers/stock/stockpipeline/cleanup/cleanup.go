// Package cleanup owns release of temporary stock-pipeline resources.
//
// It is deliberately independent of stockpipeline. The parent package adapts
// its existing acquisition.SourceStager to this neutral boundary.
package cleanup

import "context"

// Resource identifies a temporary resource created during ingestion.
type Resource struct {
	SourceID  string
	LocalPath string
}

// Releaser owns cleanup of temporary resources.
type Releaser interface {
	Release(context.Context, Resource) error
}

// Failure records one release failure without stopping cleanup of later
// resources. The resource is retained so callers can produce actionable logs.
type Failure struct {
	Resource Resource
	Err      error
}

// ReleaseAll performs best-effort cleanup in input order. A nil releaser or an
// empty resource list is a no-op. Every resource is attempted even if an
// earlier release fails.
func ReleaseAll(ctx context.Context, releaser Releaser, resources []Resource) []Failure {
	if releaser == nil {
		return nil
	}
	failures := make([]Failure, 0)
	for _, resource := range resources {
		if err := releaser.Release(ctx, resource); err != nil {
			failures = append(failures, Failure{Resource: resource, Err: err})
		}
	}
	return failures
}
