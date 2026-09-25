// Package ingest owns neutral source-ingestion contracts for stockpipeline.
package ingest

import "context"

type Source struct {
	ID  string
	URL string
	// DownloadSection is the absolute yt-dlp --download-sections range
	// ("*HH:MM:SS.mmm-HH:MM:SS.mmm") that contains every planned clip of this
	// source. Empty means "stage the whole source", which is the behaviour for
	// every run that is not sections_only and for a plan whose clips are not
	// contiguous. It travels the boundary so the stager downloads only the
	// published seconds instead of a whole interview.
	DownloadSection string
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
