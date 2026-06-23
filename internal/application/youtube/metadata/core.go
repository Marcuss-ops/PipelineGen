// Package metadata provides YouTube clip metadata type aliases.
// metadata pipeline. The metadata subsystem is split across three files for
// clarity and bounded diff surface:
//
//   - metadata_core.go    : ClipMetadataFile alias shim (canonical definition
//     extracted to youtube/types/ per PR3 Phase 2)
//   - metadata_enrich.go  : orchestration (writeClipMetadataFile)
//   - metadata_persist.go : field accessors and content helpers
package metadata

import "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"

// ClipMetadataFile is a zero-copy alias to the canonical type in youtube/types/.
// Extracted during PR3 Phase 2 (June 2026) per AGENTS.md Pattern 5.
type ClipMetadataFile = types.ClipMetadataFile
