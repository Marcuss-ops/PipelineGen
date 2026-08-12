package mediaregistry

import "fmt"

type Counts struct {
	Assets       int
	Transcripts  int
	Descriptions int
	QdrantPoints int
}

// ValidateCounts rejects silent loss of canonical enrichment. Counts may
// grow during normal ingest, but transcripts/descriptions and active assets
// may not decrease unless the caller explicitly authorizes a destructive run.
func ValidateCounts(before, after Counts, destructive bool) error {
	if destructive {
		return nil
	}
	if after.Assets < before.Assets {
		return fmt.Errorf("media registry invariant: assets decreased %d -> %d", before.Assets, after.Assets)
	}
	if after.Transcripts < before.Transcripts {
		return fmt.Errorf("media registry invariant: transcripts decreased %d -> %d", before.Transcripts, after.Transcripts)
	}
	if after.Descriptions < before.Descriptions {
		return fmt.Errorf("media registry invariant: descriptions decreased %d -> %d", before.Descriptions, after.Descriptions)
	}
	return nil
}
