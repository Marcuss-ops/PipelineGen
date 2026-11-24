package contract

import (
	semanticscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
)

// These semantic contracts are owned by internal/kernel/script/semantic; the
// aliases keep the contract facade stable while platform adapters import the
// kernel directly.

type SemanticSearchRetry = semanticscript.SemanticSearchRetry
type SemanticSearchQuery = semanticscript.SemanticSearchQuery
type SemanticSearchQueryComponent = semanticscript.SemanticSearchQueryComponent
type SemanticSearchIntent = semanticscript.SemanticSearchIntent
type SemanticIdentityMention = semanticscript.SemanticIdentityMention
type SemanticLinkEvidenceSpan = semanticscript.SemanticLinkEvidenceSpan
type SemanticLinkFeature = semanticscript.SemanticLinkFeature
type SemanticLinkCandidate = semanticscript.SemanticLinkCandidate
type SemanticIdentity = semanticscript.SemanticIdentity
type SemanticChartItem = semanticscript.SemanticChartItem
type SemanticSearchFeedback = semanticscript.SemanticSearchFeedback
type SemanticSearchFeedbackEntry = semanticscript.SemanticSearchFeedbackEntry
type SemanticVisualConsolidation = semanticscript.SemanticVisualConsolidation
type SemanticConsolidationInventory = semanticscript.SemanticConsolidationInventory
type SemanticProductionCandidate = semanticscript.SemanticProductionCandidate
type SemanticConsolidatedFact = semanticscript.SemanticConsolidatedFact
type SemanticFactRepresentation = semanticscript.SemanticFactRepresentation
type SemanticConsolidatedDate = semanticscript.SemanticConsolidatedDate
type SemanticConsolidatedDateSpan = semanticscript.SemanticConsolidatedDateSpan
type SemanticReferenceNumber = semanticscript.SemanticReferenceNumber
type SemanticEventLink = semanticscript.SemanticEventLink
type SemanticConsolidationSceneStats = semanticscript.SemanticConsolidationSceneStats
type SemanticConsolidationMetrics = semanticscript.SemanticConsolidationMetrics

// VisualEntity is the shared source-grounded entity vocabulary.
type VisualEntity = semanticscript.VisualEntity
