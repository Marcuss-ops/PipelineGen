package wiring

import (
	processwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/process"
	searchwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/search"
)

type SearchBundle = searchwiring.SearchBundle
type ProcessQdrantBundle = processwiring.ProcessQdrantBundle
type ProcessBundle = processwiring.ProcessBundle
type QdrantDeps = processwiring.QdrantDeps
