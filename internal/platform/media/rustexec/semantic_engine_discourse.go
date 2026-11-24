package rustexec

import (
	"fmt"

	semanticscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
)

func projectSemanticDiscourse(
	texts map[string]string,
	entities []semanticEngineLinkedEntity,
	discourse *semanticEngineDiscourseProjection,
) (*semanticscript.SemanticDiscourseProjection, error) {
	if discourse == nil {
		return nil, nil
	}
	if discourse.Schema != "discourse.v1" {
		return nil, fmt.Errorf("unsupported discourse schema %q", discourse.Schema)
	}
	entityIDs := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		entityIDs[entity.LocalID] = struct{}{}
	}
	references, referenceByID, counts, err := projectDiscourseReferences(discourse, texts, entityIDs)
	if err != nil {
		return nil, err
	}
	threads, threadCounts, err := projectDiscourseThreads(discourse, entityIDs, referenceByID)
	if err != nil {
		return nil, err
	}
	continuity, err := projectSceneContinuity(discourse, texts, entityIDs)
	if err != nil {
		return nil, err
	}
	counts.Threads = threadCounts.Threads
	counts.CrossSceneThreads = threadCounts.CrossSceneThreads
	if err := validateDiscourseCounts(counts, discourse.Counts); err != nil {
		return nil, err
	}
	return &semanticscript.SemanticDiscourseProjection{
		Schema:          discourse.Schema,
		References:      references,
		Threads:         threads,
		SceneContinuity: continuity,
		Counts:          counts,
	}, nil
}

func projectDiscourseReferences(
	discourse *semanticEngineDiscourseProjection,
	texts map[string]string,
	entityIDs map[string]struct{},
) ([]semanticscript.SemanticDiscourseReference, map[string]semanticEngineDiscourseReference, semanticscript.SemanticDiscourseCounts, error) {
	referenceByID := make(map[string]semanticEngineDiscourseReference, len(discourse.References))
	projected := make([]semanticscript.SemanticDiscourseReference, 0, len(discourse.References))
	counts := semanticscript.SemanticDiscourseCounts{}
	for _, reference := range discourse.References {
		text, ok := texts[reference.SceneID]
		if !ok || reference.ID == "" || reference.Mention == "" || reference.Evidence == "" ||
			!oneOf(reference.Status, "resolved", "ambiguous", "unresolved") ||
			!oneOf(reference.Type, "explicit_name", "surname", "pronoun", "nominal_reference") ||
			!groundedSpan(text, reference.StartByte, reference.EndByte, reference.Mention) ||
			!groundedSpan(text, reference.SentenceStartByte, reference.SentenceEndByte, reference.Evidence) ||
			reference.StartByte < reference.SentenceStartByte || reference.EndByte > reference.SentenceEndByte {
			return nil, nil, counts, fmt.Errorf("discourse reference %q is ungrounded or invalid", reference.ID)
		}
		if _, exists := referenceByID[reference.ID]; exists {
			return nil, nil, counts, fmt.Errorf("duplicate discourse reference id %q", reference.ID)
		}
		referenceByID[reference.ID] = reference
		for _, target := range reference.TargetEntityIDs {
			if _, exists := entityIDs[target]; !exists {
				return nil, nil, counts, fmt.Errorf("discourse reference %q has missing target %q", reference.ID, target)
			}
		}
		for _, candidate := range reference.CandidateEntityIDs {
			if _, exists := entityIDs[candidate]; !exists {
				return nil, nil, counts, fmt.Errorf("discourse reference %q has missing candidate %q", reference.ID, candidate)
			}
		}
		if reference.Status == "resolved" && len(reference.TargetEntityIDs) == 0 {
			return nil, nil, counts, fmt.Errorf("resolved discourse reference %q has no target", reference.ID)
		}
		if reference.Status != "resolved" && len(reference.TargetEntityIDs) != 0 {
			return nil, nil, counts, fmt.Errorf("unresolved discourse reference %q carries a target", reference.ID)
		}
		if reference.Status == "resolved" {
			counts.Resolved++
		} else if reference.Status == "ambiguous" {
			counts.Ambiguous++
		} else {
			counts.Unresolved++
		}
		projected = append(projected, semanticscript.SemanticDiscourseReference{
			ID: reference.ID, SceneID: reference.SceneID, Mention: reference.Mention, Type: reference.Type,
			Status: reference.Status, ResolutionBasis: derefString(reference.ResolutionBasis),
			Reason: derefString(reference.Reason), Evidence: reference.Evidence,
			SentenceIndex: reference.SentenceIndex, Start: reference.StartByte, End: reference.EndByte,
			SentenceStart: reference.SentenceStartByte, SentenceEnd: reference.SentenceEndByte,
			TargetEntityIDs:    append([]string(nil), reference.TargetEntityIDs...),
			CandidateEntityIDs: append([]string(nil), reference.CandidateEntityIDs...),
		})
	}
	counts.Total = len(discourse.References)
	return projected, referenceByID, counts, nil
}

func projectDiscourseThreads(
	discourse *semanticEngineDiscourseProjection,
	entityIDs map[string]struct{},
	referenceByID map[string]semanticEngineDiscourseReference,
) ([]semanticscript.SemanticEntityThread, semanticscript.SemanticDiscourseCounts, error) {
	threads := make([]semanticscript.SemanticEntityThread, 0, len(discourse.Threads))
	counts := semanticscript.SemanticDiscourseCounts{}
	for _, thread := range discourse.Threads {
		if thread.EntityID == "" || thread.CanonicalSurface == "" || thread.MentionCount != len(thread.Beats) || thread.MentionCount < 2 {
			return nil, counts, fmt.Errorf("entity thread %q is incomplete", thread.EntityID)
		}
		if _, exists := entityIDs[thread.EntityID]; !exists {
			return nil, counts, fmt.Errorf("entity thread references missing identity %q", thread.EntityID)
		}
		beats := make([]semanticscript.SemanticThreadBeat, 0, len(thread.Beats))
		for _, beat := range thread.Beats {
			reference, exists := referenceByID[beat.ReferenceID]
			if !exists || reference.Status != "resolved" || beat.SceneID != reference.SceneID ||
				!containsString(reference.TargetEntityIDs, thread.EntityID) ||
				!oneOf(beat.State, "starts", "continues", "returns") {
				return nil, counts, fmt.Errorf("entity thread %q has an invalid beat", thread.EntityID)
			}
			beats = append(beats, semanticscript.SemanticThreadBeat{ReferenceID: beat.ReferenceID, SceneID: beat.SceneID, SentenceIndex: beat.SentenceIndex, State: beat.State})
		}
		threads = append(threads, semanticscript.SemanticEntityThread{
			EntityID: thread.EntityID, CanonicalSurface: thread.CanonicalSurface, Type: thread.Type,
			SceneIDs: append([]string(nil), thread.SceneIDs...), FirstSceneID: thread.FirstSceneID,
			LastSceneID: thread.LastSceneID, MentionCount: thread.MentionCount, ReturnCount: thread.ReturnCount,
			Beats: beats,
		})
		counts.Threads++
		if thread.FirstSceneID != thread.LastSceneID {
			counts.CrossSceneThreads++
		}
	}
	return threads, counts, nil
}

func projectSceneContinuity(
	discourse *semanticEngineDiscourseProjection,
	texts map[string]string,
	entityIDs map[string]struct{},
) ([]semanticscript.SemanticSceneContinuity, error) {
	continuity := make([]semanticscript.SemanticSceneContinuity, 0, len(discourse.SceneContinuity))
	for _, scene := range discourse.SceneContinuity {
		if _, exists := texts[scene.SceneID]; !exists || !oneOf(scene.Status, "start", "continues", "shift", "empty") || scene.Score < 0 || scene.Score > 1 {
			return nil, fmt.Errorf("scene continuity for %q is invalid", scene.SceneID)
		}
		for _, ids := range [][]string{scene.ActiveEntityIDs, scene.SharedEntityIDs, scene.EnteredEntityIDs, scene.ExitedEntityIDs} {
			for _, id := range ids {
				if _, exists := entityIDs[id]; !exists {
					return nil, fmt.Errorf("scene continuity references missing identity %q", id)
				}
			}
		}
		continuity = append(continuity, semanticscript.SemanticSceneContinuity{
			SceneID: scene.SceneID, Status: scene.Status, Score: scene.Score,
			ActiveEntityIDs: append([]string(nil), scene.ActiveEntityIDs...), SharedEntityIDs: append([]string(nil), scene.SharedEntityIDs...),
			EnteredEntityIDs: append([]string(nil), scene.EnteredEntityIDs...), ExitedEntityIDs: append([]string(nil), scene.ExitedEntityIDs...),
		})
	}
	return continuity, nil
}

func validateDiscourseCounts(counts semanticscript.SemanticDiscourseCounts, wireCounts semanticEngineDiscourseCounts) error {
	if counts.Total != wireCounts.Total || counts.Resolved != wireCounts.Resolved ||
		counts.Ambiguous != wireCounts.Ambiguous || counts.Unresolved != wireCounts.Unresolved ||
		counts.Threads != wireCounts.Threads || counts.CrossSceneThreads != wireCounts.CrossSceneThreads {
		return fmt.Errorf("discourse aggregate counts disagree with the projections")
	}
	return nil
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
