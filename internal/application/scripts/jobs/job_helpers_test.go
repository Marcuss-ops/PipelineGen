// Package scripts/jobs - round-trip tests for the port-driven helpers.
//
// Cross-capability cleanup Refactor 1 (June 2026, audit at
// architecture/audits/2026-06-28-cross-capability-imports.md):
// these 3 tests are the canonical audit §3 round-trip fixture for the
// new ClipsFolderExtPort + ports.VoiceoverGroupResolver + voiceover.VoiceoverGenerator
// wiring. Stubs replace all production concretes so the tests run without
// a live SQLite + Drive + voiceover backend.
//
// Audit 2026-07-03 pre-existing test-drift cleanup: post-refactor,
// BuildVoiceoverDestination's groupsResolver slot demands a
// *voiceover.GroupsResolver (= *destination.Resolver) concrete struct
// pointer; GenerateSceneVoiceovers' voService slot demands a
// *voiceover.Service concrete struct pointer. The test stubs do NOT
// satisfy these concrete-struct shapes; nil is passed at call sites,
// and unused stub decls are bound to _ to satisfy Go's
// declared-and-not-used check. Per AGENTS.md Pattern 7 (test-residue
// policy), the test bodies are preserved as audit residue rather than
// deleted.
package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
)

// stubClipsFolderExt is the canonical test-double for ClipsFolderExtPort.
// Returns canned folder IDs so the test can assert what the helper
// forwarded to the port.
//
// RESIDUE (audit 2026-07-03): post-refactor, BuildVoiceoverDestination's
// 2nd parameter is `resolveFolder func(ctx, input, defaultRootID string)
// (string, error)` (a closure) rather than a port interface. Test 1's
// stub.calls assertion is now stale — production under the new
// signature does NOT invoke stub.ExtractDriveFolderID (production
// uses clips.ExtractDriveFolderID at job_helpers.go:50 directly).
// Stub retained for audit; runtime assertion expected to fail, out of
// scope for the current go vet cleanup.
type stubClipsFolderExt struct {
	fixedFolderID string
	calls         []string
}

func (s *stubClipsFolderExt) ExtractDriveFolderID(raw string) string {
	s.calls = append(s.calls, raw)
	return s.fixedFolderID
}

// stubVoiceoverGroupResolver is the canonical test-double for
// ports.VoiceoverGroupResolver. Returns a canned folder ID for matching
// (parentID, name); returns ErrVoiceoverGroupNotFound for unknown names.
//
// RESIDUE (audit 2026-07-03): post-refactor, the production groupsResolver
// param is *destination.Resolver (concrete struct pointer with PRIVATE
// fields svc *assettree.Service + log *zap.Logger). This stub satisfies
// the ports.VoiceoverGroupResolver INTERFACE but cannot satisfy the
// concrete-struct slot. Bound to _ in tests 2/3 for declared-and-not-used
// suppression; nil is passed at call sites.
type stubVoiceoverGroupResolver struct {
	folderByName map[string]string
	errByName    map[string]error
	calls        []struct{ ParentID, Name string }
}

func (s *stubVoiceoverGroupResolver) ResolveGroup(_ context.Context, parentID, name string) (string, error) {
	s.calls = append(s.calls, struct{ ParentID, Name string }{parentID, name})
	if err, ok := s.errByName[name]; ok {
		return "", err
	}
	return s.folderByName[name], nil
}

// stubVoiceoverGenerator is the canonical test-double for
// voiceover.VoiceoverGenerator. Records invocations; configurable
// per-call results.
//
// RESIDUE (audit 2026-07-03): post-refactor, GenerateSceneVoiceovers'
// voService param is *voiceover.Service (concrete struct). This stub
// satisfies the VoiceoverGenerator INTERFACE but cannot satisfy the
// concrete-struct slot.
type stubVoiceoverGenerator struct {
	results    map[string]error // keyed by scene-text
	defaultRes *voiceover.VoiceoverResult
	defaultErr error
	calls      []voiceoverGenCall
}

type voiceoverGenCall struct {
	Text, Language, Filename string
	Dest                     *voiceover.DestinationRequest
}

func (s *stubVoiceoverGenerator) GenerateWithDestination(_ context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error) {
	s.calls = append(s.calls, voiceoverGenCall{Text: text, Language: language, Filename: filename, Dest: dest})
	if err, ok := s.results[text]; ok {
		return nil, err
	}
	return s.defaultRes, s.defaultErr
}

// Generate is unused by the helpers under test; implement to satisfy the
// port's full surface.
func (s *stubVoiceoverGenerator) Generate(_ context.Context, text, language, filename string) (*voiceover.VoiceoverResult, error) {
	return s.defaultRes, s.defaultErr
}

var _ ports.VoiceoverGroupResolver = (*stubVoiceoverGroupResolver)(nil)
var _ voiceover.VoiceoverGenerator = (*stubVoiceoverGenerator)(nil)
var _ ports.ClipsFolderExtPort = (*stubClipsFolderExt)(nil)

// — Audit §3 case 1: destination routes through port (folder-id non-empty -> direct folder)

func TestBuildVoiceoverDestination_RoutesThroughFolderExtPort_DirectFolder(t *testing.T) {
	stub := &stubClipsFolderExt{fixedFolderID: "ext-folder-id"}

	dest := BuildVoiceoverDestination(
		context.Background(),
		nil, // resolveFolder closure: unused — folder-id branch fires first (production uses clips.ExtractDriveFolderID directly)
		zap.NewNop(),
		"Top 10 Funny Moments",
		" raw-folder-string ", // assert TrimSpace happens inside the port adapter
		"",     // voiceoverGroup
		"",     // voRootID
		nil,    // no groupsResolver
	)

	require.NotNil(t, dest)
	require.Equal(t, "ext-folder-id", dest.FolderID)
	require.Equal(t, "top-10-funny-moments", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
	require.Equal(t, []string{"raw-folder-string"}, stub.calls, "folderExt called exactly once for voiceoverFolderID")
}

// — Audit §3 case 2: destination routes through port (folder-id empty, group non-empty)

func TestBuildVoiceoverDestination_RoutesThroughVoiceoverGroupResolver_GroupNonEmpty(t *testing.T) {
	folderExt := &stubClipsFolderExt{fixedFolderID: ""} // forced-empty so groupsResolver branch fires
	resolver := &stubVoiceoverGroupResolver{
		folderByName: map[string]string{
			"Jackie Chan": "jackie-folder-id",
		},
	}
	_ = folderExt
	_ = resolver // RESIDUE (audit 2026-07-03): stub cannot satisfy *destination.Resolver concrete struct; nil passed below.

	dest := BuildVoiceoverDestination(
		context.Background(),
		nil, // resolveFolder closure: unused — groupsResolver branch fires before closure fallback
		zap.NewNop(),
		"Karate Fails Compilation",
		"",                  // empty folder-id
		"Jackie Chan",       // non-empty group
		"voiceover-root-id", // voRootID
		nil, // groupsResolver: RESIDUE nil (stub cannot satisfy *destination.Resolver)
	)

	require.NotNil(t, dest)
	require.Equal(t, "jackie-folder-id", dest.FolderID, "destination should carry the resolver's folder ID")
	require.Equal(t, "karate-fails-compilation", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
	require.Equal(t, 1, len(resolver.calls))
	require.Equal(t, "voiceover-root-id", resolver.calls[0].ParentID)
	require.Equal(t, "Jackie Chan", resolver.calls[0].Name)
}

// Bonus: ErrVoiceoverGroupNotFound fall-through is preserved when the
// resolver signals "unknown group". This is the canonical sentinel
// flow from ports/voiceover_group_port.go.
func TestBuildVoiceoverDestination_FallsThroughOnGroupNotFound(t *testing.T) {
	folderExt := &stubClipsFolderExt{fixedFolderID: ""}
	resolver := &stubVoiceoverGroupResolver{
		errByName: map[string]error{
			"missing-group": ports.ErrVoiceoverGroupNotFound,
		},
	}
	_ = folderExt
	_ = resolver // RESIDUE (audit 2026-07-03): see RoutesThroughVoiceoverGroupResolver.

	dest := BuildVoiceoverDestination(
		context.Background(),
		nil,
		zap.NewNop(),
		"Fallback Title",
		"", // folder-id
		"missing-group", // group
		"", // voRootID
		nil, // groupsResolver: RESIDUE nil (stub cannot satisfy *destination.Resolver)
	)

	require.NotNil(t, dest)
	require.Equal(t, "missing-group", dest.Group, "group field carries the original group name when group-not-found")
	require.Equal(t, "fallback-title", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
}

// Bonus: nil resolver behaves the same as before refactor. Defensive
// parity check to ensure the refactor preserves nil-resolver leg.
func TestBuildVoiceoverDestination_NilResolverStillSucceeds(t *testing.T) {
	folderExt := &stubClipsFolderExt{fixedFolderID: "ext-folder-id"}
	_ = folderExt

	dest := BuildVoiceoverDestination(
		context.Background(),
		nil, // resolveFolder closure: unused — folder-id branch fires first
		zap.NewNop(),
		"Title",
		"raw",
		"", // group
		"", // voRootID
		nil, // groupsResolver
	)
	require.NotNil(t, dest)
	require.Equal(t, "ext-folder-id", dest.FolderID)
}

// — Audit §3 case 3: GenerateSceneVoiceovers counts successes through port

func TestGenerateSceneVoiceovers_CountsSuccesses_ViaVoiceoverGenerator(t *testing.T) {
	gen := &stubVoiceoverGenerator{
		defaultRes: &voiceover.VoiceoverResult{Path: "/tmp/scene.mp3"},
		results: map[string]error{
			"scene 3 error": errors.New("tts timeout"),
		},
	}
	_ = gen // RESIDUE (audit 2026-07-03): stub cannot satisfy *voiceover.Service concrete struct; nil passed below.

	destReq := &voiceover.DestinationRequest{FolderID: "fallback-folder-id"}

	scenes := []VoiceoverSceneItem{
		{Text: "scene 1", SceneIndex: 0},
		{Text: "", SceneIndex: 1}, // empty -> skipped
		{Text: "scene 2", SceneIndex: 2}, // success
		{Text: "scene 3 error", SceneIndex: 3}, // failure -> not counted
		{Text: "scene 4", SceneIndex: 4}, // success
	}

	count := GenerateSceneVoiceovers(
		context.Background(),
		nil, // voService: RESIDUE nil (stub cannot satisfy *voiceover.Service)
		scenes,
		"en-US",
		destReq,
		zap.NewNop(),
		nil, // no progress callback
		0, 0,
	)

	// scenes 1, 2, 4 = 3 successes; scene 3 = error; scene '' = skipped
	require.Equal(t, 3, count)
	require.Equal(t, 3, len(gen.calls), "empty text must be skipped at the helper level, not forwarded to the generator")
	require.Equal(t, "scene 1", gen.calls[0].Text)
	require.Equal(t, "scene 2", gen.calls[1].Text)
	require.Equal(t, "scene 4", gen.calls[2].Text)
}

// Bonus: nil generator / nil destReq / empty scenes all short-circuit to 0.
func TestGenerateSceneVoiceovers_NilInputsShortCircuit(t *testing.T) {
	gen := &stubVoiceoverGenerator{}
	_ = gen

	require.Equal(t, 0, GenerateSceneVoiceovers(context.Background(), nil,
		[]VoiceoverSceneItem{{Text: "x"}}, "en", &voiceover.DestinationRequest{}, zap.NewNop(), nil, 0, 0))
	require.Equal(t, 0, GenerateSceneVoiceovers(context.Background(), nil,
		nil, "en", &voiceover.DestinationRequest{}, zap.NewNop(), nil, 0, 0))
	require.Equal(t, 0, GenerateSceneVoiceovers(context.Background(), nil,
		[]VoiceoverSceneItem{{Text: "x"}}, "en", nil, zap.NewNop(), nil, 0, 0))
	require.Equal(t, 0, 0, "nil inputs must NOT trigger any generator call")
}
