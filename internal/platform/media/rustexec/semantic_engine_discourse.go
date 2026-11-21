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
	referenceByID := make(map[string]semanticEngineDiscourseReference, len(discourse.References))
	projected := &semanticscript.SemanticDiscourseProjection{Schema: discourse.Schema}
	counts := semanticscript.SemanticDiscourseCounts{}
	for _, reference := range discourse.References {
		text, ok := texts[reference.SceneID]
		if !ok || reference.ID == "" || reference.Mention == "" || reference.Evidence == "" ||
			!oneOf(reference.Status, "resolved", "ambiguous", "unresolved") ||
			!oneOf(reference.Type, "explicit_name", "surname", "pronoun", "nominal_reference") ||
			!groundedSpan(text, reference.StartByte, reference.EndByte, reference.Mention) ||
			!groundedSpan(text, reference.SentenceStartByte, reference.SentenceEndByte, reference.Evidence) ||
			reference.StartByte < reference.SentenceStartByte || reference.EndByte > reference.SentenceEndByte {
			return nil, fmt.Errorf("discourse reference %q is ungrounded or invalid", reference.ID)
		}
		if _, exists := referenceByID[reference.ID]; exists {
			return nil, fmt.Errorf("duplicate discourse reference id %q", reference.ID)
		}
		referenceByID[reference.ID] = reference
		for _, target := range reference.TargetEntityIDs {
			if _, exists := entityIDs[target]; !exists {
				return nil, fmt.Errorf("discourse reference %q has missing target %q", reference.ID, target)
			}
		}
		for _, candidate := range reference.CandidateEntityIDs {
			if _, exists := entityIDs[candidate]; !exists {
				return nil, fmt.Errorf("discourse reference %q has missing candidate %q", reference.ID, candidate)
			}
		}
		if reference.Status == "resolved" && len(reference.TargetEntityIDs) == 0 {
			return nil, fmt.Errorf("resolved discourse reference %q has no target", reference.ID)
		}
		if reference.Status != "resolved" && len(reference.TargetEntityIDs) != 0 {
			return nil, fmt.Errorf("unresolved discourse reference %q carries a target", reference.ID)
		}
		if reference.Status == "resolved" {
			counts.Resolved++
		} else if reference.Status == "ambiguous" {
			counts.Ambiguous++
		} else {
			counts.Unresolved++
		}
		projected.References = append(projected.References, semanticscript.SemanticDiscourseReference{
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
	for _, thread := range discourse.Threads {
		if thread.EntityID == "" || thread.CanonicalSurface == "" || thread.MentionCount != len(thread.Beats) || thread.MentionCount < 2 {
			return nil, fmt.Errorf("entity thread %q is incomplete", thread.EntityID)
		}
		if _, exists := entityIDs[thread.EntityID]; !exists {
			return nil, fmt.Errorf("entity thread references missing identity %q", thread.EntityID)
		}
		beats := make([]semanticscript.SemanticThreadBeat, 0, len(thread.Beats))
		for _, beat := range thread.Beats {
			reference, exists := referenceByID[beat.ReferenceID]
			if !exists || reference.Status != "resolved" || beat.SceneID != reference.SceneID ||
				!containsString(reference.TargetEntityIDs, thread.EntityID) ||
				!oneOf(beat.State, "starts", "continues", "returns") {
				return nil, fmt.Errorf("entity thread %q has an invalid beat", thread.EntityID)
			}
			beats = append(beats, semanticscript.SemanticThreadBeat{ReferenceID: beat.ReferenceID, SceneID: beat.SceneID, SentenceIndex: beat.SentenceIndex, State: beat.State})
		}
		projected.Threads = append(projected.Threads, semanticscript.SemanticEntityThread{
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
	for _, continuity := range discourse.SceneContinuity {
		if _, exists := texts[continuity.SceneID]; !exists || !oneOf(continuity.Status, "start", "continues", "shift", "empty") || continuity.Score < 0 || continuity.Score > 1 {
			return nil, fmt.Errorf("scene continuity for %q is invalid", continuity.SceneID)
		}
		for _, ids := range [][]string{continuity.ActiveEntityIDs, continuity.SharedEntityIDs, continuity.EnteredEntityIDs, continuity.ExitedEntityIDs} {
			for _, id := range ids {
				if _, exists := entityIDs[id]; !exists {
					return nil, fmt.Errorf("scene continuity references missing identity %q", id)
				}
			}
		}
		projected.SceneContinuity = append(projected.SceneContinuity, semanticscript.SemanticSceneContinuity{
			SceneID: continuity.SceneID, Status: continuity.Status, Score: continuity.Score,
			ActiveEntityIDs: append([]string(nil), continuity.ActiveEntityIDs...), SharedEntityIDs: append([]string(nil), continuity.SharedEntityIDs...),
			EnteredEntityIDs: append([]string(nil), continuity.EnteredEntityIDs...), ExitedEntityIDs: append([]string(nil), continuity.ExitedEntityIDs...),
		})
	}
	if counts.Total != discourse.Counts.Total || counts.Resolved != discourse.Counts.Resolved ||
		counts.Ambiguous != discourse.Counts.Ambiguous || counts.Unresolved != discourse.Counts.Unresolved ||
		counts.Threads != discourse.Counts.Threads || counts.CrossSceneThreads != discourse.Counts.CrossSceneThreads {
		return nil, fmt.Errorf("discourse aggregate counts disagree with the projections")
	}
	projected.Counts = semanticscript.SemanticDiscourseCounts{
		Total: counts.Total, Resolved: counts.Resolved, Ambiguous: counts.Ambiguous, Unresolved: counts.Unresolved,
		Threads: counts.Threads, CrossSceneThreads: counts.CrossSceneThreads,
	}
	return projected, nil
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
