package mediamemory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// TICKET-16 parity gate: the Rust mediasampler ranker must reproduce the Go
// decision helpers exactly. The fixtures are GENERATED from this package (the
// Go side is the oracle), committed under `BrainRust/contract/media-ranker/v1`,
// and asserted by `mediasampler/src/rank_parity.rs` on the other side.
//
// Regenerate with:
//
//	UPDATE_MEDIA_RANKER_FIXTURES=1 go test ./internal/capabilities/mediamemory \
//	  -run TestMediaRankerRustFixtures -count=1
//
// The same test then reads the file back and fails if the Go helpers drift.

const mediaRankerFixtureSchema = "media-ranker.v1"

type mediaRankerFixtureFile struct {
	SchemaVersion string                   `json:"schema_version"`
	Cases         []mediaRankerFixtureCase `json:"cases"`
}

type mediaRankerFixtureCase struct {
	Name  string                     `json:"name"`
	Input mediaRankerFixtureInput    `json:"input"`
	Want  mediaRankerFixtureExpected `json:"expected"`
}

type mediaRankerFixtureInput struct {
	SceneDurationMs int64                    `json:"scene_duration_ms"`
	PrevVideoID     string                   `json:"prev_video_id,omitempty"`
	SlotKind        string                   `json:"slot_kind"`
	Candidates      []mediaRankerFixtureCand `json:"candidates"`
}

type mediaRankerFixtureCand struct {
	CandidateID           string                     `json:"candidate_id"`
	AssetID               string                     `json:"asset_id"`
	VideoID               string                     `json:"video_id,omitempty"`
	Provider              string                     `json:"provider,omitempty"`
	SourceURL             string                     `json:"source_url,omitempty"`
	MediaType             string                     `json:"media_type,omitempty"`
	DurationMs            int64                      `json:"duration_ms,omitempty"`
	CandidateScore        float64                    `json:"candidate_score,omitempty"`
	RightsStatus          string                     `json:"rights_status,omitempty"`
	DiscoveryStatus       string                     `json:"discovery_status,omitempty"`
	MaterializationStatus string                     `json:"materialization_status,omitempty"`
	ChannelID             string                     `json:"channel_id,omitempty"`
	Binding               *mediaRankerFixtureBinding `json:"binding,omitempty"`
}

type mediaRankerFixtureBinding struct {
	ID             string  `json:"id,omitempty"`
	AssetID        string  `json:"asset_id"`
	StartMs        int64   `json:"start_ms,omitempty"`
	EndMs          int64   `json:"end_ms,omitempty"`
	ApprovalStatus string  `json:"approval_status,omitempty"`
	ManualScore    float64 `json:"manual_score,omitempty"`
	SemanticScore  float64 `json:"semantic_score,omitempty"`
	QualityScore   float64 `json:"quality_score,omitempty"`
	SuccessScore   float64 `json:"success_score,omitempty"`
}

type mediaRankerFixtureExpected struct {
	Ranked        []mediaRankerFixtureRanked `json:"ranked"`
	Drops         []mediaRankerFixtureDrop   `json:"drops"`
	WinnerAssetID string                     `json:"winner_asset_id,omitempty"`
}

type mediaRankerFixtureRanked struct {
	AssetID      string                           `json:"asset_id"`
	CandidateID  string                           `json:"candidate_id"`
	SourceURL    string                           `json:"source_url"`
	Provider     string                           `json:"provider"`
	MediaType    string                           `json:"media_type"`
	DurationMs   int64                            `json:"duration_ms"`
	RightsStatus string                           `json:"rights_status"`
	FinalScore   float64                          `json:"final_score"`
	Verdict      string                           `json:"verdict"`
	Reason       string                           `json:"reason"`
	Breakdown    mediaRankerFixtureScoreBreakdown `json:"breakdown"`
}

type mediaRankerFixtureDrop struct {
	AssetID     string `json:"asset_id"`
	CandidateID string `json:"candidate_id"`
	Gate        string `json:"gate,omitempty"`
	Reason      string `json:"reason"`
}

type mediaRankerFixtureScoreBreakdown struct {
	Semantic          float64 `json:"semantic"`
	ExactMatch        float64 `json:"exact_match"`
	Visual            float64 `json:"visual"`
	ManualApproval    float64 `json:"manual_approval"`
	Quality           float64 `json:"quality"`
	HistoricalSuccess float64 `json:"historical_success"`
	DurationFit       float64 `json:"duration_fit"`
	RepetitionPenalty float64 `json:"repetition_penalty"`
	RightsPenalty     float64 `json:"rights_penalty"`
}

func mediaRankerFixturePath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the fixture test source")
	}
	return filepath.Clean(filepath.Join(
		filepath.Dir(source), "..", "..", "..", "..",
		"BrainRust", "contract", "media-ranker", "v1", "fixtures.json",
	))
}

// fixtureCandidates projects the fixture envelope onto the canonical types.
func fixtureCandidates(cs []mediaRankerFixtureCand) []FilteredCandidate {
	out := make([]FilteredCandidate, 0, len(cs))
	for _, c := range cs {
		fc := FilteredCandidate{
			Candidate: MediaCandidate{
				ID:                    c.CandidateID,
				AssetID:               c.AssetID,
				VideoID:               c.VideoID,
				Provider:              c.Provider,
				SourceURL:             c.SourceURL,
				MediaType:             c.MediaType,
				DurationMs:            c.DurationMs,
				CandidateScore:        c.CandidateScore,
				RightsStatus:          RightsStatus(c.RightsStatus),
				DiscoveryStatus:       DiscoveryStatus(c.DiscoveryStatus),
				MaterializationStatus: MaterializationStatus(c.MaterializationStatus),
				ChannelID:             c.ChannelID,
			},
		}
		if c.Binding != nil {
			fc.Binding = MediaBinding{
				ID:             c.Binding.ID,
				AssetID:        c.Binding.AssetID,
				StartMs:        c.Binding.StartMs,
				EndMs:          c.Binding.EndMs,
				ApprovalStatus: ApprovalStatus(c.Binding.ApprovalStatus),
				ManualScore:    c.Binding.ManualScore,
				SemanticScore:  c.Binding.SemanticScore,
				QualityScore:   c.Binding.QualityScore,
				SuccessScore:   c.Binding.SuccessScore,
			}
		}
		out = append(out, fc)
	}
	return out
}

// runMediaRankerFixture executes the canonical Go pipeline for one slot and
// projects the answer onto the wire shape the Rust worker returns.
func runMediaRankerFixture(t *testing.T, in mediaRankerFixtureInput) mediaRankerFixtureExpected {
	t.Helper()
	slot, ok := mediaSlotKindFromString(in.SlotKind)
	if !ok {
		t.Fatalf("unknown slot_kind %q", in.SlotKind)
	}
	scene := SceneSpec{ID: "scene-fixture", DurationMs: in.SceneDurationMs, Slots: []media.SlotKind{slot}}
	ranker := NewDefaultRanker(nil, nil)

	filters := buildFilterFlags(nil, fixtureCandidates(in.Candidates), nil, scene, slot)
	filtered, err := ranker.Filter(nil, filters)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	kept := make(map[string]bool, len(filtered))
	for _, fc := range filtered {
		kept[fc.Candidate.ID] = true
	}

	inputs := make([]RankingInput, 0, len(filtered))
	for _, fc := range filtered {
		inputs = append(inputs, buildRankingInput(scene, fc))
	}
	inputs = PopulateRepetitionPenalty(inputs, in.PrevVideoID)

	scored := make([]rankedCandidate, 0, len(inputs))
	for _, rankingInput := range inputs {
		score, scoreErr := ranker.Score(nil, rankingInput)
		if scoreErr != nil {
			t.Fatalf("score: %v", scoreErr)
		}
		if score.Verdict == VerdictDrop {
			continue
		}
		scored = append(scored, rankedCandidate{
			fc:  FilteredCandidate{Candidate: rankingInput.Candidate, Binding: rankingInput.Binding},
			out: score,
		})
	}
	sortByFinalScoreDesc(scored)

	want := mediaRankerFixtureExpected{
		Ranked: make([]mediaRankerFixtureRanked, 0, len(scored)),
		Drops:  make([]mediaRankerFixtureDrop, 0),
	}
	for _, c := range in.Candidates {
		if !kept[c.CandidateID] {
			want.Drops = append(want.Drops, mediaRankerFixtureDrop{
				AssetID:     c.AssetID,
				CandidateID: c.CandidateID,
				Reason:      "dropped by the pre-rank filter",
			})
			continue
		}
		found := false
		for _, rc := range scored {
			if rc.fc.Candidate.ID == c.CandidateID {
				found = true
				break
			}
		}
		if !found {
			want.Drops = append(want.Drops, mediaRankerFixtureDrop{
				AssetID:     c.AssetID,
				CandidateID: c.CandidateID,
				Reason:      "score below drop threshold (penalty or malformed)",
			})
		}
	}
	for _, rc := range scored {
		seat := buildRankingInput(scene, rc.fc)
		penaltyInputs := PopulateRepetitionPenalty([]RankingInput{seat}, in.PrevVideoID)
		seat = penaltyInputs[0]
		want.Ranked = append(want.Ranked, mediaRankerFixtureRanked{
			AssetID:      rc.fc.Candidate.AssetID,
			CandidateID:  rc.fc.Candidate.ID,
			SourceURL:    rc.fc.Candidate.SourceURL,
			Provider:     rc.fc.Candidate.Provider,
			MediaType:    rc.fc.Candidate.MediaType,
			DurationMs:   rc.fc.Candidate.DurationMs,
			RightsStatus: string(rc.fc.Candidate.RightsStatus),
			FinalScore:   rc.out.FinalScore,
			Verdict:      string(rc.out.Verdict),
			Reason:       rc.out.Reason,
			Breakdown: mediaRankerFixtureScoreBreakdown{
				Semantic:          seat.SemanticScore,
				ExactMatch:        seat.ExactMatchScore,
				Visual:            seat.VisualScore,
				ManualApproval:    seat.ManualApprovalScore,
				Quality:           seat.QualityScore,
				HistoricalSuccess: seat.HistoricalSuccessScore,
				DurationFit:       seat.DurationFitScore,
				RepetitionPenalty: seat.RepetitionPenalty,
				RightsPenalty:     seat.RightsPenalty,
			},
		})
	}
	if winner := PickTopFromRose(scored, in.PrevVideoID); winner.fc.Candidate.AssetID != "" {
		want.WinnerAssetID = winner.fc.Candidate.AssetID
	}
	return want
}

func mediaRankerFixtureCases(t *testing.T) []mediaRankerFixtureCase {
	t.Helper()
	verified := func(assetID string) mediaRankerFixtureCand {
		return mediaRankerFixtureCand{
			CandidateID:           "candidate-" + assetID,
			AssetID:               assetID,
			Provider:              ProviderArtlist,
			SourceURL:             "https://cdn.example/" + assetID + ".mp4",
			MediaType:             "video",
			DurationMs:            4000,
			CandidateScore:        0.55,
			RightsStatus:          string(RightsVerified),
			DiscoveryStatus:       string(DiscoverySearched),
			MaterializationStatus: string(MaterializationWarm),
		}
	}
	approved := func(assetID string) *mediaRankerFixtureBinding {
		return &mediaRankerFixtureBinding{
			ID:             "binding-" + assetID,
			AssetID:        assetID,
			StartMs:        0,
			EndMs:          3800,
			ApprovalStatus: string(ApprovalApproved),
			ManualScore:    0.95,
			SemanticScore:  0.82,
			QualityScore:   0.71,
			SuccessScore:   0.66,
		}
	}
	cases := []mediaRankerFixtureCase{
		{
			Name: "empty_pool",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates:      []mediaRankerFixtureCand{},
			},
		},
		{
			Name: "single_candidate_only",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates:      []mediaRankerFixtureCand{verified("asset-solo")},
			},
		},
		{
			Name: "repeated_source_penalty",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				PrevVideoID:     "asset-hot",
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates: []mediaRankerFixtureCand{
					func() mediaRankerFixtureCand {
						c := verified("asset-hot")
						c.CandidateScore = 0.9
						return c
					}(),
					verified("asset-cold"),
					func() mediaRankerFixtureCand {
						c := verified("asset-mid")
						c.CandidateScore = 0.5
						return c
					}(),
				},
			},
		},
		{
			Name: "tie_break_by_asset_id",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates: []mediaRankerFixtureCand{
					verified("zeta-asset"),
					verified("alpha-asset"),
					verified("mike-asset"),
				},
			},
		},
		{
			Name: "binding_seats_approved_and_pending",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				SlotKind:        string(media.SlotSecondaryImage),
				Candidates: []mediaRankerFixtureCand{
					func() mediaRankerFixtureCand {
						c := verified("asset-approved")
						c.MediaType = "image"
						c.Binding = approved("asset-approved")
						return c
					}(),
					func() mediaRankerFixtureCand {
						c := verified("asset-pending")
						c.MediaType = "image"
						c.Binding = &mediaRankerFixtureBinding{
							ID:             "binding-asset-pending",
							AssetID:        "asset-pending",
							ApprovalStatus: string(ApprovalPending),
							ManualScore:    1.0,
							SemanticScore:  0.4,
							QualityScore:   0.3,
							SuccessScore:   0.2,
						}
						return c
					}(),
				},
			},
		},
		{
			Name: "gate_drops",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				SlotKind:        string(media.SlotSecondaryImage),
				Candidates: []mediaRankerFixtureCand{
					func() mediaRankerFixtureCand { c := verified("asset-ok-image"); c.MediaType = "image"; return c }(),
					func() mediaRankerFixtureCand {
						c := verified("asset-unknown-rights")
						c.MediaType = "image"
						c.RightsStatus = string(RightsUnknown)
						return c
					}(),
					func() mediaRankerFixtureCand {
						c := verified("asset-cold")
						c.MediaType = "image"
						c.MaterializationStatus = string(MaterializationCold)
						return c
					}(),
					func() mediaRankerFixtureCand {
						c := verified("asset-failed")
						c.MediaType = "image"
						c.MaterializationStatus = string(MaterializationFailed)
						return c
					}(),
					func() mediaRankerFixtureCand { c := verified("asset-raw-format"); c.MediaType = "raw"; return c }(),
					func() mediaRankerFixtureCand { c := verified("asset-video-in-image-slot"); return c }(),
					func() mediaRankerFixtureCand { c := verified(""); c.CandidateID = "candidate-malformed"; return c }(),
					func() mediaRankerFixtureCand {
						c := verified("asset-unknown-state")
						c.MediaType = "image"
						c.DiscoveryStatus = "garbage"
						return c
					}(),
				},
			},
		},
		{
			Name: "duration_fit_edges",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates: []mediaRankerFixtureCand{
					func() mediaRankerFixtureCand { c := verified("asset-fit-perfect"); c.DurationMs = 4000; return c }(),
					func() mediaRankerFixtureCand { c := verified("asset-fit-half"); c.DurationMs = 2800; return c }(),
					func() mediaRankerFixtureCand { c := verified("asset-fit-none"); c.DurationMs = 1000; return c }(),
					func() mediaRankerFixtureCand { c := verified("asset-fit-zero"); c.DurationMs = 0; return c }(),
				},
			},
		},
		{
			Name: "cross_channel_equal_scores",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates: []mediaRankerFixtureCand{
					func() mediaRankerFixtureCand { c := verified("asset-channel-b"); c.ChannelID = "channel-b"; return c }(),
					func() mediaRankerFixtureCand { c := verified("asset-channel-a"); c.ChannelID = "channel-a"; return c }(),
					func() mediaRankerFixtureCand {
						c := verified("asset-channel-c")
						c.ChannelID = "channel-c"
						c.Provider = ProviderYouTube
						return c
					}(),
				},
			},
		},
		{
			Name: "drop_band_negative_score",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				PrevVideoID:     "asset-negative",
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates: []mediaRankerFixtureCand{
					func() mediaRankerFixtureCand {
						c := verified("asset-negative")
						c.RightsStatus = ""
						c.DurationMs = 100
						c.Binding = &mediaRankerFixtureBinding{ID: "binding-negative", AssetID: "asset-negative"}
						return c
					}(),
				},
			},
		},
		{
			Name: "diversity_swap_within_delta",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				PrevVideoID:     "asset-strong",
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates: []mediaRankerFixtureCand{
					func() mediaRankerFixtureCand {
						c := verified("asset-strong")
						c.Binding = &mediaRankerFixtureBinding{
							ID: "binding-strong", AssetID: "asset-strong",
							ApprovalStatus: string(ApprovalApproved),
							ManualScore:    1.0, SemanticScore: 1.0,
							QualityScore: 1.0, SuccessScore: 1.0,
						}
						return c
					}(),
					func() mediaRankerFixtureCand {
						c := verified("asset-tied")
						c.Binding = &mediaRankerFixtureBinding{
							ID: "binding-tied", AssetID: "asset-tied",
							SemanticScore: 1.0, QualityScore: 0.0, SuccessScore: 0.0,
						}
						return c
					}(),
				},
			},
		},
		{
			Name: "diversity_keeps_far_top_one",
			Input: mediaRankerFixtureInput{
				SceneDurationMs: 4000,
				PrevVideoID:     "asset-strong",
				SlotKind:        string(media.SlotPrimaryVideo),
				Candidates: []mediaRankerFixtureCand{
					func() mediaRankerFixtureCand {
						c := verified("asset-strong")
						c.Binding = &mediaRankerFixtureBinding{
							ID: "binding-strong", AssetID: "asset-strong",
							ApprovalStatus: string(ApprovalApproved),
							ManualScore:    1.0, SemanticScore: 1.0,
							QualityScore: 1.0, SuccessScore: 1.0,
						}
						return c
					}(),
					func() mediaRankerFixtureCand {
						c := verified("asset-far")
						c.Binding = &mediaRankerFixtureBinding{
							ID: "binding-far", AssetID: "asset-far",
							SemanticScore: 0.0, QualityScore: 0.0, SuccessScore: 0.0,
						}
						return c
					}(),
				},
			},
		},
	}
	for index := range cases {
		cases[index].Want = runMediaRankerFixture(t, cases[index].Input)
	}
	return cases
}

func TestMediaRankerRustFixtures(t *testing.T) {
	path := mediaRankerFixturePath(t)
	cases := mediaRankerFixtureCases(t)

	if os.Getenv("UPDATE_MEDIA_RANKER_FIXTURES") == "1" {
		payload, err := json.MarshalIndent(mediaRankerFixtureFile{
			SchemaVersion: mediaRankerFixtureSchema,
			Cases:         cases,
		}, "", "  ")
		if err != nil {
			t.Fatalf("marshal fixtures: %v", err)
		}
		payload = append(payload, '\n')
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			t.Fatalf("write fixtures: %v", err)
		}
		t.Logf("wrote %d fixtures to %s", len(cases), path)
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixtures %s: %v", path, err)
	}
	var file mediaRankerFixtureFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	if file.SchemaVersion != mediaRankerFixtureSchema {
		t.Fatalf("schema_version = %q, want %q", file.SchemaVersion, mediaRankerFixtureSchema)
	}
	if len(file.Cases) != len(cases) {
		t.Fatalf("fixture cases = %d, generated cases = %d", len(file.Cases), len(cases))
	}
	for index, want := range cases {
		got := file.Cases[index]
		if got.Name != want.Name {
			t.Fatalf("case %d name = %q, want %q", index, got.Name, want.Name)
		}
		if !reflectJSONEqual(t, got.Want, want.Want) {
			t.Fatalf("case %q drifted from the checked-in Rust parity fixture", got.Name)
		}
	}
}

// reflectJSONEqual compares two expectations through their JSON encoding so
// the float formatting of the fixture cannot mask a numeric difference.
func reflectJSONEqual(t *testing.T, got, want mediaRankerFixtureExpected) bool {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if string(gotJSON) == string(wantJSON) {
		return true
	}
	t.Logf("fixture drift\n got: %s\nwant: %s", gotJSON, wantJSON)
	return false
}

func mediaSlotKindFromString(value string) (media.SlotKind, bool) {
	switch media.SlotKind(value) {
	case media.SlotPrimaryVideo, media.SlotSecondaryImage, media.SlotEvidenceOverlay,
		media.SlotMap, media.SlotPortrait, media.SlotDocument, media.SlotBackground:
		return media.SlotKind(value), true
	default:
		return "", false
	}
}
