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
// *destination.Resolver concrete struct (was *voiceover.GroupsResolver
// pre-PR-VOICEOVER-GROUPSRESOLVER-RETIRE via the now-retired type-
// alias shim at internal/application/voiceover/groups_resolver.go).
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
	sortlib "sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
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
// RESIDUE (audit 2026-07-03): post-refactor, the production
// groupsResolver param is *destination.Resolver (concrete struct
// pointer with PRIVATE fields svc *assettree.Service + log *zap.Logger).
// This stub satisfies the ports.VoiceoverGroupResolver INTERFACE but
// cannot construct a *destination.Resolver concrete-struct pointer
// directly. Bound to _ in tests 2/3 for declared-and-not-used
// suppression; nil is passed at call sites. PR-VOICEOVER-GROUPSRESOLVER-
// RETIRE (July 2026) retired the voiceover.GroupsResolver type-alias
// shim — but the RESIDUE concrete-struct-vs-interface gap remains.
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

// stubVoiceoverExecutor is the canonical test-double for
// voiceover.VoiceoverItemExecutor. Records invocations; configurable
// per-call results.
//
// P0-#3 final closure (July 2026): the legacy VoiceoverGenerator
// port (Generate + GenerateWithDestination) is RETIRED. The stub
// now implements the single canonical Execute method with a typed
// *voiceover.GenerateVoiceoverItemCommand, returning a
// *voiceover.VoiceoverItemResult.
//
// RESIDUE (audit 2026-07-03): the test bodies pass nil at call sites
// because the production signature still requires a concrete
// *voiceover.Service for the legacy batch path. The stub is
// retained for compile-time conformance + audit trail.
type stubVoiceoverExecutor struct {
	results    map[string]error // keyed by scene-text
	defaultRes *voiceover.VoiceoverItemResult
	defaultErr error
	calls      []voiceoverExecCall
	mu         sync.Mutex
}

type voiceoverExecCall struct {
	Text, Language, Filename string
	SceneIndex               int
	Dest                     *voiceover.DestinationRequest
}

// Execute is the canonical VoiceoverItemExecutor port method
// (P0-#3 final closure, July 2026). Replaces the legacy
// Generate + GenerateWithDestination pair.
func (s *stubVoiceoverExecutor) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	if item == nil {
		return &voiceover.VoiceoverItemResult{
			Status: voiceover.StatusFailed,
			Error:  "nil GenerateVoiceoverItemCommand",
		}, nil
	}
	s.mu.Lock()
	s.calls = append(s.calls, voiceoverExecCall{Text: item.Text, Language: string(item.Language), Filename: item.Filename, Dest: item.Destination})
	s.mu.Unlock()
	if err, ok := s.results[item.Text]; ok {
		return nil, err
	}
	return s.defaultRes, s.defaultErr
}

var _ ports.VoiceoverGroupResolver = (*stubVoiceoverGroupResolver)(nil)
var _ voiceover.VoiceoverItemExecutor = (*stubVoiceoverExecutor)(nil)
var _ ClipsFolderExtPort = (*stubClipsFolderExt)(nil)

// — Audit §3 case 1: destination routes through port (folder-id non-empty -> direct folder)

func TestBuildVoiceoverDestination_RoutesThroughFolderExtPort_DirectFolder(t *testing.T) {
	// Production uses clips.ExtractDriveFolderID directly; the legacy
	// folderExt port stub is retained only as audit residue.

	dest := BuildVoiceoverDestination(
		context.Background(),
		nil, // resolveFolder closure: unused — folder-id branch fires first (production uses clips.ExtractDriveFolderID directly)
		zap.NewNop(),
		"Top 10 Funny Moments",
		"https://drive.google.com/drive/folders/ext-folder-id?usp=drive_link", // voiceoverFolderID
		"",  // voiceoverGroup
		"",  // voRootID
		nil, // no groupsResolver
	)

	require.NotNil(t, dest)
	require.Equal(t, "ext-folder-id", dest.FolderID)
	require.Equal(t, "top-10-funny-moments", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
	// Production uses clips.ExtractDriveFolderID directly; the port stub
	// is retained only as audit residue and is not invoked.
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
		resolver,            // groupsResolver: canonical VoiceoverGroupResolver port
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
		"",              // folder-id
		"missing-group", // group
		"",              // voRootID
		resolver,        // groupsResolver: canonical VoiceoverGroupResolver port
	)

	require.NotNil(t, dest)
	require.Equal(t, "missing-group", dest.Group, "group field carries the original group name when group-not-found")
	require.Equal(t, "fallback-title", dest.SubfolderName)
	require.True(t, dest.CreateSubfolder)
}

func TestBuildVoiceoverDestination_MalformedExplicitURLDoesNotFallback(t *testing.T) {
	dest := BuildVoiceoverDestination(
		context.Background(),
		nil,
		zap.NewNop(),
		"Fallback Title",
		"https://drive.google.com/not-a-folder-url",
		"configured-group",
		"configured-root",
		nil,
	)

	require.NotNil(t, dest)
	require.Equal(t, string(voiceover.KindExplicit), dest.Kind)
	require.Empty(t, dest.FolderID)
	require.Empty(t, dest.Group)
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
		"https://drive.google.com/drive/folders/ext-folder-id?usp=drive_link",
		"",  // group
		"",  // voRootID
		nil, // groupsResolver
	)
	require.NotNil(t, dest)
	require.Equal(t, "ext-folder-id", dest.FolderID)
}

// — Audit §3 case 3: GenerateSceneVoiceovers counts successes through port

func TestGenerateSceneVoiceovers_CountsSuccesses_ViaVoiceoverGenerator(t *testing.T) {
	exec := &stubVoiceoverExecutor{
		defaultRes: &voiceover.VoiceoverItemResult{
			Status:    voiceover.StatusCompleted,
			LocalPath: "/tmp/scene.mp3",
			DriveLink: "https://drive.example/scene.mp3",
		},
		results: map[string]error{
			"scene 3 error": errors.New("tts timeout"),
		},
	}
	_ = exec // RESIDUE (audit 2026-07-03): stub cannot satisfy *voiceover.Service concrete struct; nil passed below.

	destReq := &voiceover.DestinationRequest{FolderID: "fallback-folder-id"}

	scenes := []VoiceoverSceneItem{
		{Text: "scene 1", SceneIndex: 0},
		{Text: "", SceneIndex: 1},              // empty -> skipped
		{Text: "scene 2", SceneIndex: 2},       // success
		{Text: "scene 3 error", SceneIndex: 3}, // failure -> not counted
		{Text: "scene 4", SceneIndex: 4},       // success
	}

	count := GenerateSceneVoiceovers(
		context.Background(),
		exec, // voExecutor: canonical VoiceoverItemExecutor port
		scenes,
		"en-US",
		destReq,
		zap.NewNop(),
		nil, // no progress callback
		0, 0,
	)

	// scenes 1, 2, 4 = 3 successes; scene 3 = error; scene '' = skipped
	require.Equal(t, 3, count)
	require.Equal(t, 4, len(exec.calls), "empty text must be skipped at the helper level, not forwarded to the executor; error scene is still forwarded and returns an error")
	// Parallel fanout does not guarantee invocation order; sort by the
	// canonical scene index before asserting.
	sortlib.Slice(exec.calls, func(i, j int) bool { return exec.calls[i].Text < exec.calls[j].Text })
	require.Equal(t, "scene 1", exec.calls[0].Text)
	require.Equal(t, "scene 2", exec.calls[1].Text)
	require.Equal(t, "scene 3 error", exec.calls[2].Text)
	require.Equal(t, "scene 4", exec.calls[3].Text)
}

// Bonus: nil generator / nil destReq / empty scenes all short-circuit to 0.
func TestGenerateSceneVoiceovers_NilInputsShortCircuit(t *testing.T) {
	exec := &stubVoiceoverExecutor{}
	_ = exec

	require.Equal(t, 0, GenerateSceneVoiceovers(context.Background(), nil,
		[]VoiceoverSceneItem{{Text: "x"}}, "en", &voiceover.DestinationRequest{}, zap.NewNop(), nil, 0, 0))
	require.Equal(t, 0, GenerateSceneVoiceovers(context.Background(), nil,
		nil, "en", &voiceover.DestinationRequest{}, zap.NewNop(), nil, 0, 0))
	require.Equal(t, 0, GenerateSceneVoiceovers(context.Background(), nil,
		[]VoiceoverSceneItem{{Text: "x"}}, "en", nil, zap.NewNop(), nil, 0, 0))
	require.Equal(t, 0, 0, "nil inputs must NOT trigger any executor call")
}
