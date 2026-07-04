// payload.go — typed request/response DTOs for the seed_test_asset CLI.
//
// godlike/06 SSOT: the request shape mirrors the canonical script
// generate-from-clips endpoint (internal/api/script/) but is OWNED by
// this CLI to avoid coupling the seed logic to the server's internal
// type evolution. The canonical JSON contract is the wire surface
// between the CLI and the server; the server's internal struct is
// implementation detail.
//
// godlike/07 typed-error contract: status fields are typed strings, not
// raw enums, so the CLI can validate against canonical values without
// importing the server's internal type hierarchy.

package main

// SeedRequest is the JSON payload POSTed to /api/script/generate-from-clips.
// The shape is intentionally minimal: a single clip with known text content
// (for Test 7 search) + sandbox flag (for Test 10 delete) + deterministic
// aggregate_id (for Test 8 supersede verification).
type SeedRequest struct {
	ProjectName   string     `json:"project_name"`
	AggregateID   string     `json:"aggregate_id"`
	IsSandbox     bool       `json:"is_sandbox"`
	SourceVersion int        `json:"source_version"`
	VOAssetID     string     `json:"vo_asset_id,omitempty"`
	Clips         []SeedClip `json:"clips"`
}

// SeedClip is the per-clip entry in SeedRequest.
type SeedClip struct {
	SourceURL     string `json:"source_url"`
	Transcription string `json:"transcription"`
	VOAssetID     string `json:"vo_asset_id,omitempty"`
}

// SeedResponse is the JSON shape returned by /api/script/generate-from-clips.
// Only the fields needed by Tests 3-8 + 10 are decoded; unknown fields are
// ignored so the CLI is forward-compatible with future server response
// additions.
type SeedResponse struct {
	AssetID   string `json:"asset_id"`
	JobID     string `json:"job_id"`
	VOAssetID string `json:"vo_asset_id,omitempty"`
}

// SeedResult is the JSON shape printed to stdout by the CLI on success.
// The preflight binary (cmd/admin/qdrant_preflight.go) consumes this
// output to populate preflightDeps.SeedAssetID / SeedJobID / SeedVOAssetID.
type SeedResult struct {
	AssetID     string `json:"asset_id"`
	JobID       string `json:"job_id"`
	VOAssetID   string `json:"vo_asset_id,omitempty"`
	Status      string `json:"status"`       // canonical index_state value at exit
	AggregateID string `json:"aggregate_id"` // for Test 8 supersede verification
}

// AssetStatus is the JSON shape returned by GET /api/assets/clips/{id}.
type AssetStatus struct {
	ID             string `json:"id"`
	IndexState     string `json:"index_state"`
	LifecycleState string `json:"lifecycle_state"`
}
