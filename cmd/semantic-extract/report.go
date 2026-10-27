package main

import (
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func buildReport(
	inputPath, language, text string,
	sceneWords int,
	scenes []scriptgen.SemanticSceneInput,
	bundles map[string]*scriptgen.SemanticCandidateBundle,
	consolidation *scriptgen.SemanticVisualConsolidation,
	plan *scriptgen.SemanticProductionPlan,
) extractionReport {
	report := extractionReport{
		Schema: "semantic_cold_extraction.v1",
		Input:  inputReport{Path: inputPath, Language: language, WordCount: len(strings.Fields(text)), SceneCount: len(scenes), SceneWords: sceneWords},
		Scenes: make([]sceneReport, 0, len(scenes)),
	}
	if consolidation != nil {
		report.Inventory = inventoryReport{
			EntityMentions:       consolidation.Inventory.EntityMentions,
			UniqueIdentities:     consolidation.Inventory.UniqueIdentities,
			NamedIdentities:      consolidation.Inventory.NamedIdentities,
			Proposals:            consolidation.Inventory.Proposals,
			SearchEntries:        consolidation.Inventory.SearchEntries,
			QueryCandidates:      consolidation.Inventory.Queries,
			ProductionCandidates: len(consolidation.ProductionCandidates),
		}
	}
	planByScene := make(map[string][]scriptgen.SemanticProductionUnit)
	candidatePriority := make(map[string]float64)
	if plan != nil {
		report.Plan = planReport{Schema: plan.Schema, Metrics: planMetricsReport{
			RawCandidates:           plan.Metrics.RawCandidates,
			AdmittedUnits:           plan.Metrics.AdmittedUnits,
			MediaUnits:              plan.Metrics.MediaUnits,
			ReusedAssets:            plan.Metrics.ReusedAssets,
			DroppedBudget:           plan.Metrics.DroppedBudget,
			DroppedSceneBudget:      plan.Metrics.DroppedSceneBudget,
			PlannedQueries:          plan.Metrics.PlannedQueries,
			DuplicateQueriesDropped: plan.Metrics.DuplicateQueriesDrop,
		}}
		for _, unit := range plan.Units {
			planByScene[unit.SceneID] = append(planByScene[unit.SceneID], unit)
			switch unit.Status {
			case scriptgen.ProductionUnitPlanned:
				if unit.RequiresMedia {
					report.Plan.SelectedMediaUnits++
				}
			case scriptgen.ProductionUnitReuse:
				report.Plan.Reused++
			case scriptgen.ProductionUnitDroppedBudget:
				report.Plan.DroppedBudget++
			case scriptgen.ProductionUnitDroppedSceneBudget:
				report.Plan.DroppedSceneBudget++
			}
		}
	}
	if consolidation != nil {
		for _, candidate := range consolidation.ProductionCandidates {
			candidatePriority[candidate.ID] = candidate.Priority
		}
	}
	seenEngine := false
	for _, input := range scenes {
		bundle := bundles[input.SceneID]
		entry := sceneReport{SceneID: input.SceneID, Text: input.Text}
		if bundle != nil {
			entry.Status = bundle.Status
			if !seenEngine {
				report.Engine = engineReport{SchemaVersion: bundle.SchemaVersion, Fingerprint: bundle.Fingerprint, Analyzers: analyzerReport{
					VisualNER: bundle.Analyzers.VisualNER, SemanticCore: bundle.Analyzers.SemanticCore,
					PhraseImpact: bundle.Analyzers.PhraseImpact, Lexicon: bundle.Analyzers.Lexicon,
				}}
				seenEngine = true
			}
			for _, entity := range bundle.Entities {
				entry.Entities = append(entry.Entities, entityReport{ID: entity.ID, Text: entity.Text, Kind: entity.Kind, Score: entity.Score, Evidence: entity.Evidence, Start: entity.Start, End: entity.End})
			}
			for _, query := range bundle.ContextualImageQueries {
				entry.ContextualQueries = append(entry.ContextualQueries, contextualReport(query))
			}
			for _, query := range bundle.ComposedImageQueries {
				entry.ComposedQueries = append(entry.ComposedQueries, composedReport(query, input.Text))
			}
			report.Inventory.ContextualQueries += len(bundle.ContextualImageQueries)
			report.Inventory.ComposedQueries += len(bundle.ComposedImageQueries)
		}
		for _, unit := range planByScene[input.SceneID] {
			entry.ProductionUnits = append(entry.ProductionUnits, unitReport{
				CandidateID: unit.CandidateID, Recipe: unit.Recipe, Rank: unit.Rank,
				Priority: candidatePriority[unit.CandidateID],
				Title:    unit.Title, Evidence: unit.Evidence, Status: unit.Status,
				SearchStatus: unit.SearchStatus, RequiresMedia: unit.RequiresMedia,
				RejectionReason: unit.DroppedReason, Queries: plannedQueries(unit.Queries),
			})
		}
		report.Scenes = append(report.Scenes, entry)
	}
	return report
}

func contextualReport(query scriptgen.SemanticContextualImageQuery) queryReport {
	out := queryReport{ID: query.ID, Query: query.Query, Evidence: query.Evidence}
	for _, component := range query.Components {
		out.Components = append(out.Components, componentReport{Kind: component.Kind, Type: component.ComponentType, Text: component.Text, Start: component.Start, End: component.End})
	}
	return out
}

func composedReport(query scriptgen.SemanticComposedImageQuery, source string) queryReport {
	out := queryReport{ID: query.ID, Query: query.Query}
	if query.Start >= 0 && query.End >= query.Start && query.End <= len(source) {
		out.Evidence = source[query.Start:query.End]
	}
	for _, component := range query.Components {
		out.Components = append(out.Components, componentReport{Kind: component.Kind, Type: component.ComponentType, Text: component.Text, Start: component.Start, End: component.End})
	}
	return out
}

func plannedQueries(queries []scriptgen.SemanticSearchQuery) []queryReport {
	out := make([]queryReport, 0, len(queries))
	for _, query := range queries {
		entry := queryReport{Query: query.Text, Evidence: query.Evidence}
		for _, component := range query.Components {
			entry.Components = append(entry.Components, componentReport{Kind: component.Kind, Type: component.ComponentType, Text: component.Text, Start: component.StartByte, End: component.EndByte})
		}
		out = append(out, entry)
	}
	return out
}
