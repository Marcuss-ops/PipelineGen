// Package images (api/images) — request_types.go holds all
// request DTOs for the /api/images route group. Per PR-IMG-SPLIT-2
// (July 2026), request types are in their own file so handler files
// carry only transport logic.
//
// godlike/06 SSOT: each DTO is the canonical wire shape for its
// endpoint; field renames or additions must be reflected here first.
package images

// UploadRequest is the JSON body for POST /api/images/upload (legacy
// image-asset URL ingestion). The canonical multipart clip upload
// lives at internal/api/assets/clips/ingest.go — this endpoint is
// JSON-only and distinct from that surface.
type UploadRequest struct {
	Subject string   `json:"subject" binding:"required"`
	Name    string   `json:"name"`
	URL     string   `json:"image_url" binding:"required"`
	Lang    string   `json:"lang"`
	Tags    []string `json:"tags"`
}

// ImageGenerationRequest is the CANONICAL wire-shape for BOTH
// AI image-generation endpoints on the /api/images prefix:
//
//   - POST /api/images/generate          (legacy synchronous subset)
//   - POST /api/images/generated/generate (generated-territory subset)
//
// PR-IMG-LEGACY-5 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, 2026-07-06,
// CUTOVER phase, deadline 2026-08-22): the pre-PR api-layer state
// carried two byte-identical duplicate DTOs with the same field
// shape. The duplicate was a godlike/06 SSOT violation (two
// canonical owners for the same wire-shape intent). CUTOVER unifies
// both endpoints onto a single canonical type so a future field
// rename only touches ONE type definition instead of two.
//
// godlike/06 SSOT: ImageGenerationRequest is the SOLE canonical
// request DTO for AI image generation on the /api/images prefix.
// The DISTINCT service-port type living at
// internal/application/images/ports.go:28 is the service-layer port
// signature, NOT an alias to this wire-shape. The conversion
// (api-layer DTO → application-layer DTO) lives in the generation
// service dispatcher.
//
// PR-IMAGES-SHIM-REMOVAL (2026-07-04): Account/ProjectID fields were
// RETIRED (fake-availability — silently dropped by the legacy shim).
// The unified ImageGenerationRequest does NOT carry those fields;
// forward-pointer for any auth/tenancy migration lives in a separate
// auth/tenancy port (NOT in image-generation request types).
type ImageGenerationRequest struct {
	Prompt string   `json:"prompt" binding:"required"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Style  string   `json:"style" example:"medievale"`
	Tags   []string `json:"tags"`
}

// GenerateBatchRequest is the async batch image generation request
// (FASE 3, June 2026). Each item becomes an independent
// image.generate.google job; concurrency is controlled server-side
// by the worker pool, not by the client.
type GenerateBatchRequest struct {
	// RequestID is an optional caller-supplied identifier for correlation.
	RequestID string `json:"request_id,omitempty"`
	// Items is the list of images to generate (required, min 1).
	Items []GenerateBatchItem `json:"items" binding:"required,min=1"`
}

// GenerateBatchItem describes a single image to generate in a batch.
type GenerateBatchItem struct {
	// Prompt is the natural-language description of the desired image.
	Prompt string `json:"prompt" binding:"required"`
	// Style is the visual style (e.g. "cinematic", "anime").
	Style string `json:"style,omitempty"`
	// Width and Height are the desired output dimensions (default 1920x1080).
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Tags are metadata labels to attach to the generated asset.
	Tags []string `json:"tags,omitempty"`
}
