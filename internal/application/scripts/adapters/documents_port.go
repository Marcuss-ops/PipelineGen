// Package adapters — compatibility aliases for document postprocessing ports.
package adapters

import scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"

// FolderResolver is the canonical application-owned folder resolver port.
type FolderResolver = scriptports.FolderResolver

// DocumentsService is retained as a compatibility alias. Ownership remains
// in scripts/ports.DocumentsService.
type DocumentsService = scriptports.DocumentsService

// DocumentPublisherPort is retained as a compatibility alias.
type DocumentPublisherPort = scriptports.DocumentPublisher
