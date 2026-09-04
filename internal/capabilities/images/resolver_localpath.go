package images

import imageingest "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/ingest"

func LocalPathFor(imagesDir, slug, source, ext string) (string, string) {
	return imageingest.LocalPathFor(imagesDir, slug, source, ext)
}
