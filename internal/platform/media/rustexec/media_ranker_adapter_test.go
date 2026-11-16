package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const mediaRankerFixtureResponse = `{
  "ranked": [
    {
      "asset_id": "asset-strong",
      "candidate_id": "candidate-asset-strong",
      "source_url": "https://cdn.example/asset-strong.mp4",
      "provider": "artlist",
      "media_type": "video",
      "duration_ms": 4000,
      "rights_status": "verified",
      "final_score": 0.625,
      "verdict": "accept",
      "reason": "score above accept threshold",
      "breakdown": {
        "semantic": 1,
        "exact_match": 1,
        "visual": 0.5,
        "manual_approval": 1,
        "quality": 1,
        "historical_success": 1,
        "duration_fit": 1,
        "repetition_penalty": 0.3,
        "rights_penalty": 0
      }
    },
    {
      "asset_id": "asset-tied",
      "candidate_id": "candidate-asset-tied",
      "source_url": "https://cdn.example/asset-tied.mp4",
      "provider": "artlist",
      "media_type": "video",
      "duration_ms": 4000,
      "rights_status": "verified",
      "final_score": 0.625,
      "verdict": "accept",
      "reason": "score above accept threshold",
      "breakdown": {
        "semantic": 1,
        "exact_match": 1,
        "visual": 0.5,
        "manual_approval": 0,
        "quality": 0,
        "historical_success": 0,
        "duration_fit": 1,
        "repetition_penalty": 0,
        "rights_penalty": 0
      }
    }
  ],
  "drops": [{"asset_id": "asset-cold", "candidate_id": "candidate-asset-cold", "gate": "available_media", "reason": "failed the available_media gate"}],
  "winner": {"asset_id": "asset-tied", "candidate_id": "candidate-asset-tied", "final_score": 0.625, "verdict": "accept", "reason": "score above accept threshold", "breakdown": {"semantic": 1, "exact_match": 1, "visual": 0.5, "manual_approval": 0, "quality": 0, "historical_success": 0, "duration_fit": 1, "repetition_penalty": 0, "rights_penalty": 0}},
  "error": null
}`

func mediaRankerTestRequest() MediaRankerRequest {
	return MediaRankerRequest{
		SceneDurationMS: 4000,
		PrevVideoID:     "asset-strong",
		SlotKind:        "primary_video",
		Candidates: []MediaRankerCandidate{
			{CandidateID: "candidate-asset-strong", AssetID: "asset-strong", MediaType: "video", DurationMS: 4000, RightsStatus: "verified", DiscoveryStatus: "searched", MaterializationStatus: "warm"},
			{CandidateID: "candidate-asset-tied", AssetID: "asset-tied", MediaType: "video", DurationMS: 4000, RightsStatus: "verified", DiscoveryStatus: "searched", MaterializationStatus: "warm"},
			{CandidateID: "candidate-asset-cold", AssetID: "asset-cold", MediaType: "video", DurationMS: 4000, RightsStatus: "verified", DiscoveryStatus: "searched", MaterializationStatus: "cold"},
		},
	}
}

func TestMediaRankerAdapterSendsOneRankCandidatesRequest(t *testing.T) {
	runner := &semanticRunner{output: []byte(mediaRankerFixtureResponse)}
	adapter, err := NewMediaRankerAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := adapter.RankCandidates(context.Background(), mediaRankerTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("calls=%d, want exactly one worker call", runner.calls)
	}
	if len(outcome.Ranked) != 2 || outcome.Ranked[0].AssetID != "asset-strong" {
		t.Fatalf("ranked=%+v", outcome.Ranked)
	}
	if outcome.Winner == nil || outcome.Winner.AssetID != "asset-tied" {
		t.Fatalf("winner=%+v", outcome.Winner)
	}
	if len(outcome.Drops) != 1 || outcome.Drops[0].Gate != "available_media" {
		t.Fatalf("drops=%+v", outcome.Drops)
	}

	var request mediaRankerWireRequest
	if err := json.Unmarshal(runner.input, &request); err != nil {
		t.Fatal(err)
	}
	if request.Version != "mediasampler.v1" || request.Operation != "rank_candidates" {
		t.Fatalf("envelope=%+v", request)
	}
	if request.SlotKind != "primary_video" || request.PrevVideoID != "asset-strong" {
		t.Fatalf("request=%+v", request)
	}
	if len(request.Candidates) != 3 {
		t.Fatalf("candidates=%d", len(request.Candidates))
	}
	payload := string(runner.input)
	if !strings.Contains(payload, `"candidate_score"`) && !strings.Contains(payload, `"asset_id"`) {
		t.Fatalf("request does not carry candidates: %s", payload)
	}
}

func TestMediaRankerAdapterFailsClosedOnUnknownSlot(t *testing.T) {
	runner := &semanticRunner{output: []byte(mediaRankerFixtureResponse)}
	adapter, _ := NewMediaRankerAdapter(executorForSemantic(runner))
	request := mediaRankerTestRequest()
	request.SlotKind = "b_roll"
	if _, err := adapter.RankCandidates(context.Background(), request); err == nil {
		t.Fatal("unknown slot must fail closed")
	}
	if runner.calls != 0 {
		t.Fatal("no worker call may happen for an invalid slot")
	}
}

func TestMediaRankerAdapterSurfacesTheWorkerError(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ranked":[],"drops":[],"winner":null,"error":"unknown slot_kind"}`)}
	adapter, _ := NewMediaRankerAdapter(executorForSemantic(runner))
	if _, err := adapter.RankCandidates(context.Background(), mediaRankerTestRequest()); err == nil {
		t.Fatal("worker error must surface")
	}
}

func TestMediaRankerAdapterRejectsABrokenOrder(t *testing.T) {
	broken := strings.Replace(
		mediaRankerFixtureResponse,
		`"final_score": 0.625,
      "verdict": "accept",
      "reason": "score above accept threshold",
      "breakdown": {
        "semantic": 1,
        "exact_match": 1,
        "visual": 0.5,
        "manual_approval": 0,`,
		`"final_score": 0.9,
      "verdict": "accept",
      "reason": "score above accept threshold",
      "breakdown": {
        "semantic": 1,
        "exact_match": 1,
        "visual": 0.5,
        "manual_approval": 0,`,
		1,
	)
	runner := &semanticRunner{output: []byte(broken)}
	adapter, _ := NewMediaRankerAdapter(executorForSemantic(runner))
	if _, err := adapter.RankCandidates(context.Background(), mediaRankerTestRequest()); err == nil {
		t.Fatal("a non-monotonic selection must fail closed")
	}
}

func TestMediaRankerAdapterRejectsAWinnerOutsideTheSelection(t *testing.T) {
	response := strings.Replace(mediaRankerFixtureResponse, `"winner": {"asset_id": "asset-tied"`, `"winner": {"asset_id": "asset-invented"`, 1)
	response = strings.Replace(response, `"candidate_id": "candidate-asset-tied", "final_score"`, `"candidate_id": "candidate-asset-invented", "final_score"`, 1)
	runner := &semanticRunner{output: []byte(response)}
	adapter, _ := NewMediaRankerAdapter(executorForSemantic(runner))
	if _, err := adapter.RankCandidates(context.Background(), mediaRankerTestRequest()); err == nil {
		t.Fatal("a winner outside the ranked selection must fail closed")
	}
}

func TestMediaRankerAdapterRejectsAnUnaccountedCandidate(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ranked":[],"drops":[],"winner":null}`)}
	adapter, _ := NewMediaRankerAdapter(executorForSemantic(runner))
	if _, err := adapter.RankCandidates(context.Background(), mediaRankerTestRequest()); err == nil {
		t.Fatal("a candidate that is neither ranked nor dropped must fail closed")
	}
}

func TestMediaRankerAdapterReturnsAnEmptyPoolWithoutCallingTheWorker(t *testing.T) {
	runner := &semanticRunner{}
	adapter, _ := NewMediaRankerAdapter(executorForSemantic(runner))
	request := mediaRankerTestRequest()
	request.Candidates = nil
	outcome, err := adapter.RankCandidates(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Ranked) != 0 || len(outcome.Drops) != 0 || outcome.Winner != nil {
		t.Fatalf("outcome=%+v", outcome)
	}
	if runner.calls != 0 {
		t.Fatal("an empty pool must not reach the worker")
	}
}
