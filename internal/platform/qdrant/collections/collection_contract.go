package collections

import (
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

// CollectionContract derives the Qdrant-observable portion of the embedding
// contract from a collection's vector config. Qdrant exposes dimension and
// distance on the wire; model identity and preprocessing are checked by the
// separate boot contract handshake.
func CollectionContract(info *qdrantschema.CollectionInfo, channel string) (coreembedding.Contract, bool) {
	if info == nil || info.VectorConfigs == nil {
		return coreembedding.Contract{}, false
	}
	cfg, ok := info.VectorConfigs[channel]
	if !ok {
		return coreembedding.Contract{}, false
	}
	return coreembedding.Contract{Dimension: cfg.Size, Distance: cfg.Distance}, true
}
