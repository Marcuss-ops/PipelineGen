// Package media — vector_surfaces_testhelpers_test.go: typed test
// request builders shared by the pgvector cutover tests.
package media_test

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// txCommitRequestFor builds a canonical CommitRequest (the
// persistence.AssetCommitter surface used inside an explicit tx) for the
// given asset ID — mirroring fullCommitRequest()'s shape.
func txCommitRequestFor(assetID string) persistence.CommitRequest {
	return persistence.CommitRequest{
		AssetID:        assetID,
		Source:         "youtube",
		Name:           "Funny Moment",
		Filename:       "clip.mp4",
		MediaType:      "video",
		Category:       "celebrity",
		DurationMs:     50000,
		ContentHash:    "sha256:content",
		Description:    "A funny moment",
		SearchText:     "funny moment",
		LifecycleState: "ACTIVE",
		EmitIndexEvent: true,
		Locations: []persistence.LocationCommit{
			{Kind: "drive", ExternalID: "drive-tx-1", WebViewLink: "https://drive.google.com/file/d/drive-tx-1/view", IsPrimary: true},
		},
	}
}

// withTaxonomy overrides the category of a mediacommit request fixture.
func withTaxonomy(req mediacommit.CommitMediaAssetRequest, category string) mediacommit.CommitMediaAssetRequest {
	req.Asset.Category = category
	req.Taxonomy = capregistry.AssetTaxonomy{
		Namespace:  "stock",
		MediaType:  capregistry.MediaVideo,
		AssetKind:  capregistry.AssetClip,
		SourceType: "youtube",
	}
	return req
}

// searchRequestForWorkspace builds a canonical VectorSearchRequest with
// an explicit workspace scope and category filter.
func searchRequestForWorkspace(workspaceID, category string, vec []float32) (req search.VectorSearchRequest) {
	req = search.VectorSearchRequest{
		QueryVector: vec,
		VectorName:  "text",
		Limit:       10,
		WorkspaceID: workspaceID,
		Category:    category,
		MediaType:   "video",
		Source:      "youtube",
		MinScore:    0.0,
	}
	return req
}
