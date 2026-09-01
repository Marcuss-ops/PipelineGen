package mediacert

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// mediterraneanSpec is the canonical golden-fixture Spec for the
// "Top 5 Mediterranean Foods You Should Try" scenario. It mirrors
// tests/fixtures/vidrush/mediterranean_top5_expected.json so the test and
// the fixture file stay in lockstep.
func mediterraneanSpec() Spec {
	return Spec{
		Segments:                  5,
		EntitiesPerSegment:        3,
		ImagesPerSegment:          3,
		VideoProvider:             script.VidRushProviderArtlist,
		AllowCrossSceneAssetReuse: false,
		SegmentsExpected: []SpecSegment{
			{ID: "mediterranean-01-greek-salad", Subject: "greek salad", RequiredConcepts: []string{"greek salad", "feta", "tomatoes", "olives"}, WinnerSubjectMatch: "greek salad"},
			{ID: "mediterranean-02-hummus", Subject: "hummus", RequiredConcepts: []string{"hummus", "chickpeas", "tahini", "olive oil"}, WinnerSubjectMatch: "hummus"},
			{ID: "mediterranean-03-sardines", Subject: "grilled sardines", RequiredConcepts: []string{"sardines", "lemon", "herbs"}, WinnerSubjectMatch: "sardines"},
			{ID: "mediterranean-04-shakshuka", Subject: "shakshuka", RequiredConcepts: []string{"shakshuka", "eggs", "tomatoes", "peppers"}, WinnerSubjectMatch: "shakshuka"},
			{ID: "mediterranean-05-paella", Subject: "seafood paella", RequiredConcepts: []string{"paella", "shrimp", "mussels", "rice"}, WinnerSubjectMatch: "paella"},
		},
	}
}

// greekSaladSceneIR is the compiled SceneIR for the greek-salad segment,
// reused across the pass and fail fixtures.
func greekSaladSceneIR() sceneir.SceneIR {
	ir, _ := sceneir.Compile(sceneir.CompileInput{
		Segment: script.CanonicalSegment{
			ID:       "mediterranean-01-greek-salad",
			Position: 0,
			Text:     "Greek salad contains tomatoes, feta cheese and olives.",
		},
	})
	return ir
}

// TestMediaCertRejectsTechnicallySuccessfulButWrongRun is the central
// MediaCert contract: a run with job.status=SUCCEEDED, 5 segments and 5
// Artlist winners must still return CERTIFIED=false when Greek Salad is
// bound to a boxing clip. This is the explicit rejection of the count-only
// test that declared success at a semantically broken pipeline.
func TestMediaCertRejectsTechnicallySuccessfulButWrongRun(t *testing.T) {
	spec := mediterraneanSpec()
	ir := greekSaladSceneIR()

	// Technically successful: SUCCEEDED, 5 segments, winner on every segment.
	result := MediaResult{
		JobStatus: "SUCCEEDED",
		Segments: []ResultSegment{
			{
				SegmentID:      ir.SegmentID,
				Position:       0,
				SourceText:     ir.SourceText,
				SourceTextHash: ir.SourceTextHash,
				SceneIR:        &ir,
				Insights: script.SegmentInsights{
					VisualProfile: &script.SegmentVisualProfile{Subject: "greek salad", Terms: []string{"feta", "tomatoes", "olives"}},
					Entities: []script.ExtractedEntity{
						{Value: "feta", Type: "CONCEPT", Confidence: 0.9},
						{Value: "tomatoes", Type: "CONCEPT", Confidence: 0.85},
						{Value: "olives", Type: "CONCEPT", Confidence: 0.8},
					},
					ImageQueries: []string{"feta", "tomatoes", "olives"},
				},
				Assets: script.SegmentAssetSelection{
					// The wrong winner: boxing bound to Greek Salad.
					PrimaryVideo: &script.SegmentAssetCandidate{
						SegmentID: "mediterranean-01-greek-salad",
						AssetID:   "artlist-boxing-001",
						Provider:  script.VidRushProviderArtlist,
						Entity:    "woman boxing",
						Query:     "woman boxing gloves",
						Score:     0.72,
					},
					SecondaryImages: []script.SegmentAssetCandidate{
						{SegmentID: "mediterranean-01-greek-salad", AssetID: "img-feta-1", Provider: script.VidRushProviderInternetImages, Query: "feta"},
						{SegmentID: "mediterranean-01-greek-salad", AssetID: "img-tomato-1", Provider: script.VidRushProviderInternetImages, Query: "tomatoes"},
						{SegmentID: "mediterranean-01-greek-salad", AssetID: "img-olive-1", Provider: script.VidRushProviderInternetImages, Query: "olives"},
					},
				},
			},
			// The remaining four segments are correct enough to pass their
			// own checks; the certification must still fail because the
			// Greek Salad segment is semantically wrong.
			correctMediterraneanSegment("mediterranean-02-hummus", 1, "hummus", "hummus", []string{"chickpeas", "tahini", "olive oil"}),
			correctMediterraneanSegment("mediterranean-03-sardines", 2, "grilled sardines", "sardines", []string{"sardines", "lemon", "herbs"}),
			correctMediterraneanSegment("mediterranean-04-shakshuka", 3, "shakshuka", "shakshuka", []string{"eggs", "tomatoes", "peppers"}),
			correctMediterraneanSegment("mediterranean-05-paella", 4, "seafood paella", "paella", []string{"shrimp", "mussels", "rice"}),
		},
	}

	report := Certify(spec, result)

	// The whole point: SUCCEEDED but CERTIFIED=false.
	require.Equal(t, "SUCCEEDED", report.JobStatus)
	require.False(t, report.Certified, "a SUCCEEDED run with boxing for Greek Salad must NOT be certified")

	// The ARTLIST RELEVANCE check must be the one that caught it.
	var artlistCheck CheckResult
	for _, c := range report.Checks {
		if c.Name == CheckArtlistRelevance {
			artlistCheck = c
		}
	}
	require.False(t, artlistCheck.Passed, "ARTLIST RELEVANCE must fail for the boxing winner")
	require.NotEmpty(t, artlistCheck.Violations)
	require.Contains(t, artlistCheck.Violations[0].Detail, "boxing")
}

// TestMediaCertCertifiesACorrectMediterraneanRun pins the positive path:
// when every segment has the correct subject, winner, entities and fanout,
// CERTIFIED=true. This is the regression baseline for the golden fixture.
func TestMediaCertCertifiesACorrectMediterraneanRun(t *testing.T) {
	spec := mediterraneanSpec()
	result := MediaResult{
		JobStatus: "SUCCEEDED",
		Segments: []ResultSegment{
			correctMediterraneanSegment("mediterranean-01-greek-salad", 0, "greek salad", "greek salad", []string{"feta", "tomatoes", "olives"}),
			correctMediterraneanSegment("mediterranean-02-hummus", 1, "hummus", "hummus", []string{"chickpeas", "tahini", "olive oil"}),
			correctMediterraneanSegment("mediterranean-03-sardines", 2, "grilled sardines", "sardines", []string{"sardines", "lemon", "herbs"}),
			correctMediterraneanSegment("mediterranean-04-shakshuka", 3, "shakshuka", "shakshuka", []string{"eggs", "tomatoes", "peppers"}),
			correctMediterraneanSegment("mediterranean-05-paella", 4, "seafood paella", "paella", []string{"shrimp", "mussels", "rice"}),
		},
	}
	report := Certify(spec, result)
	require.True(t, report.Certified, "a fully correct Mediterranean run must be certified")
}

// TestMediaCertRejectsCrossSceneAssetReuse pins the same Artlist clip on
// scene 0 and scene 4 failure: when the spec forbids reuse, the second
// binding must be rejected.
func TestMediaCertRejectsCrossSceneAssetReuse(t *testing.T) {
	spec := mediterraneanSpec()
	result := MediaResult{
		JobStatus: "SUCCEEDED",
		Segments: []ResultSegment{
			correctMediterraneanSegment("mediterranean-01-greek-salad", 0, "greek salad", "greek salad", []string{"feta", "tomatoes", "olives"}),
			correctMediterraneanSegment("mediterranean-02-hummus", 1, "hummus", "hummus", []string{"chickpeas", "tahini", "olive oil"}),
			correctMediterraneanSegment("mediterranean-03-sardines", 2, "grilled sardines", "sardines", []string{"sardines", "lemon", "herbs"}),
			correctMediterraneanSegment("mediterranean-04-shakshuka", 3, "shakshuka", "shakshuka", []string{"eggs", "tomatoes", "peppers"}),
			correctMediterraneanSegment("mediterranean-05-paella", 4, "seafood paella", "paella", []string{"shrimp", "mussels", "rice"}),
		},
	}
	// Reuse the Greek Salad winner asset on the paella segment.
	result.Segments[4].Assets.PrimaryVideo.AssetID = result.Segments[0].Assets.PrimaryVideo.AssetID
	result.Segments[4].Assets.PrimaryVideo.SegmentID = "mediterranean-05-paella"
	report := Certify(spec, result)
	require.False(t, report.Certified, "cross-scene asset reuse must fail certification")
	var reuse CheckResult
	for _, c := range report.Checks {
		if c.Name == CheckCrossSceneReuse {
			reuse = c
		}
	}
	require.False(t, reuse.Passed)
}

// TestMediaCertRejectsNullVisualProfiles pins the visual_profile=null 5/5
// failure: segments without a SceneIR and without an Insights.VisualProfile
// must fail SEMANTIC PROFILES.
func TestMediaCertRejectsNullVisualProfiles(t *testing.T) {
	spec := mediterraneanSpec()
	result := MediaResult{
		JobStatus: "SUCCEEDED",
		Segments: []ResultSegment{
			{SegmentID: "mediterranean-01-greek-salad", Position: 0, SourceText: "Greek salad contains tomatoes, feta cheese and olives.", SourceTextHash: script.ComputeCanonicalSegmentTextHash("Greek salad contains tomatoes, feta cheese and olives.")},
			{SegmentID: "mediterranean-02-hummus", Position: 1, SourceText: "Hummus is made with chickpeas, tahini and olive oil.", SourceTextHash: script.ComputeCanonicalSegmentTextHash("Hummus is made with chickpeas, tahini and olive oil.")},
			{SegmentID: "mediterranean-03-sardines", Position: 2, SourceText: "Grilled sardines with lemon and herbs.", SourceTextHash: script.ComputeCanonicalSegmentTextHash("Grilled sardines with lemon and herbs.")},
			{SegmentID: "mediterranean-04-shakshuka", Position: 3, SourceText: "Shakshuka has eggs, tomatoes and peppers.", SourceTextHash: script.ComputeCanonicalSegmentTextHash("Shakshuka has eggs, tomatoes and peppers.")},
			{SegmentID: "mediterranean-05-paella", Position: 4, SourceText: "Seafood paella with shrimp, mussels and rice.", SourceTextHash: script.ComputeCanonicalSegmentTextHash("Seafood paella with shrimp, mussels and rice.")},
		},
	}
	report := Certify(spec, result)
	require.False(t, report.Certified)
	var profiles CheckResult
	for _, c := range report.Checks {
		if c.Name == CheckSemanticProfiles {
			profiles = c
		}
	}
	require.False(t, profiles.Passed)
	require.Equal(t, 0, profiles.PassCount)
	require.Equal(t, 5, profiles.TotalCount)
}

// TestMediaCertRejectsTamperedSourceText pins the SOURCE IMMUTABILITY check:
// a stamped source_text_hash that no longer matches a fresh hash of the
// current source_text must fail.
func TestMediaCertRejectsTamperedSourceText(t *testing.T) {
	spec := mediterraneanSpec()
	ir := greekSaladSceneIR()
	seg := correctMediterraneanSegment(ir.SegmentID, 0, "greek salad", "greek salad", []string{"feta", "tomatoes", "olives"})
	seg.SceneIR = &ir
	// Tamper: rewrite the source text AFTER compilation so the hash mismatches.
	seg.SourceText = "Get ready to dive into the vibrant world of Greek cuisine..."
	seg.SceneIR.SourceText = seg.SourceText
	result := MediaResult{JobStatus: "SUCCEEDED", Segments: []ResultSegment{seg}}
	report := Certify(spec, result)
	require.False(t, report.Certified)
	var immut CheckResult
	for _, c := range report.Checks {
		if c.Name == CheckSourceImmutability {
			immut = c
		}
	}
	require.False(t, immut.Passed)
}

// TestHumanReportContainsCertifiedLine verifies the human-readable report
// ends with the CERTIFIED= line the pre-push gate greps for.
func TestHumanReportContainsCertifiedLine(t *testing.T) {
	spec := mediterraneanSpec()
	result := MediaResult{JobStatus: "SUCCEEDED", Segments: []ResultSegment{correctMediterraneanSegment("mediterranean-01-greek-salad", 0, "greek salad", "greek salad", []string{"feta", "tomatoes", "olives"})}}
	out := HumanReport(Certify(spec, result))
	require.Contains(t, out, "CERTIFIED=true")
}

// correctMediterraneanSegment builds a fully-correct ResultSegment for the
// spec. It is the helper for both the positive path and the "rest of the
// run is correct" baseline of the fail fixtures.
func correctMediterraneanSegment(id string, position int, subject, winnerEntity string, terms []string) ResultSegment {
	entities := make([]script.ExtractedEntity, 0, len(terms))
	for _, term := range terms {
		entities = append(entities, script.ExtractedEntity{Value: term, Type: "CONCEPT", Confidence: 0.9})
	}
	imageQueries := append([]string(nil), terms...)
	sourceText := sourceTextForSegment(id)
	ir, _ := sceneir.Compile(sceneir.CompileInput{
		Segment: script.CanonicalSegment{ID: id, Position: position, Text: sourceText},
	})
	return ResultSegment{
		SegmentID:      id,
		Position:       position,
		SourceText:     ir.SourceText,
		SourceTextHash: ir.SourceTextHash,
		SceneIR:        &ir,
		Insights: script.SegmentInsights{
			VisualProfile: &script.SegmentVisualProfile{Subject: subject, Terms: terms},
			Entities:      entities,
			ImageQueries:  imageQueries,
		},
		Assets: script.SegmentAssetSelection{
			PrimaryVideo: &script.SegmentAssetCandidate{
				SegmentID: id,
				AssetID:   "artlist-" + id,
				Provider:  script.VidRushProviderArtlist,
				Entity:    winnerEntity,
				Query:     subject,
				Score:     0.9,
			},
			SecondaryImages: []script.SegmentAssetCandidate{
				{SegmentID: id, AssetID: "img-" + id + "-1", Provider: script.VidRushProviderInternetImages, Query: terms[0]},
				{SegmentID: id, AssetID: "img-" + id + "-2", Provider: script.VidRushProviderInternetImages, Query: terms[1]},
				{SegmentID: id, AssetID: "img-" + id + "-3", Provider: script.VidRushProviderInternetImages, Query: terms[2]},
			},
		},
	}
}

func sourceTextForSegment(id string) string {
	switch id {
	case "mediterranean-01-greek-salad":
		return "Greek salad contains tomatoes, feta cheese and olives."
	case "mediterranean-02-hummus":
		return "Hummus is traditionally made with chickpeas, tahini, lemon juice and olive oil."
	case "mediterranean-03-sardines":
		return "Grilled sardines are seasoned with lemon and fresh herbs."
	case "mediterranean-04-shakshuka":
		return "Shakshuka features eggs poached in tomatoes and peppers."
	case "mediterranean-05-paella":
		return "Seafood paella combines shrimp, mussels and rice."
	}
	return ""
}
