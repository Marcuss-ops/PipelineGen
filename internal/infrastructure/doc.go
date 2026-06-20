// Package infrastructure is the umbrella package that hosts the concrete
// infrastructure subpackages used by PipelineGen (logging, config, database,
// downloader, ai/ollama, ai/reranker, audio, embeddings, security, ...).
//
// STATUS: SENTINEL (Onda 5 mid-state recovery, Stage 3').
// The actual concrete subpackages under internal/infrastructure/<sub>/ still
// exist and are unchanged. This top-level file exists ONLY so that the six
// legacy import sites
//
//	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
//
// continue to resolve at the module level. Consumers that previously used
// this parent path are being migrated as part of feature/onda-5-completion.
//
// After the migration lands, this file should be deleted together with the
// last import that references the parent path; if no consumer uses symbols
// from package infrastructure itself, removal is unconditional.
package infrastructure
