// Package ingest owns neutral source-ingestion contracts for stockpipeline.
package ingest

import "context"

type Source struct {
	ID  string
	URL string
}

type PreparedSource struct {
	SourceID  string
	LocalPath string
	Bytes     int64
}

type SourcePreparer interface {
	Prepare(context.Context, Source) (*PreparedSource, error)
}

func UniqueSources(sources []Source) []Source {
	seen := make(map[string]struct{}, len(sources))
	result := make([]Source, 0, len(sources))
	for _, source := range sources {
		if source.ID == "" {
			continue
		}
		if _, ok := seen[source.ID]; ok {
			continue
		}
		seen[source.ID] = struct{}{}
		result = append(result, source)
	}
	return result
}
