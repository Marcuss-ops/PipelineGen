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

// GenerateImageRequest is the request type for POST /api/images/generate
// (legacy synchronous AI image generation). PR-IMAGES-SHIM-REMOVAL
// (2026-07-04) retired Account/ProjectID fields (fake-availability).
type GenerateImageRequest struct {
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

// AnimateRequest is the JSON body for POST /api/images/animate
// (image animation — not implemented, NVIDIA capability removed).
type AnimateRequest struct {
	ImageHash string `json:"image_hash" binding:"required"`
	Duration  int    `json:"duration"`
}
