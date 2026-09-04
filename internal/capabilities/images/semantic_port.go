package images

import imageingest "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/ingest"

type SemanticPort = imageingest.SemanticPort
type SemanticWriteRequest = imageingest.SemanticWriteRequest
type SemanticWriteResult = imageingest.SemanticWriteResult
type SemanticPayload = imageingest.SemanticPayload

func buildImageSemanticExtension(width, height int) []map[string]any {
	return imageingest.ImageSemanticExtension(width, height)
}
