package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

const semanticSceneOne = "Marco Rossi founded the studio. Revenue grew 25% against 18% in 2024."
const semanticSceneTwo = "The hearing was on 15 March 2008."

// semanticFixture is the manifest the fake worker returns: two scenes, one
// grounded bullet each, one comparable chart (25% vs 18%), one date.
const semanticFixture = `{
 "ok": true,
 "manifest": {
  "schema_version": "semantic_manifest.v3",
  "language": "en",
  "fingerprint": "0123456789abcdef",
  "analyzers": {"visualner":"visualner","semantic_core":"0.1.0","phrase_impact":"phrase_impact","entity_index":"entity_index.v1"},
  "entities": [
   {"local_id":"person_01","surface_key":"person:marco-rossi","canonical_surface":"Marco Rossi","canonical_id":"person:marco-rossi","link_status":"linked","canonical_name":"Marco Rossi","entity_type":"PERSON","role":"named_entity","disambiguation_gloss":"Italian founder","negative_constraints":[],"mentions":[{"scene_id":"scene_01","surface":"Marco Rossi","start_byte":0,"end_byte":11}],"search_intents":[{"kind":"identity","query":"Marco Rossi","lang":"en"}]}
  ],
  "scenes": [
   {
    "scene_id": "scene_01", "status": "valid", "reason": null,
    "entities": [
     {"id":"person_01","text":"Marco Rossi","type":"PERSON","start_byte":0,"end_byte":11,"evidence":"Marco Rossi","score":0.9,"provenance":"visualner"}
    ],
    "entity_facts": [],
    "identities": [
     {"id":"person_01","canonical_surface":"Marco Rossi","type":"PERSON","mentions":[{"scene_id":"scene_01","surface":"Marco Rossi","start_byte":0,"end_byte":11}]}
    ],
    "relations": {"co_mentions": [], "asserted_relations": []},
    "numeric_facts": [
     {"surface":"25%","type":"PERCENT","value":25,"start_byte":45,"end_byte":48,"evidence":"25%"},
     {"surface":"18%","type":"PERCENT","value":18,"start_byte":57,"end_byte":60,"evidence":"18%"}
    ],
    "temporal_facts": [],
    "bullets": [
     {"id":"bullet_01","text":"Marco Rossi founded the studio.","type":"SENTENCE","start_byte":0,"end_byte":31,"evidence":"Marco Rossi founded the studio.","score":1.0,"provenance":"visualner"},
     {"id":"bullet_02","text":"Revenue grew 25% against 18% in 2024.","type":"SENTENCE","start_byte":32,"end_byte":69,"evidence":"Revenue grew 25% against 18% in 2024.","score":1.0,"provenance":"visualner"}
    ],
    "short_phrases": [{"id":"phrase_01","text":"founded","type":"PHRASE","start_byte":12,"end_byte":19,"evidence":"founded","score":0.6,"provenance":"phrase_impact"}], "long_phrases": [], "quotes": [], "important_words": [],
    "important_segments": [], "composed_image_queries": [], "contextual_image_queries": [],
    "visual_proposals": [
     {"id":"scene_01_visual_001","proposal_type":"chart","entity_ids":[],"suggested_visual":"bar_comparison","status":"requires_semantic_validation","score":0.9,
      "chart_items":[
       {"label":"Revenue grew 25% against 18% in 2024.","text":"25%","kind":"PERCENT","value":25,"start_byte":45,"end_byte":48},
       {"label":"Revenue grew 25% against 18% in 2024.","text":"18%","kind":"PERCENT","value":18,"start_byte":57,"end_byte":60}
      ],
      "evidence":"Revenue grew 25% against 18% in 2024.","start_byte":32,"end_byte":69},
     {"id":"scene_01_visual_002","proposal_type":"entity_card","entity_ids":["person_01"],"visual_query_suggestions":["Marco Rossi","Marco Rossi founded the studio."],"suggested_visual":"entity_card","status":"requires_semantic_validation","score":0.6,"chart_items":[],"temporal_surfaces":[],"evidence":"Marco Rossi founded the studio.","start_byte":0,"end_byte":31}
    ],
    "semantic_groups": [{"id":"group_01","group_type":"enumeration","title":"Source points","members":[{"candidate_id":"bullet_01","sentence_index":0,"text":"Marco Rossi founded the studio.","evidence":"Marco Rossi founded the studio.","start_byte":0,"end_byte":31},{"candidate_id":"bullet_02","sentence_index":1,"text":"Revenue grew 25% against 18% in 2024.","evidence":"Revenue grew 25% against 18% in 2024.","start_byte":32,"end_byte":69}]}],
    "micro_summaries": [], "rejected_items": 0
   },
   {
    "scene_id": "scene_02", "status": "valid", "reason": null,
    "entities": [], "entity_facts": [], "identities": [],
    "relations": {"co_mentions": [], "asserted_relations": []},
    "numeric_facts": [],
    "temporal_facts": [
     {"surface":"15 March 2008","type":"DATE","normalized_date":"2008-03-15","precision":"day","start_byte":19,"end_byte":32,"evidence":"15 March 2008"}
    ],
    "bullets": [
     {"id":"bullet_01","text":"The hearing was on 15 March 2008.","type":"SENTENCE","start_byte":0,"end_byte":33,"evidence":"The hearing was on 15 March 2008.","score":1.0,"provenance":"visualner"}
    ],
    "short_phrases": [], "long_phrases": [], "quotes": [], "important_words": [],
    "important_segments": [], "composed_image_queries": [], "contextual_image_queries": [],
    "visual_proposals": [],
    "semantic_groups": [], "micro_summaries": [], "rejected_items": 0
   }
  ]
 }
}`

func semanticScenes() []scriptgen.SemanticSceneInput {
	return []scriptgen.SemanticSceneInput{
		{SceneID: "scene_01", Text: semanticSceneOne},
		{SceneID: "scene_02", Text: semanticSceneTwo},
	}
}

// semanticFixtureWithScenes projects the batch fixture onto the requested
// scene count. A scene-only extraction must return exactly the scenes in its
// request; reusing the two-scene batch fixture would make the Go boundary
// reject an otherwise valid worker response.
func semanticExtractionFixture(t *testing.T) []byte {
	t.Helper()
	raw := string(semanticFixtureWithScenes(t, 1))
	// SemanticCandidates requests a single scene with the stable id "scene".
	// Move the projection and every mention onto that id so the linked-entity
	// coverage check sees the same scene set the worker was asked to analyze.
	return []byte(strings.ReplaceAll(raw, `"scene_id":"scene_01"`, `"scene_id":"scene"`))
}

func semanticFixtureWithScenes(t *testing.T, count int) []byte {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal([]byte(semanticFixture), &response); err != nil {
		t.Fatal(err)
	}
	manifest, ok := response["manifest"].(map[string]any)
	if !ok {
		t.Fatal("fixture manifest is not an object")
	}
	scenes, ok := manifest["scenes"].([]any)
	if !ok || count < 1 || count > len(scenes) {
		t.Fatalf("fixture scene count %d cannot satisfy %d", len(scenes), count)
	}
	manifest["scenes"] = scenes[:count]
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSemanticEngineAnalysesEverySceneInOneBatchCall(t *testing.T) {
	// Guard the fixture offsets against drift.
	if semanticSceneOne[0:31] != "Marco Rossi founded the studio." ||
		semanticSceneOne[45:48] != "25%" || semanticSceneOne[57:60] != "18%" ||
		semanticSceneTwo[19:32] != "15 March 2008" {
		t.Fatalf("fixture offset drift")
	}
	runner := &semanticRunner{output: []byte(semanticFixture)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatalf("batch analysis failed: %v", err)
	}
	if len(bundles) != 2 {
		t.Fatalf("bundles=%d, want 2", len(bundles))
	}
	// A scene-only extraction is isolated from document-level identity context;
	// same-sentence text in another script must not inherit cached coreference.
	extractRunner := &semanticRunner{output: semanticExtractionFixture(t)}
	extractAdapter, err := NewSemanticEngineAdapter(executorForSemantic(extractRunner))
	if err != nil {
		t.Fatal(err)
	}
	visualEntities, err := extractAdapter.Extract(context.Background(), "en", semanticSceneOne, 3)
	if err != nil {
		t.Fatalf("extract scene entities: %v", err)
	}
	if len(visualEntities) != 1 || visualEntities[0].Text != "Marco Rossi" || visualEntities[0].Role != "named_entity" || runner.calls != 1 || extractRunner.calls != 1 {
		t.Fatalf("visual entities=%+v batch calls=%d extract calls=%d; want isolated scene analysis with the linked role", visualEntities, runner.calls, extractRunner.calls)
	}
	first := bundles["scene_01"]
	if len(first.Bullets) != 2 || first.Bullets[0].Text != "Marco Rossi founded the studio." {
		t.Fatalf("bullets: %+v", first.Bullets)
	}
	if len(first.Charts) != 2 || first.Charts[0].Text != "25%" || first.Charts[0].Value != 25 {
		t.Fatalf("charts: %+v", first.Charts)
	}
	if len(first.Numeric) != 2 || !first.Numeric[1].HasValue || first.Numeric[1].Value != 18 {
		t.Fatalf("numeric facts: %+v", first.Numeric)
	}
	if len(first.Entities) != 1 || first.Entities[0].IdentityID != "person_01" ||
		len(first.Identities) != 1 || first.Identities[0].Mentions[0].Surface != "Marco Rossi" {
		t.Fatalf("entities/identities were not preserved: entities=%+v identities=%+v", first.Entities, first.Identities)
	}
	if len(first.ShortPhrases) != 1 || first.ShortPhrases[0].Evidence != "founded" {
		t.Fatalf("short phrase was not preserved: %+v", first.ShortPhrases)
	}
	if first.SchemaVersion != "semantic_manifest.v3" || first.Fingerprint != "0123456789abcdef" ||
		first.Analyzers.SemanticCore != "0.1.0" || first.Status != "valid" || first.RejectedItems != 0 {
		t.Fatalf("manifest provenance was not preserved: %+v", first)
	}
	if len(first.Groups) != 1 || len(first.Groups[0].Members) != 2 || first.Groups[0].Members[1].Start != 32 {
		t.Fatalf("semantic group was not preserved with evidence offsets: %+v", first.Groups)
	}
	if len(first.Proposals) != 2 || len(first.Proposals[1].VisualQuerySuggestions) != 2 ||
		first.Proposals[1].VisualQuerySuggestions[0] != "Marco Rossi" {
		t.Fatalf("visual query suggestions were not preserved: %+v", first.Proposals)
	}
	second := bundles["scene_02"]
	if len(second.Temporal) != 1 || second.Temporal[0].NormalizedDate != "2008-03-15" || second.Temporal[0].Precision != "day" {
		t.Fatalf("temporal facts: %+v", second.Temporal)
	}

	// The request is ONE NDJSON line carrying both scenes.
	if strings.Count(strings.TrimSpace(string(runner.input)), "\n") != 0 {
		t.Fatalf("request must be a single NDJSON line: %q", runner.input)
	}
	var request semanticEngineRequest
	if err := json.Unmarshal(runner.input, &request); err != nil {
		t.Fatal(err)
	}
	if request.Operation != "analyze_script" || request.Language != "en" || len(request.Scenes) != 2 {
		t.Fatalf("request: %+v", request)
	}
}

func TestSemanticEnginePreservesGroundedEntityFacts(t *testing.T) {
	fact := `"id":"person_01_founded_01","subject_entity_id":"person_01","predicate":"founded","value":"Marco Rossi","type":"PERSON","normalized_value":null,"unit":null,"operator":null,"status":"asserted","sentence_index":0,"evidence":"Marco Rossi founded the studio.","start_byte":0,"end_byte":11,"evidence_start_byte":0,"evidence_end_byte":31`
	fixture := strings.Replace(semanticFixture, `"entity_facts": [],`, `"entity_facts": [{`+fact+`}],`, 1)
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(fixture)}))
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatal(err)
	}
	facts := bundles["scene_01"].EntityFacts
	if len(facts) != 1 || facts[0].SubjectEntityID != "person_01" || facts[0].Value != "Marco Rossi" || facts[0].EvidenceEnd != 31 {
		t.Fatalf("entity facts were not preserved: %+v", facts)
	}
}

func TestSemanticEnginePreservesFactBackedMicroSummaries(t *testing.T) {
	fact := `"id":"person_01_founded_01","subject_entity_id":"person_01","predicate":"founded","value":"Marco Rossi","type":"PERSON","normalized_value":null,"normalized_date":null,"target_entity_id":null,"unit":null,"operator":null,"status":"asserted","sentence_index":0,"evidence":"Marco Rossi founded the studio.","start_byte":0,"end_byte":11,"evidence_start_byte":0,"evidence_end_byte":31,"importance":0.9,"visual_concept_ids":["person_01"]`
	summary := `"id":"micro_001","kind":"micro_summary","text":"Marco Rossi: studio founder","evidence_fact_ids":["person_01_founded_01"],"subject_entity_id":"person_01","sentence_index":0,"evidence":"Marco Rossi founded the studio.","start_byte":0,"end_byte":31,"score":0.8`
	fixture := strings.Replace(semanticFixture, `"entity_facts": [],`, `"entity_facts": [{`+fact+`}],`, 1)
	fixture = strings.Replace(fixture, `"micro_summaries": [],`, `"micro_summaries": [{`+summary+`}],`, 1)
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(fixture)}))
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatal(err)
	}
	bundle := bundles["scene_01"]
	if len(bundle.MicroSummaries) != 1 {
		t.Fatalf("micro summaries: %+v", bundle.MicroSummaries)
	}
	sum := bundle.MicroSummaries[0]
	if sum.ID != "micro_001" || sum.Kind != "micro_summary" || sum.Text != "Marco Rossi: studio founder" ||
		sum.SubjectEntityID != "person_01" || sum.Evidence != "Marco Rossi founded the studio." ||
		sum.Start != 0 || sum.End != 31 || sum.Score != 0.8 || len(sum.EvidenceFactIDs) != 1 {
		t.Fatalf("micro summary lost provenance: %+v", sum)
	}
	if len(bundle.EntityFacts) != 1 || bundle.EntityFacts[0].Importance != 0.9 ||
		len(bundle.EntityFacts[0].VisualConceptIDs) != 1 || bundle.EntityFacts[0].VisualConceptIDs[0] != "person_01" {
		t.Fatalf("fact importance or visual concepts lost: %+v", bundle.EntityFacts)
	}

	dangling := strings.Replace(fixture, `"evidence_fact_ids":["person_01_founded_01"]`, `"evidence_fact_ids":["missing_fact"]`, 1)
	adapter, _ = NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(dangling)}))
	if _, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes()); err == nil || !strings.Contains(err.Error(), "unknown fact") {
		t.Fatalf("expected dangling fact rejection, got %v", err)
	}

	ungrounded := strings.Replace(fixture, `"evidence":"Marco Rossi founded the studio.","start_byte":0,"end_byte":31,"score":0.8`, `"evidence":"invented claim","start_byte":0,"end_byte":31,"score":0.8`, 1)
	adapter, _ = NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(ungrounded)}))
	if _, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes()); err == nil || !strings.Contains(err.Error(), "not grounded") {
		t.Fatalf("expected ungrounded micro summary rejection, got %v", err)
	}
}

func TestSemanticEngineRejectsUngroundedChartValue(t *testing.T) {
	broken := strings.Replace(semanticFixture, `"text":"18%","kind":"PERCENT","value":18`, `"text":"19%","kind":"PERCENT","value":18`, 1)
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(broken)}))
	_, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err == nil || !strings.Contains(err.Error(), "not grounded") {
		t.Fatalf("expected grounding failure, got %v", err)
	}
}

func TestSemanticEngineRejectsUngroundedVisualQuery(t *testing.T) {
	broken := strings.Replace(semanticFixture, `"visual_query_suggestions":["Marco Rossi","Marco Rossi founded the studio."]`, `"visual_query_suggestions":["Marco Rossi","unrelated generated query"]`, 1)
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(broken)}))
	_, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err == nil || !strings.Contains(err.Error(), "visual query") {
		t.Fatalf("expected an ungrounded query to be rejected, got %v", err)
	}
}

func TestSemanticEngineDropsUngroundedBulletButKeepsTheScene(t *testing.T) {
	broken := strings.Replace(semanticFixture, `"text":"Marco Rossi founded the studio.","type":"SENTENCE","start_byte":0,"end_byte":31,"evidence":"Marco Rossi founded the studio."`, `"text":"Marco Rossi founded the studio.","type":"SENTENCE","start_byte":0,"end_byte":31,"evidence":"Marco Rossi founded the studio"`, 1)
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(broken)}))
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatal(err)
	}
	// NO EVIDENCE -> NO CANDIDATE: the forged bullet is dropped, the rest of
	// the bundle survives.
	if len(bundles["scene_01"].Bullets) != 1 {
		t.Fatalf("bullets: %+v", bundles["scene_01"].Bullets)
	}
}

func TestSemanticEngineRejectsWrongSchemaAndBadInputs(t *testing.T) {
	wrong := strings.Replace(semanticFixture, "semantic_manifest.v3", "semantic_manifest.v1", 1)
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(wrong)}))
	if _, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes()); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("expected schema rejection, got %v", err)
	}

	okAdapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(semanticFixture)}))
	if _, err := okAdapter.SemanticBundles(context.Background(), "  ", []scriptgen.SemanticSceneInput{{SceneID: "s", Text: "text"}}); err == nil {
		t.Fatal("expected missing-language error")
	}
	if _, err := okAdapter.SemanticBundles(context.Background(), "en", []scriptgen.SemanticSceneInput{{SceneID: "s", Text: "a"}, {SceneID: "s", Text: "b"}}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate-scene error, got %v", err)
	}
	if _, err := NewSemanticEngineAdapter(nil); err == nil {
		t.Fatal("expected nil-executor error")
	}
}

func TestSemanticEngineFailsClosedOnIncompleteManifestAndDanglingIdentity(t *testing.T) {
	for _, collection := range []string{"short_phrases", "important_segments", "composed_image_queries", "contextual_image_queries"} {
		t.Run(collection, func(t *testing.T) {
			omitted := strings.Replace(semanticFixture, `"`+collection+`": [],`, ``, 1)
			if omitted == semanticFixture {
				t.Fatal("fixture collection was not removed")
			}
			adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(omitted)}))
			if _, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes()); err == nil || !strings.Contains(err.Error(), "omits required manifest collections") {
				t.Fatalf("expected missing collection rejection, got %v", err)
			}
		})
	}

	dangling := strings.Replace(semanticFixture, `"id":"person_01","canonical_surface":"Marco Rossi"`, `"id":"person_99","canonical_surface":"Marco Rossi"`, 1)
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(dangling)}))
	if _, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes()); err == nil || !strings.Contains(err.Error(), "missing from the linked entity projection") {
		t.Fatalf("expected dangling identity rejection, got %v", err)
	}
}

func TestSemanticEngineAllowsCrossSceneIdentityReference(t *testing.T) {
	// A scene can mention an entity whose linked identity is registered by a
	// later scene. Validation must see the document-level projection, not just
	// the identities attached to the scene that owns the entity mention.
	var response map[string]any
	if err := json.Unmarshal(semanticFixtureWithScenes(t, 2), &response); err != nil {
		t.Fatal(err)
	}
	manifest := response["manifest"].(map[string]any)
	scenes := manifest["scenes"].([]any)
	first := scenes[0].(map[string]any)
	second := scenes[1].(map[string]any)
	identities := first["identities"].([]any)
	first["identities"] = []any{}
	second["identities"] = identities
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: raw}))
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatalf("cross-scene identity reference was rejected: %v", err)
	}
	if len(bundles["scene_01"].Identities) != 1 || bundles["scene_01"].Identities[0].ID != "person_01" {
		t.Fatalf("scene_01 identities = %+v, want the document-level person_01", bundles["scene_01"].Identities)
	}
}

func TestSemanticEngineRefusedSceneReturnsEmptyBundle(t *testing.T) {
	refused := strings.Replace(semanticFixture, `"scene_id": "scene_02", "status": "valid", "reason": null`, `"scene_id": "scene_02", "status": "rejected", "reason": "unsupported language"`, 1)
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: []byte(refused)}))
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatal(err)
	}
	if bundle := bundles["scene_02"]; len(bundle.Bullets) != 0 || len(bundle.Temporal) != 0 {
		t.Fatalf("refused scene must stay empty: %+v", bundle)
	}
}

func TestSemanticGroupMembersAreGroundedAgainstTheirOwnScene(t *testing.T) {
	first := "Lo Stadio Aurora fu progettato."
	second := "La costruzione dello Stadio Aurora iniziò."
	start := strings.Index(second, "La costruzione")
	end := start + len("La costruzione dello Stadio Aurora iniziò.")
	groups, err := projectSemanticGroups("planning", map[string]string{
		"planning":     first,
		"construction": second,
	}, []semanticEngineGroup{{
		ID: "cross_process_facility", GroupType: "process", Title: "Processo: Stadio Aurora",
		Members: []semanticEngineGroupMember{
			{CandidateID: "phase_01", SceneID: "planning", SentenceIndex: 0, Text: first, Evidence: first, StartByte: 0, EndByte: len(first)},
			{CandidateID: "phase_02", SceneID: "construction", SentenceIndex: 0, Text: second[start:end], Evidence: second[start:end], StartByte: start, EndByte: end},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Members) != 2 || groups[0].Members[0].SceneID != "planning" || groups[0].Members[1].SceneID != "construction" {
		t.Fatalf("cross-scene member lost scene address: %+v", groups)
	}

	_, err = projectSemanticGroups("planning", map[string]string{"planning": first}, []semanticEngineGroup{{
		ID: "bad", GroupType: "process", Title: "bad",
		Members: []semanticEngineGroupMember{
			{CandidateID: "valid_member", SceneID: "planning", Text: first, Evidence: first, StartByte: 0, EndByte: len(first)},
			{CandidateID: "bad_member", SceneID: "not-requested", Text: "bad", Evidence: "bad", StartByte: 0, EndByte: 3},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "unrequested scene") {
		t.Fatalf("unrequested group scene must fail closed, got %v", err)
	}
}
