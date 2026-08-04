// Package voiceover — PR-VO-B2 (June 2026) round-trip tests.
//
// Scope (PR-VO-B2 metadata + StyleGroup propagation):
//  1. mergeUserMetadata preserves user-supplied keys when they do
//     not collide with the core key set.
//  2. mergeUserMetadata DROPS user-supplied keys on collision with
//     a core key (with a warn log line; we verify the warn fires).
//  3. mergeUserMetadata injects StyleGroup from ResolvedDestination
//     into the meta map only when non-empty.
//  4. asset.ResolveRequest carries StyleGroup verbatim from the
//     voiceover caller through the resolver (sanity pin).
//  5. BatchRequest round-trips StyleGroup + Metadata verbatim
//     through JSON marshal + unmarshal (worker payload path).
//  6. resolveDestination forwards StyleGroup into asset.ResolveRequest
//     AND mirrors it back into ResolvedDestination (function-body
//     drift pin).
//  7. resolveDestination empty-StyleGroup case still forwards +
//     mirrors zero-value.
package voiceover

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestMergeUserMetadata_NoCollisionPreservesAllKeys is the baseline:
// every user-supplied metadata key lands in the meta map unchanged
// when no core key is present.
func TestMergeUserMetadata_NoCollisionPreservesAllKeys(t *testing.T) {
	meta := map[string]any{
		"language": "it",
		"voice":    "en-US-RogerNeural",
	}
	userMetadata := map[string]any{
		"campaign":       "q3_launch",
		"producer_email": "ops@example.com",
		"shot_count":     12,
	}
	core, logs := observer.New(zap.WarnLevel)
	dest := &ResolvedDestination{StyleGroup: ""}

	mergeUserMetadata(meta, dest, userMetadata, zap.New(core))

	for want := range userMetadata {
		if _, ok := meta[want]; !ok {
			t.Errorf("user key %q dropped (no collision expected); got meta=%v", want, meta)
		}
	}
	if logs.Len() != 0 {
		t.Errorf("no collisions expected; got %d warn lines: %v", logs.Len(), logs.All())
	}
	if meta["language"] != "it" {
		t.Errorf("core key language overwritten; got %v", meta["language"])
	}
	if meta["voice"] != "en-US-RogerNeural" {
		t.Errorf("core key voice overwritten; got %v", meta["voice"])
	}
}

// TestMergeUserMetadata_CollisionDropsUserKey pins the safety rule:
// a caller-supplied key colliding with a core key is dropped (NOT
// silently merged), with a warn log line. The test exercises the
// exact attack vector mentioned in the design doc:
//
//	{"language": "hacked", "voice": "hacked"}.
func TestMergeUserMetadata_CollisionDropsUserKey(t *testing.T) {
	meta := map[string]any{
		"language": "it",
		"voice":    "en-US-RogerNeural",
	}
	userMetadata := map[string]any{
		"language": "hacked",
		"voice":    "hacked",
		"campaign": "q3_launch", // non-colliding key → must pass
	}
	core, logs := observer.New(zap.WarnLevel)
	dest := &ResolvedDestination{}

	mergeUserMetadata(meta, dest, userMetadata, zap.New(core))

	if meta["language"] != "it" {
		t.Errorf("language collision: core value should win, got %v", meta["language"])
	}
	if meta["voice"] != "en-US-RogerNeural" {
		t.Errorf("voice collision: core value should win, got %v", meta["voice"])
	}
	if meta["campaign"] != "q3_launch" {
		t.Errorf("non-colliding campaign should pass through; got %v", meta["campaign"])
	}
	if logs.Len() != 2 {
		t.Errorf("expected 2 warn lines (language + voice collisions); got %d", logs.Len())
	}
	for _, e := range logs.All() {
		if !strings.Contains(e.Message, "user metadata key colliding") {
			t.Errorf("warn message lacks 'user metadata key colliding'; got %q", e.Message)
		}
	}
}

// TestMergeUserMetadata_StyleGroupInjected pins StyleGroup injection:
// when dest.StyleGroup is non-empty, meta["style_group"] is set to
// that value verbatim.
func TestMergeUserMetadata_StyleGroupInjected(t *testing.T) {
	meta := map[string]any{"language": "en"}
	dest := &ResolvedDestination{StyleGroup: "cinematic"}

	mergeUserMetadata(meta, dest, nil, zap.NewNop())

	if got, ok := meta["style_group"]; !ok {
		t.Errorf("style_group key missing; got meta=%v", meta)
	} else if got != "cinematic" {
		t.Errorf("style_group = %v; want %q", got, "cinematic")
	}
}

// TestMergeUserMetadata_StyleGroupEmptyOmits verifies the omitempty
// contract: empty StyleGroup does NOT produce a meta["style_group"]
// entry (which would silently shadow real defaults).
func TestMergeUserMetadata_StyleGroupEmptyOmits(t *testing.T) {
	meta := map[string]any{"language": "en"}
	dest := &ResolvedDestination{StyleGroup: ""}

	mergeUserMetadata(meta, dest, nil, zap.NewNop())

	if _, ok := meta["style_group"]; ok {
		t.Errorf("style_group key should be absent when StyleGroup==\"\"; got meta=%v", meta)
	}
}

// TestResolveRequestForVoiceover_CarriesStyleGroup: sanity pin on
// the field's presence on asset.ResolveRequest so a struct-level
// refactor that drops it fails this test.
func TestResolveRequestForVoiceover_CarriesStyleGroup(t *testing.T) {
	dest := &DestinationRequest{StyleGroup: "anime"}
	req := &asset.ResolveRequest{
		Source:     "voiceover",
		StyleGroup: string(dest.StyleGroup),
	}
	if req.StyleGroup != "anime" {
		t.Fatalf("ResolveRequest.StyleGroup should carry DestinationRequest.StyleGroup; got %q", req.StyleGroup)
	}
}

// TestBatchRequest_RoundTripsStyleGroupAndMetadata: JSON round-trip
// pin for the worker payload path. Verifies that the wire shape
// preserves both fields verbatim through marshal + unmarshal.
func TestBatchRequest_RoundTripsStyleGroupAndMetadata(t *testing.T) {
	req := &BatchRequest{
		Text:             "Hello world",
		Languages:        []Language{"it", "en"},
		FilenameTemplate: "{slug}_{lang}.mp3",
		VoiceOverrides:   map[string]string{"it": "it-IT-Voice"},
		Strategy:         "replace",
		Destination: &DestinationRequest{
			Group:         "boxe",
			StyleGroup:    "cinematic",
			SubfolderName: "intro",
		},
		Metadata: map[string]any{
			"campaign": "q3_launch",
			"shot_id":  "shot_42",
		},
	}

	payload := req.PayloadMap()
	destPayload, ok := payload["destination"].(map[string]any)
	if !ok {
		t.Fatalf("payload destination not a map; got %T", payload["destination"])
	}
	if destPayload["style_group"] != "cinematic" {
		t.Errorf("payload destination.style_group = %v; want %q", destPayload["style_group"], "cinematic")
	}
	if _, present := destPayload["kind"]; present {
		t.Errorf("payload destination.kind should be omitted for legacy request, got %v", destPayload["kind"])
	}
	voiceOverrides, ok := payload["voice_overrides"].(map[string]string)
	if !ok {
		if raw, exists := payload["voice_overrides"]; exists {
			t.Fatalf("payload voice_overrides has unexpected type %T", raw)
		}
		t.Fatalf("payload voice_overrides missing")
	}
	if voiceOverrides["it"] != "it-IT-Voice" {
		t.Errorf("payload voice_overrides.it = %v; want %q", voiceOverrides["it"], "it-IT-Voice")
	}
	metaPayload, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("payload metadata not a map; got %T", payload["metadata"])
	}
	if metaPayload["campaign"] != "q3_launch" {
		t.Errorf("payload metadata.campaign = %v; want %q", metaPayload["campaign"], "q3_launch")
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal BatchRequest: %v", err)
	}
	var rebuilt BatchRequest
	if err := json.Unmarshal(raw, &rebuilt); err != nil {
		t.Fatalf("unmarshal BatchRequest: %v", err)
	}
	if rebuilt.Destination == nil {
		t.Fatalf("rebuilt.Destination = nil; want non-nil with StyleGroup")
	}
	if rebuilt.Destination.StyleGroup != "cinematic" {
		t.Errorf("rebuilt.Destination.StyleGroup = %q; want %q", rebuilt.Destination.StyleGroup, "cinematic")
	}
	if rebuilt.Metadata["campaign"] != "q3_launch" {
		t.Errorf("rebuilt.Metadata.campaign = %v; want %q", rebuilt.Metadata["campaign"], "q3_launch")
	}
	if rebuilt.Metadata["shot_id"] != "shot_42" {
		t.Errorf("rebuilt.Metadata.shot_id = %v; want %q", rebuilt.Metadata["shot_id"], "shot_42")
	}
}

// TestBatchRequest_UnicodeAndSpecialCharsRoundTrips: Unicode + JSON
// special-char round-trip. StyleGroup carries user style tags
// (often non-ASCII), and metadata can include nested JSON. The wire
// shape must survive a marshal/unmarshal cycle via PayloadMap.
func TestBatchRequest_UnicodeAndSpecialCharsRoundTrips(t *testing.T) {
	req := &BatchRequest{
		Text:      "こんにちは",
		Languages: []Language{"ja"},
		Strategy:  "replace",
		Destination: &DestinationRequest{
			Group:      "海外ドキュメンタリー",
			StyleGroup: "日本/\\\"\"",
		},
		Metadata: map[string]any{
			"notes":      "first line\nsecond line",
			"nested_obj": map[string]any{"k": 1, "deep": []any{"a", "b"}},
			"emoji":      "🎯",
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rebuilt BatchRequest
	if err := json.Unmarshal(raw, &rebuilt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rebuilt.Destination.StyleGroup != "日本/\\\"\"" {
		t.Errorf("Japanese + escaped chars dropped; got %q", rebuilt.Destination.StyleGroup)
	}
	if rebuilt.Metadata["emoji"] != "🎯" {
		t.Errorf("emoji dropped; got %v", rebuilt.Metadata["emoji"])
	}
	if rebuilt.Metadata["notes"] != "first line\nsecond line" {
		t.Errorf("multiline dropped; got %q", rebuilt.Metadata["notes"])
	}
	nested, _ := rebuilt.Metadata["nested_obj"].(map[string]any)
	if nested == nil || nested["k"] != float64(1) {
		t.Errorf("nested object lost; got %v", rebuilt.Metadata["nested_obj"])
	}
	deep, _ := nested["deep"].([]any)
	if len(deep) != 2 || deep[0] != "a" || deep[1] != "b" {
		t.Errorf("nested slice lost; got %v", deep)
	}
}

// TestResolveDestination_ForwardsAndMirrorsStyleGroup pins the
// PR-VO-B2 wiring at the function-body level (not just struct-level
// — the field-existence test above doesn't catch drift in the
// forward OR mirror wiring). A stub asset.Resolver records the
// shape of the ResolveRequest it receives, so we can verify:
//
//  1. FORWARD: caller-supplied DestinationRequest.StyleGroup lands
//     in the ResolveRequest.StyleGroup the resolver sees.
//  2. MIRROR: caller-supplied DestinationRequest.StyleGroup also
//     appears on the returned ResolvedDestination.StyleGroup
//     (resolver is not the source of truth — we mirror in
//     resolveDestination directly).
//
// If a future refactor drops either the FORWARD or the MIRROR, this
// test fails loudly.
func TestResolveDestination_ForwardsAndMirrorsStyleGroup(t *testing.T) {
	stub := &recordingResolver{
		result: &asset.ResolveResult{
			FolderID:   "stub-folder-id",
			FolderPath: "/stub/path",
			DriveLink:  "https://drive.google.com/folders/stub",
		},
	}
	svc := &Service{
		log:               zap.NewNop(),
		assetDestResolver: stub,
	}
	dest := &DestinationRequest{
		Group:      "boxe",
		StyleGroup: "anime",
	}
	rd, err := svc.resolveDestination(context.Background(), dest)
	if err != nil {
		t.Fatalf("resolveDestination: %v", err)
	}

	// MIRROR: resolved.StyleGroup == dest.StyleGroup verbatim.
	if rd.StyleGroup != "anime" {
		t.Errorf("ResolvedDestination.StyleGroup = %q; want %q", rd.StyleGroup, "anime")
	}
	if rd.FolderID != "stub-folder-id" {
		t.Errorf("FolderID = %q; want stub-folder-id", rd.FolderID)
	}
	if rd.FolderPath != "/stub/path" {
		t.Errorf("FolderPath = %q; want /stub/path", rd.FolderPath)
	}
	if rd.DriveLink != "https://drive.google.com/folders/stub" {
		t.Errorf("DriveLink = %q", rd.DriveLink)
	}

	// FORWARD: the resolver received the StyleGroup in its
	// ResolveRequest.
	if stub.got == nil {
		t.Fatal("recordingResolver did not receive a ResolveRequest")
	}
	if stub.got.StyleGroup != "anime" {
		t.Errorf("ResolveRequest.StyleGroup = %q; want %q (forward direction)", stub.got.StyleGroup, "anime")
	}
	if stub.got.Source != "voiceover" {
		t.Errorf("ResolveRequest.Source = %q; want voiceover", stub.got.Source)
	}
	if stub.got.Group != "boxe" {
		t.Errorf("ResolveRequest.Group = %q; want boxe", stub.got.Group)
	}
}

// TestResolveDestination_StyleGroupEmpty_NoForwardOrMirror pins the
// omitempty contract: an empty StyleGroup propagates as zero through
// both directions (forward and mirror).
func TestResolveDestination_StyleGroupEmpty_NoForwardOrMirror(t *testing.T) {
	stub := &recordingResolver{
		result: &asset.ResolveResult{FolderID: "stub"},
	}
	svc := &Service{
		log:               zap.NewNop(),
		assetDestResolver: stub,
	}
	dest := &DestinationRequest{Group: "boxe"} // StyleGroup empty
	rd, err := svc.resolveDestination(context.Background(), dest)
	if err != nil {
		t.Fatalf("resolveDestination: %v", err)
	}
	if rd.StyleGroup != "" {
		t.Errorf("ResolvedDestination.StyleGroup = %q; want \"\" (empty case omitempty)", rd.StyleGroup)
	}
	if stub.got == nil || stub.got.StyleGroup != "" {
		t.Errorf("ResolveRequest.StyleGroup = %q; want \"\" (empty case passthrough)", stub.got.StyleGroup)
	}
}

// recordingResolver is a hand-rolled stub for asset.Resolver that
// records the ResolveRequest it received and returns a synthetic
// result.
type recordingResolver struct {
	got    *asset.ResolveRequest
	result *asset.ResolveResult
	err    error
}

func (r *recordingResolver) Resolve(ctx context.Context, req *asset.ResolveRequest) (*asset.ResolveResult, error) {
	r.got = req
	if r.err != nil {
		return nil, r.err
	}
	if r.result != nil {
		return r.result, nil
	}
	return &asset.ResolveResult{}, nil
}
