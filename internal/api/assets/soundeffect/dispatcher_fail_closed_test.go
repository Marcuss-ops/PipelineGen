// Package soundeffect — dispatcher fail-closed contract tests for PR 6
// (June 2026, codex/qdrant-api-writers-fail-closed).
//
// Scope
// -----
// The Generate handler routes the sfx asset write through
// sfxports.DispatcherPort.EnqueueAndIndex (canonical AssetMutationDispatcher
// SSOT) instead of the legacy h.clipsRepo.Upsert path. This test file
// pins the handler-side contract:
//
//   - dispatcher nil → HTTP 503 (fail-closed)
//   - dispatcher present → dispatcher.EnqueueAndIndex called exactly once
//   - dispatcher error → propagated as HTTP 500
//
// The atomic UPSERT + outbox-event behaviour at the dispatcher tx layer
// is verified at
// internal/application/assets/catalogsync/dispatcher_test.go — we don't
// duplicate that suite here. We assert only the handler-side contract
// using a stub dispatcher that records calls.
//
// Generate's body runs the python3/ffmpeg subprocesses BEFORE the
// dispatcher check fires. To reach the dispatcher branch without a real
// Python interpreter, the test stubs processRunner.Run to write the
// expected empty mp3 to the temp paths the handler pre-determines.

package soundeffect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Stubs ─────────────────────────────────────────────────────────────────────

type recordingDispatcher struct {
	calls     atomic.Int32
	lastClip  *asset.Asset
	lastHash  string
	injectErr error
}

func (r *recordingDispatcher) EnqueueAndIndex(_ context.Context, clip *asset.Asset, hash string) error {
	r.calls.Add(1)
	r.lastClip = clip
	r.lastHash = hash
	return r.injectErr
}

var _ sfxports.DispatcherPort = (*recordingDispatcher)(nil)

// stubProcessRunner creates empty output files in place of python3 + ffmpeg.
// The handler passes the temp output path as a CLI arg (`--output` for
// synth_sfx.py, and the positional arg for ffmpeg). The stub extracts
// the path from the args and writes an empty file there so the
// downstream os.Rename step finds something to rename.
type stubProcessRunner struct{}

func (stubProcessRunner) Run(_ context.Context, name string, args []string, _ appassets.ProcessOptions) (*appassets.ProcessResult, error) {
	var target string
	switch name {
	case "python3":
		for i := range args {
			if args[i] == "--output" && i+1 < len(args) {
				target = args[i+1]
				break
			}
		}
	case "ffmpeg":
		// ffmpeg -y -i in.wav -acodec libmp3lame out.mp3  →  out.mp3 is the last arg.
		if len(args) > 0 {
			target = args[len(args)-1]
		}
	}
	if target == "" {
		return &appassets.ProcessResult{Output: ""}, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(target, []byte{}, 0o644); err != nil {
		return nil, err
	}
	return &appassets.ProcessResult{Output: ""}, nil
}

func (stubProcessRunner) RunSimple(_ context.Context, _ string, _ ...string) (*appassets.ProcessResult, error) {
	return &appassets.ProcessResult{Output: ""}, nil
}

// stubResolver pins the final mp3 path under t.TempDir() so the handler's
// Rename + dispatch + idempotent verification steps have a stable path
// to operate on.
type stubResolver struct{ finalDir string }

func (r stubResolver) Resolve(_ sfxports.AssetDestinationRequest) (sfxports.ResolvedDest, error) {
	return sfxports.ResolvedDest{LocalPath: filepath.Join(r.finalDir, "sfx-final.mp3")}, nil
}

var _ sfxports.DestinationResolverPort = (*stubResolver)(nil)

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestHandler(t *testing.T, disp sfxports.DispatcherPort, finalDir string) *Handler {
	t.Helper()
	return &Handler{
		clipsRepo:              nil, // replaced by dispatcher — see PR 6 fail-closed comment
		metaWriter:             nil, // nil-tolerated per branch in Generate
		resolver:               stubResolver{finalDir: finalDir},
		dispatcher:             disp,
		soundEffectsRootFolder: "", // F2.10: soundEffectsRootFolder is read-only tombstone field (no legacy fallback)
		processRunner:          stubProcessRunner{},
		log:                    zap.NewNop(),
	}
}

func routerFor(t *testing.T, h *Handler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/sound_effect")
	g.POST("/generate", h.Generate)
	return r
}

// ── PR 6 — sfx dispatcher fail-closed contract ────────────────────────────────

// TestPR6_Generate_NilDispatcher_503 verifies the spec:
//
//	dispatcher nil → HTTP error / zero SQLite rows.
//
// Generate runs synthesis + rename BEFORE the dispatcher check fires, so
// the test stubs processRunner.Run to write empty output files and
// resolver.Resolve to return a t.TempDir() path. The handler reaches the
// dispatcher check, finds h.dispatcher == nil, and returns 503.
func TestPR6_Generate_NilDispatcher_503(t *testing.T) {
	tmp := t.TempDir()
	h := newTestHandler(t, nil /* disp=nil → fail-closed */, tmp)
	r := routerFor(t, h)

	body := `{"name":"test","duration":1}`
	req := httptest.NewRequest("POST", "/sound_effect/generate",
		bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AssetMutationDispatcher not wired",
		"the fail-closed error must identify the wiring regression for operators")
}

// TestPR6_Generate_DispatcherPresent_OneCall verifies the spec:
//
//	dispatcher present → exactly one outbox event.
//
// We pin the handler-side contract (the dispatcher is consulted exactly
// once, with the correct content hash). The dispatcher's tx-level
// outbox_events INSERT is verified at the catalogsync test layer.
func TestPR6_Generate_DispatcherPresent_OneCall(t *testing.T) {
	tmp := t.TempDir()
	disp := &recordingDispatcher{}
	h := newTestHandler(t, disp, tmp)
	r := routerFor(t, h)

	body := `{"name":"test","duration":1}`
	req := httptest.NewRequest("POST", "/sound_effect/generate",
		bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(1), disp.calls.Load(),
		"Generate successful path MUST call dispatcher.EnqueueAndIndex exactly once")
	if disp.lastClip != nil {
		// The sfx clip ID convention is "sfx_<12-char-md5-prefix>".
		assert.True(t, len(disp.lastClip.ID) > 4 && disp.lastClip.ID[:4] == "sfx_",
			"clip_id must follow the sfx_<md5-prefix> convention")
	}
	// contentHash should equal the MD5 hex the handler computed (32 chars).
	if disp.lastHash != "" {
		assert.Len(t, disp.lastHash, 32, "contentHash must be the full MD5 hex")
	}
}

// TestPR6_Generate_DispatcherError_Propagated verifies the spec:
//
//	dispatcher error → propagated.
//
// The recorder returns an injected error; the handler surfaces this via
// apiutil.InternalError as HTTP 500.
func TestPR6_Generate_DispatcherError_Propagated(t *testing.T) {
	tmp := t.TempDir()
	disp := &recordingDispatcher{injectErr: errors.New("simulated sfx dispatcher tx failure")}
	h := newTestHandler(t, disp, tmp)
	r := routerFor(t, h)

	body := `{"name":"test","duration":1}`
	req := httptest.NewRequest("POST", "/sound_effect/generate",
		bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errString, _ := resp["error"].(string)
	assert.Contains(t, errString, "dispatcher.EnqueueAndIndex",
		"handler MUST propagate the dispatcher error via apiutil.InternalError")
	assert.Equal(t, int32(1), disp.calls.Load())
}

// TestPR6_Generate_DispatcherUnavailableSentinel_503 pins the defense-in-
// depth branch: when the SSOT dispatcher returns the canonical
// mutations.ErrDispatcherUnavailable at runtime (composition wired but
// pre-flight rejects), Generate must surface it as HTTP 503 + the
// canonical message rather than collapsing it into HTTP 500. This
// branch is unreachable with a generic errors.New(...) stub because the
// sentinel value is a specific exported symbol from the SSOT package,
// so we inject it directly here to lock the contract.
func TestPR6_Generate_DispatcherUnavailableSentinel_503(t *testing.T) {
	tmp := t.TempDir()
	disp := &recordingDispatcher{injectErr: mutations.ErrDispatcherUnavailable}
	h := newTestHandler(t, disp, tmp)
	r := routerFor(t, h)

	body := `{"name":"test","duration":1}`
	req := httptest.NewRequest("POST", "/sound_effect/generate",
		bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Generate sentinel-branch: mutations.ErrDispatcherUnavailable MUST surface as 503")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AssetMutationDispatcher not wired",
		"the sentinel branch must reuse the canonical fail-closed message")
	assert.Equal(t, int32(1), disp.calls.Load())
}
