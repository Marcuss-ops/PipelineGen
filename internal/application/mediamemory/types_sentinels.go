// Package mediamemory — sentinel errors re-exported from capabilities/mediamemory/.
package mediamemory

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"

var (
	ErrInvalidPhrase                       = mediamemory.ErrInvalidPhrase
	ErrConceptNotFound                     = mediamemory.ErrConceptNotFound
	ErrBindingNotFound                     = mediamemory.ErrBindingNotFound
	ErrDuplicateBinding                    = mediamemory.ErrDuplicateBinding
	ErrInvalidSlotKind                     = mediamemory.ErrInvalidSlotKind
	ErrApprovalRequired                    = mediamemory.ErrApprovalRequired
	ErrCandidateMaterializationFailed      = mediamemory.ErrCandidateMaterializationFailed
	ErrBatchNotFound                       = mediamemory.ErrBatchNotFound
	ErrBatchNotReconcilable                = mediamemory.ErrBatchNotReconcilable
	ErrInvalidFeedbackAction               = mediamemory.ErrInvalidFeedbackAction
	ErrInvalidAggregateSince               = mediamemory.ErrInvalidAggregateSince
	ErrCandidateNotFound                   = mediamemory.ErrCandidateNotFound
	ErrInvalidBindingInput                 = mediamemory.ErrInvalidBindingInput
	ErrBindingMutationDispatcherUnavailable = mediamemory.ErrBindingMutationDispatcherUnavailable
	ErrSemanticNotConfigured               = mediamemory.ErrSemanticNotConfigured
	ErrSemanticBackendFailed               = mediamemory.ErrSemanticBackendFailed
	ErrInvalidBatchMode                    = mediamemory.ErrInvalidBatchMode
	ErrBatchSpecDrift                      = mediamemory.ErrBatchSpecDrift

	ErrLinkerUnmappableConcept         = mediamemory.ErrLinkerUnmappableConcept
	ErrLinkerExtractFailed             = mediamemory.ErrLinkerExtractFailed
	ErrLinkerEmbeddingFailed           = mediamemory.ErrLinkerEmbeddingFailed
	ErrLinkerConceptAssignmentFailed   = mediamemory.ErrLinkerConceptAssignmentFailed
	ErrLinkerInvariantBroken           = mediamemory.ErrLinkerInvariantBroken
)