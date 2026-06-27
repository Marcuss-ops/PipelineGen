// Package scripts — pipeline_impl.go was the pre-PR-3 implementation
// of the post-generation pipeline. PR 3 replaced it with the typed
// PostProcessorRegistry.Run + per-processor scene walk. The Pipeline,
// PipelineResult, SceneImage, SceneVoiceover types were consumed only
// by this file's Pipeline.Run stub and have been removed. Deleted by
// `git rm` in the PR 3 commit; this stub is kept here to preserve the
// directory's package declaration during the transition window and
// will be removed in a follow-up clean-up commit.
//
// See `internal/application/scripts/postprocessor_registry.go` (PR 3)
// for the canonical typed registry.
package scripts
